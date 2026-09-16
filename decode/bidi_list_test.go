package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestDefaultBidiL1SwapsIdenticalLists(t *testing.T) {
	d := NewDecoder()
	d.DPB = frame.NewDPB(4)
	for _, poc := range []int{4, 8, 12} {
		d.DPB.Add(&frame.Frame{POC: poc, FullPOC: poc, FrameNum: poc / 4, IsRef: true})
	}
	got := d.defaultBidiL1Frames(16)
	if len(got) != 3 || got[0].FullPOC != 8 || got[1].FullPOC != 12 || got[2].FullPOC != 4 {
		t.Fatalf("swapped L1 POCs=%v", []int{got[0].FullPOC, got[1].FullPOC, got[2].FullPOC})
	}
}

func TestDefaultBidiL1ModificationAppliesAfterSwap(t *testing.T) {
	d := NewDecoder()
	d.DPB = frame.NewDPB(4)
	for _, poc := range []int{4, 8, 12} {
		d.DPB.Add(&frame.Frame{POC: poc, FullPOC: poc, FrameNum: poc / 4, IsRef: true})
	}
	// CurrPicNum 4 minus (abs_diff_minus1 0 + 1) selects frame_num 3.
	base := d.defaultBidiL1Frames(16)
	got, err := buildBModifiedList(base, d.DPB.Frames, 4, 16, 3, []syntax.RefPicListModification{{Op: 0, Val: 0}})
	if err != nil || len(got) == 0 || got[0].FrameNum != 3 {
		t.Fatalf("modified L1[0]=%v", got)
	}
}
