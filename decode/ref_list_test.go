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
	refs := d.refL0ListWithMods(5, mods)
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
