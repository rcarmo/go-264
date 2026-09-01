package decode

import (
	"fmt"
	"os"
	"reflect"

	"github.com/rcarmo/go-264/filter"
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

// A picture can span several slices. addSlice reconstructs each slice into the
// shared picture; decodeSliceData calls saveSlice before predictors are reset.
// At a picture boundary, the caller runs finishPicture and publishes the result,
// or abandons the pending picture if assembly fails.

// moreSliceData reports whether a CAVLC reader has macroblock syntax left,
// rather than only rbsp_trailing_bits. The slice loop calls it after any pending
// skip run is exhausted: that run can describe further macroblocks without
// consuming another syntax element.
//
// A valid trailer is one stop bit followed by zeros to byte alignment, occupying
// at most eight bits. This function only peeks; ReadRBSPTrailingBits consumes and
// validates the trailer after macroblock decoding.
func moreSliceData(r *nal.Reader) bool {
	n := r.BitsLeft()
	return n > 8 || n > 0 && r.PeekBits(n) != uint32(1)<<(n-1)
}

// pocState records the decoder's picture-order state before assembly starts.
type pocState struct {
	msb, lsb, max, current int
	valid                  bool
}

// pocState takes a value snapshot before a new picture binds its order counts.
// newPicture retains it so abortPicture can restore the temporary decoder fields
// if a slice or picture-finalization step fails.
func (d *Decoder) pocState() pocState {
	return pocState{d.prevPOCMSB, d.prevPOCLSB, d.maxPOCLSB, d.currentFullPOC, d.prevPOCValid}
}

// abortPicture abandons the pending reconstruction after a decoding failure.
// It restores the order-count fields changed while decoding this picture and
// releases the current picture, slice and active List 0 bindings. Earlier
// completed pictures remain intact.
//
// Callers use this after addSlice or finishPicture fails, because those methods
// may already have written part of the pending picture before returning an error.
func (d *Decoder) abortPicture() {
	if d.picture != nil {
		s := d.picture.pocBefore
		d.prevPOCMSB, d.prevPOCLSB, d.maxPOCLSB, d.currentFullPOC, d.prevPOCValid = s.msb, s.lsb, s.max, s.current, s.valid
	}
	d.picture, d.slice, d.activeL0Refs = nil, nil, nil
}

// addSlice validates and decodes a parsed slice into the pending picture,
// initializing that picture on the first slice. The caller detects picture
// boundaries and finishes the previous picture before starting a different one.
// Later slices must agree with the pending picture's identity, parameter sets
// and reference-marking commands.
//
// Reconstructed samples and macroblock metadata survive slice boundaries, while
// entropy, QP and spatial motion predictors start afresh. This method assigns
// the slice's ownership ID and runs decodeSliceData, which also saves its motion
// and filtering metadata. A successful call can leave coverage incomplete;
// finishPicture checks coverage once all slices have arrived. On error, the
// caller must abort the pending picture rather than continue partial decoding.
func (d *Decoder) addSlice(s *sliceState) error {
	if s.header.RedundantPicCnt != 0 {
		return fmt.Errorf("unsupported redundant coded picture")
	}
	if d.picture == nil {
		refs, next, valid, err := d.preparePictureReferences(s)
		if err != nil {
			return err
		}
		d.picture = d.newPicture(s)
		d.picture.referenceFrames, d.picture.nextPrevRefFrameNum, d.picture.nextPrevRefValid = refs, next, valid
	}
	p := d.picture
	if err := validateSliceReferences(s, p.referenceFrames); err != nil {
		return err
	}
	if p.identity != identifyPicture(s) {
		return fmt.Errorf("slice belongs to another picture")
	}
	// Matching parameter-set IDs is not enough: the exported SPS/PPS objects
	// may have been updated since the first slice took its snapshots.
	if *p.sps != *s.sps || *p.pps != *s.pps {
		return fmt.Errorf("parameter sets changed within picture")
	}
	if len(p.slices) > 0 {
		// Reference marking is a picture-level operation. Every slice must
		// agree on the commands that will be applied when the picture commits.
		first := p.slices[0].header
		if first.AdaptiveRefPicMarking != s.header.AdaptiveRefPicMarking || !reflect.DeepEqual(first.MemoryManagementControls, s.header.MemoryManagementControls) {
			return fmt.Errorf("reference marking differs between slices of one picture")
		}
		// Spatial motion prediction cannot cross a slice boundary. saveSlice
		// has retained the previous slice's motion, so reset its scratch cells
		// to the unavailable-reference sentinel (-2). Earlier slices' cells
		// were already cleared, making total clearing work O(picture MBs),
		// even for one-macroblock slices.
		for mb := p.lastStart; mb < p.lastEnd; mb++ {
			x, y := mb%d.mbW, mb/d.mbW
			for by := 0; by < 4; by++ {
				for bx := 0; bx < 4; bx++ {
					idx := (y*4+by)*p.motion.stride4 + x*4 + bx
					for list := 0; list < 2; list++ {
						p.motion.ref[list][idx] = -2
						p.motion.mv[list][idx], p.motion.mvd[list][idx] = syntax.MotionVector{}, syntax.MotionVector{}
					}
					p.motion.direct[idx] = false
				}
			}
		}
	}
	// Macroblocks store this ID so final filtering can recover their own
	// slice's deblocking controls and distinguish cross-slice edges.
	s.id = len(p.slices)
	p.slices = append(p.slices, s)
	d.slice = s
	return d.decodeSliceData(s)
}

// saveSlice copies the just-decoded macroblocks' motion vectors, reference
// identities, types and filter inputs into picture-owned storage for [start, end).
// decodeSliceData calls it after accepting the slice's termination, while s and
// its reference lists are still active: ref_idx values only have meaning within
// those slice-local lists.
//
// Temporal List 0 indices map into a picture-wide reference list; spatial direct
// retains the original slice-local indices. Deblocking records resolved frame
// identities. The saved metadata survives predictor resets for final filtering
// and later temporal prediction. Recording [start, end) also tells addSlice exactly
// which scratch cells to clear before decoding the next slice.
func (d *Decoder) saveSlice(s *sliceState, start, end int) {
	p, f, c := d.picture, d.picture.frame, d.picture.motion
	hdr, pps := s.header, s.pps
	isB := hdr.SliceType == syntax.SliceTypeB
	var l0 []*frame.Frame
	if isB {
		l0 = d.bidiL0FramesWithMods(f.POC, hdr.FrameNum, hdr.RefModifications[0])
	} else if hdr.SliceType == syntax.SliceTypeP {
		l0 = d.activeL0Refs
	}
	// ref_idx 0 can name different pictures in different slices. Temporal direct
	// needs a picture-wide List 0 mapping; spatial direct must still see the raw
	// index because colZeroFlag specifically tests the slice-local ref_idx == 0.
	remap := make([]int8, len(l0))
	for i, ref := range l0 {
		remap[i] = -1
		if ref == nil {
			continue
		}
		index := -1
		for j := range f.RefListL0Num {
			if f.RefListL0Num[j] == ref.FrameNum && f.RefListL0POC[j] == frameOrderPOC(ref) {
				index = j
				break
			}
		}
		if index < 0 {
			index = len(f.RefListL0Num)
			f.RefListL0Num = append(f.RefListL0Num, ref.FrameNum)
			f.RefListL0POC = append(f.RefListL0POC, frameOrderPOC(ref))
		}
		remap[i] = int8(index)
	}
	// identity resolves a slice-local reference index to a picture-local frame
	// ID for deblocking. Equal indices or POC values must not merge distinct
	// referenced frames; unused or unresolved references receive the sentinel -1.
	identity := func(list int, ref int8) int {
		if ref < 0 {
			return -1
		}
		var target *frame.Frame
		if !isB {
			target = d.refL0(ref)
		} else if list == 0 {
			target = d.refBidiL0(ref, f.POC)
		} else {
			target = d.refBidiL1(ref, f.POC)
		}
		if target == nil {
			return -1
		}
		id, ok := p.referenceIDs[target]
		if !ok {
			id = len(p.referenceIDs)
			p.referenceIDs[target] = id
		}
		return id
	}
	// Capture filter inputs while this slice's reference lists are active.
	for mb := start; mb < end; mb++ {
		info := filter.MBDeblockInfo{QP: p.mbQPCtx[mb],
			ChromaQPU: frame.ChromaQP(p.mbQPCtx[mb], int(pps.ChromaQPIndexOffset)),
			ChromaQPV: frame.ChromaQP(p.mbQPCtx[mb], int(pps.SecondChromaQPIndexOffset)),
			IsIntra:   p.mbIsIntra[mb], Use8x8: p.transform8x8Ctx[mb], IsB: isB}
		// Coefficient contexts use H.264 scan order; deblocking visits physical
		// rows and columns of 4x4 blocks, so store the counts in raster order.
		for scan, nz := range p.nzCtx[mb] {
			info.NZC[syntax.Blk4x4Row[scan]*4+syntax.Blk4x4Col[scan]] = nz
		}
		x, y := mb%d.mbW, mb/d.mbW
		for by := 0; by < 4; by++ {
			for bx := 0; bx < 4; bx++ {
				idx := (y*4+by)*c.stride4 + x*4 + bx
				block := by*4 + bx
				ref0, ref1 := c.ref[0][idx], c.ref[1][idx]
				info.RefIDL0[block], info.RefIDL1[block] = identity(0, ref0), identity(1, ref1)
				mv0, mv1 := c.mv[0][idx], c.mv[1][idx]
				info.MVL0[block], info.MVL1[block] = [2]int16{mv0.X, mv0.Y}, [2]int16{mv1.X, mv1.Y}
				// Keep permanent motion metadata separate from the scratch cache
				// that addSlice clears before decoding the next slice.
				f.MotionL0[idx], f.MotionL1[idx] = info.MVL0[block], info.MVL1[block]
				f.RefIdxL0[idx], f.RefIdxL1[idx] = ref0, ref1
				f.TemporalRefIdxL0[idx] = ref0
				if ref0 >= 0 && int(ref0) < len(remap) {
					f.TemporalRefIdxL0[idx] = remap[ref0]
				}
			}
		}
		p.deblock[mb] = info
		f.MBType[mb] = p.mbFFTypeCtx[mb]
	}
	p.lastStart, p.lastEnd = start, end
}

// finishPicture validates coverage and finishes the pending reconstructed frame
// for publication. It requires every macroblock to have been decoded exactly
// once, then applies in-loop deblocking in place using each macroblock's own
// slice controls. It returns the internal frame with its full coded
// dimensions; the caller creates the cropped output view and commits
// reference state when publishing it.
//
// Call this once after the last slice: filtering changes the samples, so a second
// call would filter them again. The caller publishes and clears pending state
// after success, or abandons the pending picture on failure.
func (d *Decoder) finishPicture() (*frame.Frame, error) {
	p := d.picture
	// decodeSliceData rejects overlapping ownership, so this count proves that
	// every macroblock was decoded exactly once, with no gaps between slices.
	if p.decoded != len(p.mbSliceID) {
		return nil, fmt.Errorf("incomplete picture: decoded %d of %d macroblocks", p.decoded, len(p.mbSliceID))
	}
	f := p.frame
	// Intra prediction reads pre-filter samples. Finish reconstructing all
	// slices before filtering, which may change samples across slice boundaries.
	if os.Getenv("GO264_DISABLE_DEBLOCK") == "" {
		for mb, id := range p.mbSliceID {
			// The current macroblock's slice supplies the offsets and controls;
			// different slices of the same picture can use different settings.
			hdr := p.slices[id].header
			ctx := filter.DeblockMBContext{DisableIDC: int(hdr.DisableDeblocking), AlphaOffset: int(hdr.SliceAlphaC0Offset), BetaOffset: int(hdr.SliceBetaOffset)}
			x, y := mb%d.mbW, mb/d.mbW
			var left, top *filter.MBDeblockInfo
			// IDC 2 suppresses cross-slice edges; IDC 0 permits them.
			// IDC 1 disables filtering inside DeblockMBFrame itself.
			if x > 0 && (hdr.DisableDeblocking != 2 || p.mbSliceID[mb-1] == id) {
				left = &p.deblock[mb-1]
			}
			if y > 0 && (hdr.DisableDeblocking != 2 || p.mbSliceID[mb-d.mbW] == id) {
				top = &p.deblock[mb-d.mbW]
			}
			filter.DeblockMBFrame(f.Y, f.StrideY, f.U, f.V, f.StrideC, x, y, p.deblock[mb], left, top, ctx)
		}
	}
	traceSavedMotion(f, d.mbW)
	return f, nil
}
