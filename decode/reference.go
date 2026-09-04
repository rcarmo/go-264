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

// validateShortTermReferenceMarking checks a reference picture's retention
// instructions before decoding it, including the allowed picture-number
// differences. It accepts short-term removal and rejects long-term and MMCO 5
// reset operations whose state transitions are not implemented here.
// Target existence and space for the new reference are checked at picture
// completion, as commands change the candidate reference store.
func validateShortTermReferenceMarking(hdr *syntax.Header, maxFrameNum int) error {
	if hdr.LongTermReference {
		return fmt.Errorf("unsupported long-term IDR reference marking")
	}
	for _, mmco := range hdr.MemoryManagementControls {
		switch mmco.Op {
		case 1:
			if uint64(mmco.DifferenceOfPicNumsMinus1) >= uint64(maxFrameNum) {
				return fmt.Errorf("MMCO 1 reference difference %d exceeds MaxPicNum %d", uint64(mmco.DifferenceOfPicNumsMinus1)+1, maxFrameNum)
			}
		case 5:
			return fmt.Errorf("unsupported MMCO 5 picture-number/POC reset")
		default:
			return fmt.Errorf("unsupported long-term reference marking MMCO %d", mmco.Op)
		}
	}
	return nil
}

// buildPReferenceList builds a P slice's List0 from the stored reference
// pictures. Slices of one picture share the reference store, but can choose
// different active counts and reorder or repeat references independently.
// The returned list maps this slice's ref_idx_l0 values to stored pictures
// without changing the reference store (H.264 8.2.4.2.1 and 8.2.4.3.1).
func buildPReferenceList(frames []*frame.Frame, currentFrameNum, maxFrameNum, activeCount int, mods []syntax.RefPicListModification) ([]*frame.Frame, error) {
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

// stageFrameNumGaps handles H.264 8.2.5.2 for a non-IDR progressive picture
// after a previous reference is known. Inputs use validated SPS/header ranges.
// It neither mutates the caller's reference slice nor changes Frame metadata.
// Commit the result only after the current picture succeeds. nextPrev is the
// last inferred reference (or prevRefFrameNum if no gap); a successfully decoded
// reference picture supersedes it with its own frame_num at picture completion.
// POC derivation for non-existing pictures is deliberately separate.
func stageFrameNumGaps(frames []*frame.Frame, prevRefFrameNum, currentFrameNum, maxFrameNum, maxReferences int, gapsAllowed bool) (staged []*frame.Frame, nextPrev int, err error) {
	nextPrev = prevRefFrameNum
	if currentFrameNum == prevRefFrameNum {
		return append([]*frame.Frame(nil), frames...), nextPrev, nil
	}
	distance := (currentFrameNum - prevRefFrameNum + maxFrameNum) % maxFrameNum
	if distance > 1 && !gapsAllowed {
		return nil, nextPrev, fmt.Errorf("unannounced frame_num gap from %d to %d (possible lost reference picture)", prevRefFrameNum, currentFrameNum)
	}
	// 7.4.3: none of the forward interval (previous, current] may already
	// identify a stored short-term reference, even if sliding would evict it.
	for _, f := range frames {
		if f != nil && f.IsRef {
			refDistance := (f.FrameNum - prevRefFrameNum + maxFrameNum) % maxFrameNum
			if refDistance > 0 && refDistance <= distance {
				return nil, nextPrev, fmt.Errorf("frame_num progression from %d to %d reuses stored reference %d", prevRefFrameNum, currentFrameNum, f.FrameNum)
			}
		}
	}
	staged = append([]*frame.Frame(nil), frames...)
	for missing := (prevRefFrameNum + 1) % maxFrameNum; missing != currentFrameNum; missing = (missing + 1) % maxFrameNum {
		staged = slidingWindowReferences(staged, missing, maxFrameNum, maxReferences)
		// A placeholder consumes a reference slot but never owns image samples.
		staged = append(staged, &frame.Frame{FrameNum: missing, IsRef: true, NonExisting: true})
		nextPrev = missing
	}
	return staged, nextPrev, nil
}

// slidingWindowReferences makes room for one new reference when the store is
// full by removing the oldest short-term picture. Age follows wrap-aware
// picture numbers, so frame_num wrap does not make a new picture look old.
// The marking limit is max(maxReferences, 1), as specified in H.264 8.2.5.3.
// refs must be an owned slice; removal changes membership without changing
// the stored Frame objects. Adaptive marking uses explicit commands instead.
func slidingWindowReferences(refs []*frame.Frame, currentFrameNum, maxFrameNum, maxReferences int) []*frame.Frame {
	limit := max(maxReferences, 1)
	count, oldest := 0, -1
	for i, f := range refs {
		if f != nil && f.IsRef {
			count++
			if oldest < 0 || shortTermPicNum(f.FrameNum, currentFrameNum, maxFrameNum) < shortTermPicNum(refs[oldest].FrameNum, currentFrameNum, maxFrameNum) {
				oldest = i
			}
		}
	}
	if count >= limit {
		copy(refs[oldest:], refs[oldest+1:])
		refs[len(refs)-1] = nil
		refs = refs[:len(refs)-1]
	}
	return refs
}
