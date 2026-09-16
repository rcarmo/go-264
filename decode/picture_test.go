package decode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func TestPictureComponentsRemainIndependentWhenGrown(t *testing.T) {
	for _, owned := range []bool{false, true} {
		f := newPictureFrame(2, 2)
		f.MotionL1[0] = [2]int16{7, 8}
		f.TemporalRefIdxL0[0], f.RefIdxL1[0] = 2, 3
		f.RefListL0POC, f.RefListL0Num = []int{12}, []int{4}
		if owned {
			f = ownedOutput(f)
		}
		// Batch frames and streaming copies expose growable slices. Sharing an
		// allocation must not let append on one component overwrite the next.
		f.Y = append(f.Y, 99)
		f.U = append(f.U, 99)
		if f.U[0] != 128 || f.V[0] != 128 {
			t.Fatalf("owned=%v: growing one pixel plane overwrote another", owned)
		}
		f.MotionL0 = append(f.MotionL0, [2]int16{99, 99})
		f.RefIdxL0 = append(f.RefIdxL0, 99)
		f.TemporalRefIdxL0 = append(f.TemporalRefIdxL0, 99)
		f.RefListL0POC = append(f.RefListL0POC, 99)
		if f.MotionL1[0] != [2]int16{7, 8} || f.TemporalRefIdxL0[0] != 2 || f.RefIdxL1[0] != 3 || f.RefListL0Num[0] != 4 {
			t.Fatalf("owned=%v: growing one metadata array overwrote another", owned)
		}
	}
}

func TestPictureScratchReuseAcrossEntropyModesAndAbort(t *testing.T) {
	d := NewDecoder()
	var retained, snapshots []*frame.Frame
	var reused *[16]int
	for _, name := range []string{"high", "cavlc", "cabac", "high"} {
		input := syntaxTestInput(t, name)
		fresh := NewDecoder()
		want, err := fresh.Decode(input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Decode(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s after scratch reuse: %v; outputs differ=%v", name, err, !reflect.DeepEqual(got, want))
		}
		if reused != nil && &d.scratch.nzCtx[0] != reused {
			t.Fatal("same-size picture allocated new scratch")
		}
		reused = &d.scratch.nzCtx[0]
		// Compare contexts as well as pixels: switching entropy/transform modes
		// must not leave stale cells that this picture happened not to consume.
		if !reflect.DeepEqual(d.scratch, fresh.scratch) {
			t.Fatalf("%s retained contexts from the preceding picture", name)
		}
		retained = append(retained, got[0])
		snapshots = append(snapshots, ownedOutput(got[0]))
		// Truncation happens after reconstruction has dirtied scratch. The next
		// IDR must recover using the same arrays, not inherit partial contexts.
		if frames, err := d.Decode(input[:len(input)-8]); err == nil || len(frames) != 0 || d.picture != nil {
			t.Fatalf("%s truncated picture was not abandoned: frames=%d err=%v", name, len(frames), err)
		}
		for i := range retained {
			if !reflect.DeepEqual(retained[i], snapshots[i]) {
				t.Fatalf("reusing or abandoning scratch modified retained frame %d", i)
			}
		}
	}
}

func TestPictureScratchGeometryChangeAndSmallerBudget(t *testing.T) {
	d := assemblyDecoder(2, 1)
	if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 81, 149))); err != nil {
		t.Fatal(err)
	}
	buffer := &d.scratch.motion.mv[0][0]
	// The number of cells is unchanged, but raster indexing uses a new stride.
	d.SPS[0] = assemblyDecoder(1, 2).SPS[0]
	frames, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 91, 102)))
	if err != nil || len(frames) != 1 {
		t.Fatalf("aspect-ratio change: %v", err)
	}
	if &d.scratch.motion.mv[0][0] != buffer || d.scratch.motion.stride4 != 4 || frames[0].PixelY(0, 16) != 102 {
		t.Fatal("equal-size scratch did not rebind the new raster geometry")
	}
	// Shrinking the picture/budget releases peak-capacity scratch rather than
	// keeping the old larger backing arrays behind smaller slice lengths.
	d.MaxFrameMacroblocks = 1
	d.SPS[0] = assemblyDecoder(1, 1).SPS[0]
	frames, err = d.Decode(assemblyInput(pcmAssemblySlice(0, 77)))
	if err != nil || len(frames) != 1 || frames[0].Y[0] != 77 {
		t.Fatalf("smaller picture/budget: %v", err)
	}
	if cap(d.scratch.deblock) != 1 || cap(d.scratch.nzCtx) != 1 || cap(d.scratch.motion.mv[0]) != 16 || cap(d.scratch.intraModes) != 16 {
		t.Fatal("smaller budget retained oversized scratch capacity")
	}
}

