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
	maxFrameNum := 1 << p.sps.Log2MaxFrameNum
	if p.frame.IsRef {
		if h.AdaptiveRefPicMarking {
			// Admission rejects long-term and reset operations; MMCO1 is the
			// short-term removal needed here. Validate before publishing state.
			for _, op := range h.MemoryManagementControls {
				target := (int(h.FrameNum) - int(op.DifferenceOfPicNumsMinus1) - 1 + maxFrameNum) % maxFrameNum
				found := -1
				for i, ref := range refs {
					if ref.IsRef && ref.FrameNum == target {
						found = i
						break
					}
				}
				if found < 0 {
					return fmt.Errorf("MMCO1 refers to missing frame_num %d", target)
				}
				refs = append(refs[:found], refs[found+1:]...)
			}
			count := 0
			for _, ref := range refs {
				if ref.IsRef {
					count++
				}
			}
			if count >= max(int(p.sps.MaxNumRefFrames), 1) {
				return fmt.Errorf("adaptive reference marking leaves no slot for current picture")
			}
		} else {
			refs = slidingWindowReferences(refs, int(h.FrameNum), maxFrameNum, int(p.sps.MaxNumRefFrames))
		}
		refs = append(refs, p.frame)
		p.nextPrevRefFrameNum, p.nextPrevRefValid = int(h.FrameNum), true
	}
	// Non-reference pictures remain output-only. Non-existing pictures remain
	// reference metadata only and are never added to decoded output/history.
	d.referenceSPS = p.sps
	d.DPB.Frames = refs
	d.DPB.MaxSize = max(int(p.sps.MaxNumRefFrames), 1)
	d.prevRefFrameNum, d.prevRefFrameNumValid = p.nextPrevRefFrameNum, p.nextPrevRefValid
	return nil
}

// validateSliceReferences checks this slice's reference requirements before
// reconstruction. Run it for every slice: a picture can start with an I slice
// and later contain P or B slices with different reference requirements.
// Marking commands are validated only for reference pictures.
func validateSliceReferences(s *sliceState, refs []*frame.Frame) error {
	if s.unit.RefIDC != 0 {
		if err := validateShortTermReferenceMarking(s.header, 1<<s.sps.Log2MaxFrameNum); err != nil {
			return err
		}
	}
	if s.header.SliceType == syntax.SliceTypeP && s.sps.MaxNumRefFrames == 0 {
		return fmt.Errorf("P slice requires max_num_ref_frames greater than zero")
	}
	if s.header.SliceType == syntax.SliceTypeB {
		for _, ref := range refs {
			if ref.NonExisting {
				return fmt.Errorf("B pictures across frame_num gaps require non-existing-picture POC support")
			}
		}
	}
	return nil
}
