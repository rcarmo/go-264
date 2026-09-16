package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestRefListsSkipNonReferenceFrames(t *testing.T) {
	d := NewDecoder()
	d.DPB = frame.NewDPB(16)
	d.DPB.Add(&frame.Frame{POC: 0, IsRef: true})
	d.DPB.Add(&frame.Frame{POC: 2, IsRef: false})
	d.DPB.Add(&frame.Frame{POC: 4, IsRef: true})
	d.DPB.Add(&frame.Frame{POC: 6, IsRef: false})

	if got := d.refL0(0); got == nil || got.POC != 4 {
		t.Fatalf("refL0(0) = POC %v, want latest reference POC 4", pocOf(got))
	}
	if got := d.refL0(1); got == nil || got.POC != 0 {
		t.Fatalf("refL0(1) = POC %v, want previous reference POC 0", pocOf(got))
	}
	if got := d.refL1(0); got == nil || got.POC != 0 {
		t.Fatalf("refL1(0) = POC %v, want second-latest reference POC 0", pocOf(got))
	}
}

func TestRefListsKeepLegacySyntheticFrames(t *testing.T) {
	d := NewDecoder()
	d.DPB = frame.NewDPB(16)
	d.DPB.Add(&frame.Frame{POC: 0})
	d.DPB.Add(&frame.Frame{POC: 2})
	d.DPB.Add(&frame.Frame{POC: 4})

	if got := d.refL0(0); got == nil || got.POC != 4 {
		t.Fatalf("legacy refL0(0) = POC %v, want most recent synthetic POC 4", pocOf(got))
	}
	if got := d.refL1(0); got == nil || got.POC != 2 {
		t.Fatalf("legacy refL1(0) = POC %v, want second-most recent synthetic POC 2", pocOf(got))
	}
}

func TestRefL0ListModificationsMayRepeatRecentPicture(t *testing.T) {
	d := NewDecoder()
	d.DPB = frame.NewDPB(16)
	for frameNum, poc := range []int{0, 2, 6, 10, 14} {
		d.DPB.Add(&frame.Frame{FrameNum: frameNum, POC: poc, IsRef: true})
	}
	mods := []syntax.RefPicListModification{
		{Op: 0, Val: 0},  // frame_num 5 - 1 = 4
		{Op: 0, Val: 15}, // subtract MaxPicNum: frame_num 4 again
		{Op: 0, Val: 15}, // frame_num 4 again
		{Op: 0, Val: 0},  // frame_num 3
		{Op: 0, Val: 1},  // frame_num 1
	}
	refs, err := buildPReferenceList(d.DPB.Frames, 5, 16, 5, mods)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{4, 4, 4, 3, 1}
	if len(refs) < len(want) {
		t.Fatalf("modified L0 has %d entries, want at least %d", len(refs), len(want))
	}
	for i, frameNum := range want {
		if refs[i] == nil || refs[i].FrameNum != frameNum {
			t.Fatalf("modified L0[%d] frame_num=%v, want %d", i, frameNumOf(refs[i]), frameNum)
		}
	}
}

func TestActivePReferenceSelectionRejectsMissingPictures(t *testing.T) {
	real := &frame.Frame{FrameNum: 0, IsRef: true}
	missing := &frame.Frame{FrameNum: 1, IsRef: true, NonExisting: true}
	for _, tc := range []struct {
		name  string
		list  []*frame.Frame
		index int8
	}{
		{"negative", []*frame.Frame{real}, -1},
		{"out_of_range", []*frame.Frame{real}, 1},
		{"empty", []*frame.Frame{}, 0},
		{"nil", []*frame.Frame{nil}, 0},
		{"non_existing", []*frame.Frame{missing, real}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder()
			d.DPB.Add(real) // There is a fallback, but it must never be substituted.
			d.slice = &sliceState{}
			d.activeL0Refs = tc.list
			if got := d.refL0(tc.index); got != nil {
				t.Fatalf("invalid selection substituted %v", got)
			}
			if d.slice.referenceErr == nil {
				t.Fatal("invalid reference did not propagate an error")
			}
			first := d.slice.referenceErr
			d.refL0(127)
			if d.slice.referenceErr != first {
				t.Fatal("later selection replaced first reference error")
			}
		})
	}
	d := NewDecoder()
	d.slice = &sliceState{}
	d.activeL0Refs = []*frame.Frame{missing, real}
	if got := d.refL0(1); got != real || d.slice.referenceErr != nil {
		t.Fatalf("real reference after a gap: got %p, err %v", got, d.slice.referenceErr)
	}
}

func frameNumOf(f *frame.Frame) any {
	if f == nil {
		return nil
	}
	return f.FrameNum
}

func pocOf(f *frame.Frame) any {
	if f == nil {
		return nil
	}
	return f.POC
}
