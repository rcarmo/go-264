package decode

import (
	"fmt"
	"math"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

// pocHistory lets the next picture continue numbering across counter wraps.
// It changes only when a picture successfully commits: deriving a pending copy
// keeps a failed picture from changing how the next picture is numbered.
// int64 arithmetic allows checking the standard's signed-32-bit limits before
// overflow, including on 386.
type pocHistory struct {
	// Types 1/2 unwrap frame_num using the previous picture in decoding order,
	// including non-reference pictures and inferred frame-number gaps.
	frameNum       uint32
	frameNumOffset int64
	// Type 0 unwraps POC-LSB against the previous reference picture instead.
	// Decoding a non-reference picture must leave this pair unchanged.
	refMSB, refLSB int64
}

// pictureOrder holds this picture's counts and the history to publish on success.
// Even a progressive frame has top/bottom field counts; its POC is their minimum.
type pictureOrder struct {
	top, bottom int64
	next        pocHistory
}

func checkPOCRange(name string, value int64) error {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return fmt.Errorf("%w: %s outside signed 32-bit range", nal.ErrInvalidSyntax, name)
	}
	return nil
}

// derivePictureOrder implements 8.2.1 for progressive frames. The parameter
// sets/header have already passed syntax validation. No decoder state changes.
func derivePictureOrder(previous pocHistory, sps *nal.SPS, h *syntax.Header, idr, reference bool) (pictureOrder, error) {
	cycle := pocCyclePrefix(sps)
	return derivePictureOrderWithCycle(previous, sps, h, idr, reference, &cycle)
}

// Prefix sums make each inferred gap picture O(1), rather than multiplying a
// maximum-size frame_num gap by the SPS's maximum 255-entry POC cycle.
func pocCyclePrefix(sps *nal.SPS) (cycle [256]int64) {
	for i := uint32(0); i < sps.NumRefFramesInPicOrderCntCycle; i++ {
		cycle[i+1] = cycle[i] + int64(sps.OffsetForRefFrame[i])
	}
	return cycle
}

// derivePictureOrderWithCycle derives a progressive picture's field order counts
// and candidate history without advancing the decoder's committed history.
// It reuses prefix sums for type 1 so a run of inferred gap pictures does not
// rescan the SPS offset cycle for every picture.
func derivePictureOrderWithCycle(previous pocHistory, sps *nal.SPS, h *syntax.Header, idr, reference bool, cycle *[256]int64) (pictureOrder, error) {
	if idr {
		previous = pocHistory{}
	}
	o := pictureOrder{next: previous}
	// Types 1/2 reconstruct the full frame number by counting wraps. Type 0
	// unwraps its independently signaled POC-LSB below instead.
	maxFrameNum := int64(1) << sps.Log2MaxFrameNum
	o.next.frameNum = h.FrameNum
	if sps.PicOrderCntType != 0 && !idr && previous.frameNum > h.FrameNum {
		o.next.frameNumOffset += maxFrameNum
	}
	if err := checkPOCRange("FrameNumOffset", o.next.frameNumOffset); err != nil {
		return pictureOrder{}, err
	}
	switch sps.PicOrderCntType {
	case 0:
		// Recover the missing high bits relative to the previous reference picture.
		// The half-range tie is deliberately asymmetric: a decrease wraps at
		// equality, while an increase must exceed half the range (8.2.1.1).
		maxLSB := int64(1) << sps.Log2MaxPocLsb
		lsb, msb := int64(h.PicOrderCntLsb), previous.refMSB
		if lsb < previous.refLSB && previous.refLSB-lsb >= maxLSB/2 {
			msb += maxLSB
		} else if lsb > previous.refLSB && lsb-previous.refLSB > maxLSB/2 {
			msb -= maxLSB
		}
		if err := checkPOCRange("PicOrderCntMsb", msb); err != nil {
			return pictureOrder{}, err
		}
		o.top = msb + lsb
		o.bottom = o.top + int64(h.DeltaPicOrderCntBottom)
		if reference {
			o.next.refMSB, o.next.refLSB = msb, lsb
		}
	case 1:
		// The SPS cycle describes repeating POC increments. With no cycle,
		// the expected count starts at zero; the remaining offsets still apply.
		count := int64(sps.NumRefFramesInPicOrderCntCycle)
		absFrameNum := int64(0)
		if count != 0 {
			absFrameNum = o.next.frameNumOffset + int64(h.FrameNum)
		}
		// Use the preceding reference-cycle position for a non-reference picture;
		// its own offset is added after evaluating that position.
		if !reference && absFrameNum > 0 {
			absFrameNum--
		}
		expected := int64(0)
		if absFrameNum > 0 {
			// Absolute frame number 1 uses cycle entry 0. Sum every completed
			// cycle and the current cycle through this entry; cycle[k] is the
			// sum of the first k offsets, so the inclusive prefix is position+1.
			cycleCount, position := (absFrameNum-1)/count, (absFrameNum-1)%count
			expected = cycleCount*cycle[count] + cycle[position+1]
		}
		if !reference {
			expected += int64(sps.OffsetForNonRefPic)
		}
		// Slice deltas adjust the cycle prediction; the bottom count also adds
		// the SPS's top-to-bottom offset.
		o.top = expected + int64(h.DeltaPicOrderCnt[0])
		o.bottom = o.top + int64(sps.OffsetForTopToBottomField) + int64(h.DeltaPicOrderCnt[1])
	case 2:
		// Reference pictures get even counts; non-reference pictures with the
		// same unwrapped frame number get the immediately preceding odd count.
		if !idr {
			o.top = 2 * (o.next.frameNumOffset + int64(h.FrameNum))
			if !reference {
				o.top--
			}
		}
		o.bottom = o.top
	}
	if err := checkPOCRange("TopFieldOrderCnt", o.top); err != nil {
		return pictureOrder{}, err
	}
	if err := checkPOCRange("BottomFieldOrderCnt", o.bottom); err != nil {
		return pictureOrder{}, err
	}
	// The frame's minimum must be zero at IDR, not necessarily both field counts.
	if idr && min(o.top, o.bottom) != 0 {
		return pictureOrder{}, fmt.Errorf("%w: IDR picture order count must be zero", nal.ErrInvalidSyntax)
	}
	return o, nil
}

