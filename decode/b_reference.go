package decode

import (
	"fmt"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

// buildBModifiedList applies H.264 reference picture list modification to an
// already constructed default B list. Selection searches the complete DPB;
// insertion preserves prior selections and removes only later duplicates.
func buildBModifiedList(initial, store []*frame.Frame, currentFrameNum, maxPicNum, activeCount int, mods []syntax.RefPicListModification) ([]*frame.Frame, error) {
	if maxPicNum <= 0 || activeCount < 0 {
		return nil, fmt.Errorf("invalid B reference list limits")
	}
	if len(mods) > activeCount {
		return nil, fmt.Errorf("B list has %d modifications for %d active references", len(mods), activeCount)
	}
	list := make([]*frame.Frame, activeCount)
	copy(list, initial)
	predicted := currentFrameNum
	for index, mod := range mods {
		var selected *frame.Frame
		switch mod.Op {
		case 0, 1:
			if uint64(mod.Val) >= uint64(maxPicNum) {
				return nil, fmt.Errorf("B reference difference %d exceeds MaxPicNum %d", uint64(mod.Val)+1, maxPicNum)
			}
			diff := int(mod.Val) + 1
			if mod.Op == 0 {
				predicted = (predicted - diff + maxPicNum) % maxPicNum
			} else {
				predicted = (predicted + diff) % maxPicNum
			}
			for _, f := range store {
				if f != nil && f.IsRef && !f.IsLongTerm && f.FrameNum == predicted {
					selected = f
					break
				}
			}
		case 2:
			for _, f := range store {
				if f != nil && f.IsRef && f.IsLongTerm && uint64(f.LongTermFrameIdx) == uint64(mod.Val) {
					selected = f
					break
				}
			}
		default:
			return nil, fmt.Errorf("invalid B reference list operation %d", mod.Op)
		}
		if selected == nil || selected.NonExisting {
			return nil, fmt.Errorf("B list modification refers to unavailable picture")
		}
		tail := append([]*frame.Frame(nil), list[index:]...)
		list[index] = selected
		write := index + 1
		for _, f := range tail {
			if f != selected && write < len(list) {
				list[write] = f
				write++
			}
		}
		for write < len(list) {
			list[write] = nil
			write++
		}
	}
	for i, f := range list {
		if f == nil {
			return nil, fmt.Errorf("B reference list entry %d has no reference picture", i)
		}
	}
	return list, nil
}
