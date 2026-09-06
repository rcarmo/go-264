package decode

import (
	"strings"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func referenceSkipSlice(number uint32, mods []syntax.RefPicListModification, mmco []syntax.MemoryManagementControl) nal.Unit {
	w := &assemblyBits{}
	w.ue(0)
	w.ue(syntax.SliceTypeP)
	w.ue(0)
	w.uint(number, 4)
	w.bit(0) // default one active reference
	if len(mods) > 0 {
		w.bit(1)
		for _, m := range mods {
			w.ue(m.Op)
			w.ue(m.Val)
		}
		w.ue(3)
	} else {
		w.bit(0)
	}
	if len(mmco) > 0 {
		w.bit(1)
		for _, m := range mmco {
			w.ue(m.Op)
			if m.Op == 1 {
				w.ue(m.DifferenceOfPicNumsMinus1)
			}
		}
		w.ue(0)
	} else {
		w.bit(0)
	}
	w.ue(0)
	w.ue(1)
	w.ue(1) // QP delta0, filter off, one skipped MB
	w.bit(1)
	w.align()
	return nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 1, Payload: w.bytes()}
}

func primedReferenceDecoder(t *testing.T, gaps bool) *Decoder {
	t.Helper()
	d := assemblyDecoder(1, 1)
	d.SPS[0].GapsInFrameNumValueAllowedFlag = gaps
	if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 91))); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLegalGapRetainsMetadataButNeverOutputsInferredPicture(t *testing.T) {
	d := primedReferenceDecoder(t, true)
	good := d.DPB.Frames[0]
	// frame_num1 is inferred; explicitly select actual frame_num0, not gap1.
	frames, err := d.Decode(assemblyInput(referenceSkipSlice(2, []syntax.RefPicListModification{{Op: 0, Val: 1}}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || len(d.Frames) != 2 || frames[0].PixelY(0, 0) != 91 {
		t.Fatal("gap produced pixels/output or changed prediction")
	}
	if len(d.DPB.Frames) != 3 || d.DPB.Frames[0] != good || !d.DPB.Frames[1].NonExisting || len(d.DPB.Frames[1].Y) != 0 || d.DPB.Frames[1].FrameNum != 1 {
		t.Fatal("gap reference metadata missing or owns pixels")
	}
	if !d.prevRefFrameNumValid || d.prevRefFrameNum != 2 {
		t.Fatal("reference progression not committed")
	}
}

func TestInvalidReferencesDoNotChangeCommittedState(t *testing.T) {
	for _, tt := range []struct {
		name string
		gap  bool
		unit nal.Unit
		want string
	}{
		{"unannounced gap", false, referenceSkipSlice(2, nil, nil), "unannounced frame_num gap"},
		{"sampling inferred reference", true, referenceSkipSlice(2, nil, nil), "non-existing frame_num"},
		{"modifying to inferred reference", true, referenceSkipSlice(2, []syntax.RefPicListModification{{Op: 0, Val: 0}}, nil), "non-existing frame_num"},
		{"missing modification", false, referenceSkipSlice(1, []syntax.RefPicListModification{{Op: 0, Val: 2}}, nil), "missing frame_num"},
		{"missing MMCO target", false, referenceSkipSlice(1, nil, []syntax.MemoryManagementControl{{Op: 1, DifferenceOfPicNumsMinus1: 2}}), "MMCO1 refers to missing"},
		{"MMCO fails after staged removal", false, referenceSkipSlice(1, nil, []syntax.MemoryManagementControl{{Op: 1}, {Op: 1}}), "MMCO1 refers to missing"},
		{"repeated reference number", false, referenceSkipSlice(0, nil, nil), "repeats previous reference"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := primedReferenceDecoder(t, tt.gap)
			good := d.DPB.Frames[0]
			before := d.pocState()
			frames, err := d.Decode(assemblyInput(tt.unit))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if len(frames) != 0 || len(d.Frames) != 1 || len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != good || d.prevRefFrameNum != 0 || d.pocState() != before {
				t.Fatal("failed picture mutated committed reference/output/POC state")
			}
		})
	}
}

func TestAdaptiveShortTermRemovalCommitsAfterCompletePicture(t *testing.T) {
	d := primedReferenceDecoder(t, false)
	frames, err := d.Decode(assemblyInput(referenceSkipSlice(1, nil, []syntax.MemoryManagementControl{{Op: 1}})))
	if err != nil || len(frames) != 1 {
		t.Fatalf("valid MMCO1: %v", err)
	}
	if len(d.DPB.Frames) != 1 || d.DPB.Frames[0].FrameNum != 1 || d.prevRefFrameNum != 1 {
		t.Fatal("MMCO1 did not replace reference")
	}
	// A later IDR resets both the stored reference set and number progression.
	if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 121))); err != nil {
		t.Fatal(err)
	}
	if len(d.DPB.Frames) != 1 || d.prevRefFrameNum != 0 || d.DPB.Frames[0].PixelY(0, 0) != 121 {
		t.Fatal("IDR retained prior references/progression")
	}
}

func TestLaterSliceCannotBypassReferenceGates(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*Decoder, *sliceState)
		want  string
	}{
		{"P with zero SPS references", func(d *Decoder, s *sliceState) {
			d.picture.sps.MaxNumRefFrames = 0
			s.sps.MaxNumRefFrames = 0
			s.header.SliceType = syntax.SliceTypeP
		}, "max_num_ref_frames"},
		{"long term IDR", func(d *Decoder, s *sliceState) { s.header.LongTermReference = true }, "long-term"},
		{"B across inferred gaps", func(d *Decoder, s *sliceState) {
			d.picture.referenceFrames = []*frame.Frame{{IsRef: true, NonExisting: true}}
			s.header.SliceType = syntax.SliceTypeB
		}, "B pictures across"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := assemblyDecoder(2, 1)
			first, err := d.parseSlice(pcmAssemblySlice(0, 91))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.addSlice(first); err != nil {
				t.Fatal(err)
			}
			later, err := d.parseSlice(pcmAssemblySlice(1, 121))
			if err != nil {
				t.Fatal(err)
			}
			tt.setup(d, later)
			if err := d.addSlice(later); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("later slice bypassed gate: %v", err)
			}
		})
	}
}

func TestSPSReferenceLayoutChangeRequiresIDR(t *testing.T) {
	for _, change := range []func(*nal.SPS){
		func(s *nal.SPS) { s.Log2MaxFrameNum = 5 },
		func(s *nal.SPS) { s.MaxNumRefFrames = 1 },
	} {
		d := primedReferenceDecoder(t, false)
		prior := d.DPB.Frames[0]
		next := *d.SPS[0]
		change(&next)
		d.SPS[0] = &next
		unit := referenceSkipSlice(1, nil, nil)
		// Admission receives already parsed fields; exercise the cross-picture
		// state check independently of the changed frame_num header bit width.
		s := &sliceState{unit: unit, sps: &next, pps: d.PPS[0], header: &syntax.Header{SliceType: syntax.SliceTypeP, FrameNum: 1}}
		if _, _, _, err := d.preparePictureReferences(s); err == nil || !strings.Contains(err.Error(), "SPS changed") {
			t.Fatalf("layout changed without reset: %v", err)
		}
		if len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != prior {
			t.Fatal("rejected SPS changed reference storage")
		}
		s.unit.Type = nal.TypeSliceIDR
		s.header.FrameNum = 0
		if _, _, _, err := d.preparePictureReferences(s); err != nil {
			t.Fatalf("IDR did not permit SPS replacement: %v", err)
		}
	}
}