func (d *Decoder) preparePicturePOC(s *sliceState, refs []*frame.Frame) (pictureOrder, error) {
	history := d.pocHistory
	cycle := pocCyclePrefix(s.sps)
	if s.unit.Type != nal.TypeSliceIDR && s.sps.PicOrderCntType != 0 && d.prevRefFrameNumValid {
		// 8.2.5.2 derives POC for every inferred picture for types 1/2, even
		// those evicted before the current picture is admitted. Type 0 gaps
		// have no POC and do not change the previous-reference POC state.
		maxFrameNum := 1 << s.sps.Log2MaxFrameNum
		for number := (d.prevRefFrameNum + 1) % maxFrameNum; number != int(s.header.FrameNum); number = (number + 1) % maxFrameNum {
			gap, err := derivePictureOrderWithCycle(history, s.sps, &syntax.Header{FrameNum: uint32(number)}, false, true, &cycle)
			if err != nil {
				return pictureOrder{}, err
			}
			history = gap.next
			for _, ref := range refs {
				// These are newly staged placeholders, never committed frames.
				// Long-term pictures may share a number with a new gap.
				if ref.NonExisting && ref.FrameNum == number {
					ref.POC = int(min(gap.top, gap.bottom))
					ref.FullPOC = ref.POC
				}
			}
		}
	}
	return derivePictureOrderWithCycle(history, s.sps, s.header, s.unit.Type == nal.TypeSliceIDR, s.unit.RefIDC != 0, &cycle)
}

func (d *Decoder) bindPicturePOC(p *pictureState, order pictureOrder) {
	p.order = order
	p.frame.POC = int(min(order.top, order.bottom))
	p.frame.FullPOC = p.frame.POC
	d.maxPOCLSB = 0
	if p.sps.PicOrderCntType == 0 {
		d.maxPOCLSB = 1 << p.sps.Log2MaxPocLsb
	}
	d.currentFullPOC = p.frame.FullPOC
}

// commitPicturePOC publishes this completed picture as the predecessor for
// subsequent POC derivation. Call after finalization and reference marking succeed.
func (d *Decoder) commitPicturePOC(p *pictureState) {
	d.pocHistory = p.order.next
	d.currentFullPOC = p.frame.FullPOC
}