func TestInvalidCallerCropDoesNotCommitPicture(t *testing.T) {
	d := primedReferenceDecoder(t, false)
	before, ref, sps := d.pocState(), d.DPB.Frames[0], d.referenceSPS
	// Public parameter maps can be populated without ParseSPS. This crop
	// extends past the coded picture and must fail before the new IDR commits.
	d.SPS[0].FrameCropping, d.SPS[0].CropLeft = true, 1
	_, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 102)))
	if err == nil || !strings.Contains(err.Error(), "crop:") {
		t.Fatalf("invalid caller crop: %v", err)
	}
	if d.pocState() != before || len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != ref || d.referenceSPS != sps || len(d.Frames) != 1 {
		t.Fatal("invalid crop committed picture state")
	}
}

func TestSliceSnapshotsParameterSets(t *testing.T) {
	prefix, unit := firstSyntaxTestSlice(t, "cavlc")
	d := NewDecoder()
	if _, err := d.Decode(prefix); err != nil {
		t.Fatal(err)
	}
	s, err := d.parseSlice(unit)
	if err != nil {
		t.Fatal(err)
	}
	originalWidth, originalQP := s.sps.PicWidthInMbs, s.pps.PicInitQP
	d.SPS[s.sps.SPSID].PicWidthInMbs++
	d.PPS[s.pps.PPSID].PicInitQP++
	if s.sps.PicWidthInMbs != originalWidth || s.pps.PicInitQP != originalQP {
		t.Fatal("active slice aliases mutable parameter registry")
	}
	next, err := d.parseSlice(unit)
	if err != nil {
		t.Fatal(err)
	}
	if next.sps.PicWidthInMbs != originalWidth+1 || next.pps.PicInitQP != originalQP+1 {
		t.Fatal("later slice did not bind updated parameter sets")
	}
}

func TestPictureIdentity(t *testing.T) {
	fresh := func() *sliceState {
		return &sliceState{unit: nal.Unit{Type: nal.TypeSliceIDR, RefIDC: 3},
			sps: &nal.SPS{PicOrderCntType: 0}, header: &syntax.Header{FrameNum: 7, PPSID: 2, PicOrderCntLsb: 9}}
	}
	tests := []struct {
		name   string
		change func(*sliceState)
		same   bool
	}{
		{"later macroblock", func(s *sliceState) { s.header.FirstMbInSlice = 8 }, true},
		{"slice type", func(s *sliceState) { s.header.SliceType = syntax.SliceTypeI }, true},
		{"nonzero reference priority", func(s *sliceState) { s.unit.RefIDC = 1 }, true},
		{"slice QP", func(s *sliceState) { s.header.SliceQPDelta = 3 }, true},
		{"frame number", func(s *sliceState) { s.header.FrameNum++ }, false},
		{"PPS", func(s *sliceState) { s.header.PPSID++ }, false},
		{"nonreference", func(s *sliceState) { s.unit.RefIDC = 0 }, false},
		{"IDR flag", func(s *sliceState) { s.unit.Type = nal.TypeSliceNonIDR }, false},
		{"IDR id", func(s *sliceState) { s.header.IdrPicID++ }, false},
		{"POC LSB", func(s *sliceState) { s.header.PicOrderCntLsb++ }, false},
		{"bottom POC", func(s *sliceState) { s.header.DeltaPicOrderCntBottom++ }, false},
		{"field flag", func(s *sliceState) { s.header.FieldPicFlag = true }, false},
		{"unused bottom flag", func(s *sliceState) { s.header.BottomFieldFlag = true }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := fresh(), fresh()
			tt.change(b)
			if got := identifyPicture(a) == identifyPicture(b); got != tt.same {
				t.Fatalf("same picture=%v, want %v", got, tt.same)
			}
		})
	}
	a, b := fresh(), fresh()
	a.sps.PicOrderCntType, b.sps.PicOrderCntType = 1, 1
	b.header.DeltaPicOrderCnt[1] = 1
	if identifyPicture(a) == identifyPicture(b) {
		t.Fatal("type-1 POC delta did not split pictures")
	}
}
