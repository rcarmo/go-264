package decode

import (
	"fmt"
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
	"sort"
)

// Allocation-heavy original algorithm, independent of the compact/shift path.
func originalPReferenceList(frames []*frame.Frame, currentFrameNum, maxFrameNum, activeCount int, mods []syntax.RefPicListModification) ([]*frame.Frame, error) {
	var refs []*frame.Frame
	realReference := false
	for _, f := range frames {
		if f != nil && f.IsRef {
			refs = append(refs, f)
			realReference = realReference || !f.NonExisting
		}
	}
	// 8.2.4.2.1 requires a real stored reference even for an all-intra P slice.
	// It need not survive truncation into the initial active-list prefix.
	if !realReference {
		return nil, fmt.Errorf("P slice has no decoded reference picture")
	}
	if len(mods) > activeCount {
		return nil, fmt.Errorf("P list has %d modifications for %d active references", len(mods), activeCount)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].IsLongTerm != refs[j].IsLongTerm {
			return !refs[i].IsLongTerm
		}
		if refs[i].IsLongTerm {
			return refs[i].LongTermFrameIdx < refs[j].LongTermFrameIdx
		}
		return shortTermPicNum(refs[i].FrameNum, currentFrameNum, maxFrameNum) >
			shortTermPicNum(refs[j].FrameNum, currentFrameNum, maxFrameNum)
	})

	// Initial missing entries remain nil: modifications may fill them by
	// repeating a real reference. They must all be filled before reconstruction.
	list := make([]*frame.Frame, activeCount)
	copy(list, refs)
	// Each modification fills the next List0 position. Short-term op 0
	// subtracts mod.Val+1 and op 1 adds it, wrapping modulo maxFrameNum.
	// The predictor starts at the current frame_num; each short-term command
	// continues from the previous short-term command's result.
	predicted := currentFrameNum
	for index, mod := range mods {
		var selected *frame.Frame
		switch mod.Op {
		case 0, 1:
			if uint64(mod.Val) >= uint64(maxFrameNum) {
				return nil, fmt.Errorf("P reference difference %d exceeds MaxPicNum %d", uint64(mod.Val)+1, maxFrameNum)
			}
			diff := int(mod.Val) + 1
			if mod.Op == 0 {
				predicted = (predicted - diff + maxFrameNum) % maxFrameNum
			} else {
				predicted = (predicted + diff) % maxFrameNum
			}
			// Search the complete store, not just the truncated active list.
			for _, f := range refs {
				if !f.IsLongTerm && f.FrameNum == predicted {
					selected = f
					break
				}
			}
			if selected == nil {
				return nil, fmt.Errorf("P list modification refers to missing frame_num %d", predicted)
			}
		case 2:
			// LongTermPicNum is the frame index for progressive pictures. This
			// operation does not change picNumLXPred used by later op0/op1.
			for _, f := range refs {
				if f.IsLongTerm && uint64(f.LongTermFrameIdx) == uint64(mod.Val) {
					selected = f
					break
				}
			}
			if selected == nil {
				return nil, fmt.Errorf("P list modification refers to missing long_term_pic_num %d", mod.Val)
			}
		default:
			return nil, fmt.Errorf("invalid P reference list operation %d", mod.Op)
		}
		if selected.NonExisting {
			return nil, fmt.Errorf("P list modification refers to non-existing frame_num %d", predicted)
		}
		// Preserve earlier selections, including repetitions. Only later copies
		// of this picture are removed when inserting at the current list index.
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
	for index, f := range list {
		if f == nil {
			return nil, fmt.Errorf("P reference list entry %d has no reference picture", index)
		}
	}
	return list, nil
}
