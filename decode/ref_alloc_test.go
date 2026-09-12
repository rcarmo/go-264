package decode

import (
	"github.com/rcarmo/go-264/frame"
	"math/rand"
	"testing"
)

func referenceL1Legacy(frames []*frame.Frame, ref int8, currentPOC, currentOrder, maxPOC int, useFull bool) *frame.Frame {
	type item struct {
		fr  *frame.Frame
		poc int
	}
	var future, past []item
	wrap := maxPOC > 0 && currentPOC > (3*maxPOC)/4
	for _, fr := range frames {
		if fr == nil || !fr.IsRef {
			continue
		}
		p := fr.POC
		if useFull {
			p = frameOrderPOC(fr)
		}
		if !useFull && p == fr.POC && wrap && fr.POC < maxPOC/4 {
			p += maxPOC
		}
		if p > currentOrder {
			future = append(future, item{fr, p})
		} else {
			past = append(past, item{fr, p})
		}
	}
	for i := 0; i < len(future)-1; i++ {
		for j := i + 1; j < len(future); j++ {
			if future[j].poc < future[i].poc || (future[j].poc == future[i].poc && future[j].fr.FrameNum > future[i].fr.FrameNum) {
				future[i], future[j] = future[j], future[i]
			}
		}
	}
	for i := 0; i < len(past)-1; i++ {
		for j := i + 1; j < len(past); j++ {
			if past[j].poc > past[i].poc || (past[j].poc == past[i].poc && past[j].fr.FrameNum > past[i].fr.FrameNum) {
				past[i], past[j] = past[j], past[i]
			}
		}
	}
	all := append(future, past...)
	if len(all) == 0 {
		return nil
	}
	i := max(0, int(ref))
	i = min(i, len(all)-1)
	return all[i].fr
}

func TestL1StackListMatchesLegacyOrdering(t *testing.T) {
	t.Setenv("GO264_REF_LIST_TRACE", "")
	rng := rand.New(rand.NewSource(264))
	for _, count := range []int{1, 2, 16, 32, 33, 64} {
		d := NewDecoder()
		d.DPB = frame.NewDPB(count)
		d.maxPOCLSB = 64
		for i := 0; i < count; i++ {
			d.DPB.Add(&frame.Frame{POC: rng.Intn(64), FullPOC: rng.Intn(192), FrameNum: rng.Intn(32), IsRef: i%5 != 4})
		}
		for _, full := range []bool{false, true} {
			for _, poc := range []int{0, 20, 52, 63} {
				for _, ref := range []int8{-1, 0, 1, 15, 31, 63, 127} {
					got := d.refBidiL1Ordered(ref, poc, poc, full)
					want := referenceL1Legacy(d.DPB.Frames, ref, poc, poc, 64, full)
					if got != want {
						t.Fatal(count, full, poc, ref)
					}
				}
			}
		}
	}
}
func TestL1ReferenceZeroAlloc(t *testing.T) {
	t.Setenv("GO264_REF_LIST_TRACE", "")
	d := NewDecoder()
	d.DPB = frame.NewDPB(16)
	for i := 0; i < 16; i++ {
		d.DPB.Add(&frame.Frame{POC: i * 2, FullPOC: i * 2, FrameNum: i, IsRef: true})
	}
	allocs := testing.AllocsPerRun(100, func() {
		if d.refBidiL1Ordered(2, 15, 15, true) == nil {
			panic("ref")
		}
	})
	if allocs != 0 {
		t.Fatal(allocs)
	}
}
