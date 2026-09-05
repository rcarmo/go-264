package decode

import (
	"strings"
	"testing"

	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

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
