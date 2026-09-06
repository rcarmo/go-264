package decode

import (
	"fmt"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

// preparePictureReferences binds a candidate reference list to a new picture.
// An IDR starts empty; other pictures copy committed references after checking
// sequence parameters and frame-number continuity. The caller owns the returned
// pointer slice, while the existing picture buffers remain shared.
// Permitted frame-number gaps add metadata-only references to this copy.
// The returned number and validity flag carry the previous-reference state
// forward until the current picture successfully commits.
func (d *Decoder) preparePictureReferences(s *sliceState) ([]*frame.Frame, int, bool, error) {
	h, sps := s.header, s.sps
	maxFrameNum := 1 << sps.Log2MaxFrameNum
	if s.unit.Type == nal.TypeSliceIDR {
		if h.FrameNum != 0 {
			return nil, 0, false, fmt.Errorf("IDR frame_num must be zero")
		}
		return nil, 0, true, nil
	}
	// An SPS stays active for the coded video sequence (7.4.1.2.1). In
	// particular, reusing reference numbers under a changed modulus corrupts
	// gap arithmetic; shrinking the reference limit can overfill the DPB.
	if d.referenceSPS != nil && *d.referenceSPS != *sps {
		return nil, 0, false, fmt.Errorf("SPS changed without an IDR picture")
	}
	refs := append([]*frame.Frame(nil), d.DPB.Frames...)
	next, valid := d.prevRefFrameNum, d.prevRefFrameNumValid
	if valid {
		if int(h.FrameNum) == next {
			return nil, 0, false, fmt.Errorf("new progressive picture repeats previous reference frame_num %d", next)
		}
		var err error
		refs, next, err = stageFrameNumGaps(refs, next, int(h.FrameNum), maxFrameNum, int(sps.MaxNumRefFrames), sps.GapsInFrameNumValueAllowedFlag)
		if err != nil {
			return nil, 0, false, err
		}
	}
	return refs, next, valid, nil
}

// commitPictureReferences applies the completed picture's reference marking
// and retains the picture if it is a reference. Explicit commands run in
// bitstream order; otherwise automatic marking makes room for the picture.
// The reference store and numbering state are published together only after
// all marking checks succeed, so an error leaves committed references intact.
// Call once after reconstructing every slice. Non-reference pictures remain
// output-only.
func (d *Decoder) commitPictureReferences(p *pictureState) error {
	h := p.slices[0].header
	refs := p.referenceFrames
	maxLongTerm := d.maxLongTermFrameIdx
	if p.frame.IsRef {
		var current *frame.Frame
		var err error
		refs, current, maxLongTerm, err = stageReferenceMarking(refs, p.frame, h,
			1<<p.sps.Log2MaxFrameNum, int(p.sps.MaxNumRefFrames), maxLongTerm)
		if err != nil {
			return err
		}
		p.frame = current
		p.nextPrevRefFrameNum, p.nextPrevRefValid = current.FrameNum, true
	}
	// Non-reference pictures remain output-only. Non-existing pictures remain
	// reference metadata only and are never added to decoded output/history.
	d.referenceSPS = p.sps
	d.DPB.Frames = refs
	d.DPB.MaxSize = max(int(p.sps.MaxNumRefFrames), 1)
	d.maxLongTermFrameIdx = maxLongTerm
	d.prevRefFrameNum, d.prevRefFrameNumValid = p.nextPrevRefFrameNum, p.nextPrevRefValid
	return nil
}

// stageReferenceMarking applies 8.2.5.3/8.2.5.4 in command order. Both the
// reference slice and any changed Frame metadata are owned copies: a rejected
// later command cannot change a previously published picture or the live DPB.
// Pixel and motion buffers are immutable here and can stay shared.
func stageReferenceMarking(previous []*frame.Frame, current *frame.Frame, h *syntax.Header, maxFrameNum, maxReferences, maxLongTerm int) ([]*frame.Frame, *frame.Frame, int, error) {
	if err := validateReferenceMarking(h, maxFrameNum); err != nil {
		return nil, nil, maxLongTerm, err
	}
	refs := append([]*frame.Frame(nil), previous...)
	marked := *current
	marked.IsLongTerm, marked.LongTermFrameIdx = false, 0
	if current.IsIDR {
		refs, maxLongTerm = nil, -1
		if h.LongTermReference {
			// 7.4.3.3 forbids long-term IDR marking when the SPS allows no references.
			if maxReferences == 0 {
				return nil, nil, maxLongTerm, fmt.Errorf("long-term IDR requires max_num_ref_frames greater than zero")
			}
			marked.IsLongTerm, maxLongTerm = true, 0
		}
	} else if h.AdaptiveRefPicMarking {
		for _, op := range h.MemoryManagementControls {
			switch op.Op {
			case 1, 3:
				target := (int(h.FrameNum) - int(op.DifferenceOfPicNumsMinus1) - 1 + maxFrameNum) % maxFrameNum
				found := findShortReference(refs, target)
				if found < 0 {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO%d refers to missing frame_num %d", op.Op, target)
				}
				if op.Op == 1 {
					refs = removeReference(refs, found)
					continue
				}
				// 8.2.5.4.3 permits promotion only of decoded pictures, not gap placeholders.
				if refs[found].NonExisting {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO3 refers to non-existing frame_num %d", target)
				}
				if maxLongTerm < 0 || uint64(op.LongTermFrameIdx) > uint64(maxLongTerm) {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO3 long_term_frame_idx %d exceeds limit %d", op.LongTermFrameIdx, maxLongTerm)
				}
				index := int(op.LongTermFrameIdx)
				if marked.IsLongTerm && marked.LongTermFrameIdx == index {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO3 would unmark current long-term picture")
				}
				promoted := *refs[found]
				promoted.IsLongTerm, promoted.LongTermFrameIdx = true, index
				refs = removeReference(refs, found)
				if old := findLongReference(refs, index); old >= 0 {
					refs = removeReference(refs, old)
				}
				refs = append(refs, &promoted)
			case 2:
				if marked.IsLongTerm && uint64(marked.LongTermFrameIdx) == uint64(op.LongTermPicNum) {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO2 would unmark current long-term picture")
				}
				found := -1
				if op.LongTermPicNum <= 15 {
					found = findLongReference(refs, int(op.LongTermPicNum))
				}
				if found < 0 {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO2 refers to missing long_term_pic_num %d", op.LongTermPicNum)
				}
				refs = removeReference(refs, found)
			case 4:
				if uint64(op.MaxLongTermFrameIdxPlus1) > uint64(maxReferences) {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO4 maximum long-term index exceeds max_num_ref_frames %d", maxReferences)
				}
				limit := int(op.MaxLongTermFrameIdxPlus1) - 1
				if marked.IsLongTerm && marked.LongTermFrameIdx > limit {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO4 would unmark current long-term picture")
				}
				maxLongTerm = limit
				for i := len(refs) - 1; i >= 0; i-- {
					if refs[i] != nil && refs[i].IsRef && refs[i].IsLongTerm && refs[i].LongTermFrameIdx > limit {
						refs = removeReference(refs, i)
					}
				}
			case 5:
				refs, maxLongTerm = nil, -1
				// finalizePicturePOC separately normalizes the order counts.
				marked.FrameNum = 0
			case 6:
				if maxLongTerm < 0 || uint64(op.LongTermFrameIdx) > uint64(maxLongTerm) {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO6 long_term_frame_idx %d exceeds limit %d", op.LongTermFrameIdx, maxLongTerm)
				}
				index := int(op.LongTermFrameIdx)
				if old := findLongReference(refs, index); old >= 0 {
					refs = removeReference(refs, old)
				}
				// 8.2.5.4.6 checks capacity here, after replacing the old index.
				// A later removal cannot repair an over-capacity MMCO6.
				if referenceCount(refs) >= max(maxReferences, 1) {
					return nil, nil, maxLongTerm, fmt.Errorf("MMCO6 leaves no slot for current picture")
				}
				marked.IsLongTerm, marked.LongTermFrameIdx = true, index
			}
		}
	} else {
		var err error
		refs, err = slidingWindowReferences(refs, int(h.FrameNum), maxFrameNum, maxReferences)
		if err != nil {
			return nil, nil, maxLongTerm, err
		}
	}
	if referenceCount(refs) >= max(maxReferences, 1) {
		return nil, nil, maxLongTerm, fmt.Errorf("reference marking leaves no slot for current picture")
	}
	return append(refs, &marked), &marked, maxLongTerm, nil
}

// referenceCount includes both reference categories and inferred gap placeholders.
func referenceCount(refs []*frame.Frame) int {
	count := 0
	for _, ref := range refs {
		if ref != nil && ref.IsRef {
			count++
		}
	}
	return count
}

func findShortReference(refs []*frame.Frame, number int) int {
	for i, ref := range refs {
		if ref != nil && ref.IsRef && !ref.IsLongTerm && ref.FrameNum == number {
			return i
		}
	}
	return -1
}

func findLongReference(refs []*frame.Frame, index int) int {
	for i, ref := range refs {
		if ref != nil && ref.IsRef && ref.IsLongTerm && ref.LongTermFrameIdx == index {
			return i
		}
	}
	return -1
}

// refs must be an owned slice. Removing it from the store does not change the
// removed Frame's metadata, which may already be visible to a caller.
func removeReference(refs []*frame.Frame, index int) []*frame.Frame {
	copy(refs[index:], refs[index+1:])
	refs[len(refs)-1] = nil
	return refs[:len(refs)-1]
}

// validateSliceReferences checks this slice's reference requirements before
// reconstruction. Run it for every slice: a picture can start with an I slice
// and later contain P or B slices with different reference requirements.
// Marking commands are validated only for reference pictures.
func validateSliceReferences(s *sliceState, refs []*frame.Frame) error {
	if s.unit.RefIDC != 0 {
		if err := validateReferenceMarking(s.header, 1<<s.sps.Log2MaxFrameNum); err != nil {
			return err
		}
	}
	if s.header.SliceType == syntax.SliceTypeP && s.sps.MaxNumRefFrames == 0 {
		return fmt.Errorf("P slice requires max_num_ref_frames greater than zero")
	}
	if s.header.SliceType == syntax.SliceTypeB {
		for _, ref := range refs {
			if ref.IsLongTerm || ref.HasLongTermReferences {
				return fmt.Errorf("B pictures with long-term references require long-term direct-prediction support")
			}
			if ref.NonExisting {
				return fmt.Errorf("B pictures across frame_num gaps require non-existing-picture POC support")
			}
		}
	}
	return nil
}
