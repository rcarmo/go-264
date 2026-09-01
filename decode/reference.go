package decode

import (
	"fmt"
	"sort"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

// shortTermPicNum makes stored reference numbers comparable across frame_num
// wraparound. A stored number above the current number is from before the
// wrap, so subtract the modulus to place it before newer references.
// For example, with modulus 32 and current number 1, references 31 and 0
// become -1 and 0. This is FrameNumWrap/PicNum for progressive frames
// (H.264 8.2.4.1).
func shortTermPicNum(frameNum, currentFrameNum, maxFrameNum int) int {
	if frameNum > currentFrameNum {
		return frameNum - maxFrameNum
	}
	return frameNum
}

// buildPReferenceList builds a P slice's List0 from the stored reference
// pictures. Slices of one picture share the reference store, but can choose
// different active counts and reorder or repeat references independently.
// The returned list maps this slice's ref_idx_l0 values to stored pictures
// without changing the reference store (H.264 8.2.4.2.1 and 8.2.4.3.1).
func buildPReferenceList(frames []*frame.Frame, currentFrameNum, maxFrameNum, activeCount int, mods []syntax.RefPicListModification) ([]*frame.Frame, error) {
	var refs []*frame.Frame
	for _, f := range frames {
		if f != nil && f.IsRef {
			refs = append(refs, f)
		}
	}
	// 8.2.4.2.1 requires a real stored reference even for an all-intra P slice.
	// It need not survive truncation into the initial active-list prefix.
	if len(refs) == 0 {
		return nil, fmt.Errorf("P slice has no decoded reference picture")
	}
	if len(mods) > activeCount {
		return nil, fmt.Errorf("P list has %d modifications for %d active references", len(mods), activeCount)
	}
	sort.Slice(refs, func(i, j int) bool {
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
		if mod.Op != 0 && mod.Op != 1 {
			return nil, fmt.Errorf("unsupported P reference list operation %d", mod.Op)
		}
		if uint64(mod.Val) >= uint64(maxFrameNum) {
			return nil, fmt.Errorf("P reference difference %d exceeds MaxPicNum %d", uint64(mod.Val)+1, maxFrameNum)
		}
		diff := int(mod.Val) + 1
		if mod.Op == 0 {
			predicted = (predicted - diff + maxFrameNum) % maxFrameNum
		} else {
			predicted = (predicted + diff) % maxFrameNum
		}
		var selected *frame.Frame
		// Search the complete reference store, not just the truncated active list.
		for _, f := range refs {
			if f.FrameNum == predicted {
				selected = f
				break
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("P list modification refers to missing frame_num %d", predicted)
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
