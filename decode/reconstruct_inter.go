package decode

// decode/reconstruct_inter.go — inter macroblock reconstruction.
// Covers P-skip, P16x16/P16x8/P8x16/P8x8 motion compensation, B-frame blending,
// and chroma inter prediction.

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
	"github.com/rcarmo/go-264/transform"
)

func frameOrderPOC(fr *frame.Frame) int {
	if fr == nil {
		return 0
	}
	if fr.FullPOC != 0 || fr.POC == 0 {
		return fr.FullPOC
	}
	return fr.POC
}

func (d *Decoder) currentBidiOrderPOC(currentPOC int) int {
	if d != nil && d.maxPOCLSB > 0 && d.currentFullPOC%d.maxPOCLSB == currentPOC%d.maxPOCLSB {
		return d.currentFullPOC
	}
	return currentPOC
}

func dpbHasReferenceFrames(frames []*frame.Frame) bool {
	for _, fr := range frames {
		if fr != nil && fr.IsRef {
			return true
		}
	}
	return false
}

func (d *Decoder) refL0(refIdx int8) *frame.Frame {
	if d == nil {
		return nil
	}
	idx := int(refIdx)
	if d.activeL0Refs != nil {
		var err error
		if idx < 0 || idx >= len(d.activeL0Refs) {
			err = fmt.Errorf("P reference index %d outside active list of %d", idx, len(d.activeL0Refs))
		} else if ref := d.activeL0Refs[idx]; ref == nil || !ref.IsRef {
			err = fmt.Errorf("P reference index %d has no reference picture", idx)
		} else if ref.NonExisting {
			err = fmt.Errorf("P reference index %d selects non-existing frame_num %d", idx, ref.FrameNum)
		} else {
			if ref.IsLongTerm && d.picture != nil {
				d.picture.frame.HasLongTermReferences = true
			}
			return ref
		}
		if d.slice != nil && d.slice.referenceErr == nil {
			d.slice.referenceErr = err
		}
		return nil
	}
	if d.DPB == nil || len(d.DPB.Frames) == 0 {
		return nil
	}
	if idx < 0 {
		idx = 0
	}
	// Legacy B/synthetic helper fallback. P slices install an explicitly built
	// active list above; they must not silently substitute references here.
	var refs []*frame.Frame
	filterRef := dpbHasReferenceFrames(d.DPB.Frames)
	for _, fr := range d.DPB.Frames {
		if fr != nil && (!filterRef || fr.IsRef) {
			refs = append(refs, fr)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	// Sort by descending FrameNum, then descending POC as tiebreaker.
	// Only sort when FrameNums are meaningful (not all zero with distinct POCs).
	hasDistinctFN := false
	for i := 1; i < len(refs); i++ {
		if refs[i].FrameNum != refs[0].FrameNum {
			hasDistinctFN = true
			break
		}
	}
	if hasDistinctFN {
		for i := 0; i < len(refs)-1; i++ {
			for j := i + 1; j < len(refs); j++ {
				if refs[j].FrameNum > refs[i].FrameNum || (refs[j].FrameNum == refs[i].FrameNum && refs[j].POC > refs[i].POC) {
					refs[i], refs[j] = refs[j], refs[i]
				}
			}
		}
	} else {
		// All same FrameNum (or synthetic): keep insertion order reversed (most recent first).
		for i, j := 0, len(refs)-1; i < j; i, j = i+1, j-1 {
			refs[i], refs[j] = refs[j], refs[i]
		}
	}
	if idx < len(refs) {
		return refs[idx]
	}
	return refs[len(refs)-1]
}

func (d *Decoder) refL1(refIdx int8) *frame.Frame {
	if d == nil || d.DPB == nil || len(d.DPB.Frames) == 0 {
		return nil
	}
	idx := int(refIdx)
	if idx < 0 {
		idx = 0
	}
	filterRef := dpbHasReferenceFrames(d.DPB.Frames)
	seen := 0
	for i := len(d.DPB.Frames) - 1; i >= 0; i-- {
		fr := d.DPB.Frames[i]
		if fr == nil || (filterRef && !fr.IsRef) {
			continue
		}
		if seen == idx+1 {
			return fr
		}
		seen++
	}
	return d.refL0(refIdx)
}

// refBidiL0 returns the refIdx-th L0 (past) reference for B-slice prediction.
// Reference-list ordering uses unwrapped POC so pictures after an LSB wrap are
// not mistaken for old past references.
func (d *Decoder) refBidiL0(refIdx int8, currentPOC int) *frame.Frame {
	if d == nil || d.DPB == nil || len(d.DPB.Frames) == 0 {
		return nil
	}
	currentOrderPOC := d.currentBidiOrderPOC(currentPOC)
	var pastFrames []*frame.Frame
	for _, fr := range d.DPB.Frames {
		if fr != nil && fr.IsRef && frameOrderPOC(fr) < currentOrderPOC {
			pastFrames = append(pastFrames, fr)
		}
	}
	// Sort by descending unwrapped POC (most recent past first).
	for i := 0; i < len(pastFrames)-1; i++ {
		for j := i + 1; j < len(pastFrames); j++ {
			if frameOrderPOC(pastFrames[j]) > frameOrderPOC(pastFrames[i]) {
				pastFrames[i], pastFrames[j] = pastFrames[j], pastFrames[i]
			}
		}
	}
	idx := int(refIdx)
	if idx < 0 {
		idx = 0
	}
	if len(pastFrames) > 0 && idx < len(pastFrames) {
		return pastFrames[idx]
	}
	// Fallback: simple index from end of DPB (handles equal-POC test cases).
	pos := len(d.DPB.Frames) - 1 - idx
	if pos < 0 {
		pos = 0
	}
	if pos >= len(d.DPB.Frames) {
		pos = len(d.DPB.Frames) - 1
	}
	return d.DPB.Frames[pos]
}

// refBidiL1 returns the refIdx-th L1 (future) reference for B-slice prediction.
func (d *Decoder) refBidiL1(refIdx int8, currentPOC int) *frame.Frame {
	return d.refBidiL1Ordered(refIdx, currentPOC, d.currentBidiOrderPOC(currentPOC), true)
}

func (d *Decoder) refBidiL1DirectColocated(refIdx int8, currentPOC int) *frame.Frame {
	return d.refBidiL1(refIdx, currentPOC)
}

func (d *Decoder) refBidiL1Ordered(refIdx int8, currentPOC, currentOrderPOC int, useFull bool) *frame.Frame {
	if d == nil || d.DPB == nil || len(d.DPB.Frames) == 0 {
		return nil
	}
	type orderedRef struct {
		fr  *frame.Frame
		poc int
	}
	// Build ordered L1 list per H.264 §8.2.4.2.3. Around POC wrap, compact low
	// POC values after a high current POC are future pictures in the next cycle;
	// rank by effective unwrapped POC and prefer the newest frame_num for duplicate
	// compact POCs so colocated Direct uses the current GOP's future reference.
	// Normal H.264 DPBs fit in bounded stack scratch; unusually large direct
	// library callers retain exact behaviour using proportional temporary slices.
	var futureScratch, pastScratch [32]orderedRef
	futureRefs, pastRefs := futureScratch[:0], pastScratch[:0]
	if len(d.DPB.Frames) > len(futureScratch) {
		futureRefs = make([]orderedRef, 0, len(d.DPB.Frames))
		pastRefs = make([]orderedRef, 0, len(d.DPB.Frames))
	}
	maxPOC := d.maxPOCLSB
	wrapCurrent := maxPOC > 0 && currentPOC > (3*maxPOC)/4
	for _, fr := range d.DPB.Frames {
		if fr == nil || !fr.IsRef {
			continue
		}
		effPOC := fr.POC
		if useFull {
			effPOC = frameOrderPOC(fr)
		}
		if !useFull && effPOC == fr.POC && wrapCurrent && fr.POC < maxPOC/4 {
			effPOC += maxPOC
		}
		if effPOC > currentOrderPOC {
			futureRefs = append(futureRefs, orderedRef{fr: fr, poc: effPOC})
		} else {
			pastRefs = append(pastRefs, orderedRef{fr: fr, poc: effPOC})
		}
	}
	for i := 0; i < len(futureRefs)-1; i++ {
		for j := i + 1; j < len(futureRefs); j++ {
			if futureRefs[j].poc < futureRefs[i].poc || (futureRefs[j].poc == futureRefs[i].poc && futureRefs[j].fr.FrameNum > futureRefs[i].fr.FrameNum) {
				futureRefs[i], futureRefs[j] = futureRefs[j], futureRefs[i]
			}
		}
	}
	for i := 0; i < len(pastRefs)-1; i++ {
		for j := i + 1; j < len(pastRefs); j++ {
			if pastRefs[j].poc > pastRefs[i].poc || (pastRefs[j].poc == pastRefs[i].poc && pastRefs[j].fr.FrameNum > pastRefs[i].fr.FrameNum) {
				pastRefs[i], pastRefs[j] = pastRefs[j], pastRefs[i]
			}
		}
	}
	count := len(futureRefs) + len(pastRefs)
	if d.traceRefList {
		fmt.Fprintf(os.Stderr, "GOBL1LIST curpoc=%d curorder=%d maxpoc=%d wrap=%t", currentPOC, currentOrderPOC, maxPOC, wrapCurrent)
		for i := 0; i < count && i < 12; i++ {
			var r orderedRef
			if i < len(futureRefs) {
				r = futureRefs[i]
			} else {
				r = pastRefs[i-len(futureRefs)]
			}
			fmt.Fprintf(os.Stderr, " idx%d=poc%d/eff%d/fn%d", i, r.fr.POC, r.poc, r.fr.FrameNum)
		}
		fmt.Fprintln(os.Stderr)
	}
	idx := int(refIdx)
	if idx < 0 {
		idx = 0
	}
	if count > 0 {
		if idx >= count {
			idx = count - 1
		}
		if idx < len(futureRefs) {
			return futureRefs[idx].fr
		}
		return pastRefs[idx-len(futureRefs)].fr
	}
	// Preserve synthetic-frame tests and callers that predate IsRef tracking.
	if !dpbHasReferenceFrames(d.DPB.Frames) {
		return d.refL1(refIdx)
	}
	return nil
}

func clipWeightedSample(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func clip3(lo, hi, v int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// implicitBipredWeights derives (w0, w1) for implicit weighted bi-prediction
// (weighted_bipred_idc == 2) per H.264 §8.4.2.3.2. Returns w0=w1=32 (plain
// average) when implicit weighting is not applicable. logWD is fixed at 5.
func implicitBipredWeights(pocCur, pocL0, pocL1 int) (int, int) {
	td := clip3(-128, 127, pocL1-pocL0)
	tb := clip3(-128, 127, pocCur-pocL0)
	if td == 0 {
		return 32, 32
	}
	txAbs := td >> 1
	if txAbs < 0 {
		txAbs = -txAbs
	}
	tx := (16384 + txAbs) / td
	distScaleFactor := clip3(-1024, 1023, (tb*tx+32)>>6)
	w1 := distScaleFactor >> 2
	if w1 < -64 || w1 > 128 {
		return 32, 32
	}
	return 64 - w1, w1
}

// biWeightsForRefs returns implicit weighted-bipred (w0,w1) for the given L0/L1
// reference indices, or (32,32) (plain average) when implicit weighting is not
// active. Predictor selection and weight derivation must use the same POC-ordered
// B lists. logWD is fixed at 5.
func (d *Decoder) biWeightsForRefs(refIdxL0, refIdxL1 int8, currentPOC int) (int, int) {
	if d == nil || d.weightedBipredIDC != 2 {
		return 32, 32
	}
	r0 := d.refBidiL0(refIdxL0, currentPOC)
	r1 := d.refBidiL1(refIdxL1, currentPOC)
	if r0 == nil || r1 == nil {
		return 32, 32
	}
	return implicitBipredWeights(d.currentFullPOC, r0.FullPOC, r1.FullPOC)
}

type biBlendParams struct {
	w0, w1       int
	round, shift int
	offset       int
	plainAverage bool
}

func clampedRefIndex(refIdx int8, n int) int {
	idx := int(refIdx)
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return idx
}

func (d *Decoder) explicitBiLumaParams(refIdxL0, refIdxL1 int8) biBlendParams {
	i0, i1 := clampedRefIndex(refIdxL0, len(d.lumaWeightL0)), clampedRefIndex(refIdxL1, len(d.lumaWeightL1))
	denom := int(d.lumaWeightDenom)
	return biBlendParams{
		w0: int(d.lumaWeightL0[i0]), w1: int(d.lumaWeightL1[i1]),
		round: 1 << denom, shift: denom + 1,
		offset: (int(d.lumaOffsetL0[i0]) + int(d.lumaOffsetL1[i1]) + 1) >> 1,
	}
}

func (d *Decoder) biChromaParams(comp int, refIdxL0, refIdxL1 int8, currentPOC int) biBlendParams {
	if d != nil && d.weightedBipredIDC == 1 && comp >= 0 && comp < 2 {
		i0, i1 := clampedRefIndex(refIdxL0, len(d.chromaWeightL0)), clampedRefIndex(refIdxL1, len(d.chromaWeightL1))
		denom := int(d.chromaWeightDenom)
		return biBlendParams{
			w0: int(d.chromaWeightL0[i0][comp]), w1: int(d.chromaWeightL1[i1][comp]),
			round: 1 << denom, shift: denom + 1,
			offset: (int(d.chromaOffsetL0[i0][comp]) + int(d.chromaOffsetL1[i1][comp]) + 1) >> 1,
		}
	}
	if d != nil && d.weightedBipredIDC == 1 {
		return d.explicitBiLumaParams(refIdxL0, refIdxL1)
	}
	w0, w1 := d.biWeightsForRefs(refIdxL0, refIdxL1, currentPOC)
	return biBlendParams{w0: w0, w1: w1, round: 32, shift: 6, plainAverage: w0 == 32 && w1 == 32}
}

func (d *Decoder) applyExplicitUniLuma(dst []byte, list int, refIdx int8, dstX, dstY, w, h int) {
	if d == nil || d.weightedBipredIDC != 1 || len(dst) < 256 || !valid16x16Rect(dstX, dstY, w, h) {
		return
	}
	idx := clampedRefIndex(refIdx, len(d.lumaWeightL0))
	weight, offset := d.lumaWeightL0[idx], d.lumaOffsetL0[idx]
	if list == 1 {
		idx = clampedRefIndex(refIdx, len(d.lumaWeightL1))
		weight, offset = d.lumaWeightL1[idx], d.lumaOffsetL1[idx]
	}
	denom := int(d.lumaWeightDenom)
	round := 0
	if denom > 0 {
		round = 1 << (denom - 1)
	}
	for y := 0; y < h; y++ {
		row := (dstY+y)*16 + dstX
		for x := 0; x < w; x++ {
			v := int(dst[row+x]) * int(weight)
			if denom > 0 {
				v = (v + round) >> denom
			}
			dst[row+x] = clipWeightedSample(v + int(offset))
		}
	}
}

func (d *Decoder) applyExplicitUniChroma(dst []byte, comp, list int, refIdx int8, dstX, dstY, w, h int) {
	if d == nil || d.weightedBipredIDC != 1 || comp < 0 || comp > 1 || len(dst) < 64 || dstX < 0 || dstY < 0 || w <= 0 || h <= 0 || dstX+w > 8 || dstY+h > 8 {
		return
	}
	idx := clampedRefIndex(refIdx, len(d.chromaWeightL0))
	weight, offset := d.chromaWeightL0[idx][comp], d.chromaOffsetL0[idx][comp]
	if list == 1 {
		idx = clampedRefIndex(refIdx, len(d.chromaWeightL1))
		weight, offset = d.chromaWeightL1[idx][comp], d.chromaOffsetL1[idx][comp]
	}
	denom := int(d.chromaWeightDenom)
	round := 0
	if denom > 0 {
		round = 1 << (denom - 1)
	}
	for y := 0; y < h; y++ {
		row := (dstY+y)*8 + dstX
		for x := 0; x < w; x++ {
			v := int(dst[row+x]) * int(weight)
			if denom > 0 {
				v = (v + round) >> denom
			}
			dst[row+x] = clipWeightedSample(v + int(offset))
		}
	}
}

// biBlendRect blends L0/L1 predictions into dst for a w×h rectangle at
// (dstX,dstY) within the 16-wide MB buffer, applying implicit weighted
// bi-prediction when the active PPS selects weighted_bipred_idc == 2.
func (d *Decoder) biBlendRect(dst, predL0, predL1 []uint8, refL0, refL1 *frame.Frame, refIdxL0, refIdxL1 int8, dstX, dstY, w, h int) {
	p := biBlendParams{w0: 32, w1: 32, round: 32, shift: 6, plainAverage: true}
	if d != nil && d.weightedBipredIDC == 1 {
		p = d.explicitBiLumaParams(refIdxL0, refIdxL1)
	} else if d != nil && d.weightedBipredIDC == 2 && refL0 != nil && refL1 != nil {
		p.w0, p.w1 = implicitBipredWeights(d.currentFullPOC, refL0.FullPOC, refL1.FullPOC)
		p.plainAverage = p.w0 == 32 && p.w1 == 32
	}
	if !valid16x16Rect(dstX, dstY, w, h) || len(dst) < 256 || len(predL0) < 256 || len(predL1) < 256 {
		return
	}
	off := dstY*16 + dstX
	if d != nil && d.weightedBipredIDC == 1 {
		biBlendRectParams(dst[off:], predL0[off:], predL1[off:], 16, w, h, p.w0, p.w1, p.round, p.shift, p.offset)
		return
	}
	biBlendRectPixels(dst[off:], predL0[off:], predL1[off:], 16, w, h, p.w0, p.w1)
}

func (d *Decoder) applyWeightedPredL0Rect(predicted []uint8, refIdx int8, dstX, dstY, w, h int) {
	if d == nil || !d.weightedPred || len(predicted) < 256 || w <= 0 || h <= 0 {
		return
	}
	idx := int(refIdx)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(d.lumaWeightL0) {
		idx = len(d.lumaWeightL0) - 1
	}
	denom := d.lumaWeightDenom
	weight := int(d.lumaWeightL0[idx])
	offset := int(d.lumaOffsetL0[idx])
	if weight == 1<<denom && offset == 0 {
		return
	}
	round := 0
	if denom > 0 {
		round = 1 << (denom - 1)
	}
	for y := 0; y < h; y++ {
		row := (dstY+y)*16 + dstX
		for x := 0; x < w; x++ {
			v := int(predicted[row+x]) * weight
			if denom > 0 {
				v = (v + round) >> denom
			}
			predicted[row+x] = clipWeightedSample(v + offset)
		}
	}
}

func (d *Decoder) applyWeightedChromaL0Rect(predicted []uint8, comp int, refIdx int8, dstX, dstY, w, h int) {
	if d == nil || !d.weightedPred || len(predicted) < 64 || comp < 0 || comp > 1 || w <= 0 || h <= 0 {
		return
	}
	idx := int(refIdx)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(d.chromaWeightL0) {
		idx = len(d.chromaWeightL0) - 1
	}
	denom := d.chromaWeightDenom
	weight := int(d.chromaWeightL0[idx][comp])
	offset := int(d.chromaOffsetL0[idx][comp])
	if weight == 1<<denom && offset == 0 {
		return
	}
	round := 0
	if denom > 0 {
		round = 1 << (denom - 1)
	}
	for y := 0; y < h; y++ {
		row := (dstY+y)*8 + dstX
		for x := 0; x < w; x++ {
			v := int(predicted[row+x]) * weight
			if denom > 0 {
				v = (v + round) >> denom
			}
			predicted[row+x] = clipWeightedSample(v + offset)
		}
	}
}

func frameLumaHeight(f *frame.Frame) int {
	if f == nil || f.StrideY <= 0 {
		return 0
	}
	return len(f.Y) / f.StrideY
}

func frameChromaHeight(f *frame.Frame) int {
	if f == nil || f.StrideC <= 0 {
		return 0
	}
	return len(f.U) / f.StrideC
}

func (d *Decoder) reconstructMBInter(f *frame.Frame, mb *syntax.MBInter, mbX, mbY, qp int) {
	if f == nil || mb == nil {
		return
	}
	ref := d.refL0(mb.RefIdx[0])
	if ref == nil {
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				f.SetPixelY(mbX*16+x, mbY*16+y, 128)
			}
		}
		return
	}

	switch mb.MBType {
	case syntax.PMBTypeP16x16:
		mv := mb.MV[0]
		var predicted [256]uint8
		pred.InterPred16x16At(predicted[:], ref.Y, ref.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv.X, Y: mv.Y})
		d.applyWeightedPredL0Rect(predicted[:], mb.RefIdx[0], 0, 0, 16, 16)
		d.writeInterResidual(f, mb, predicted[:], mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP16x8:
		var predicted [256]uint8
		ref0 := d.refL0(mb.RefIdx[0])
		if ref0 == nil {
			ref0 = ref
		}
		mv0 := mb.MV[0]
		pred.InterPredLumaH264(predicted[:], 16, ref0.Y, ref0.StrideY, mbX*16, mbY*16, 16, 8, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		d.applyWeightedPredL0Rect(predicted[:], mb.RefIdx[0], 0, 0, 16, 8)
		ref1 := d.refL0(mb.RefIdx[1])
		if ref1 == nil {
			ref1 = ref
		}
		mv1 := mb.MV[1]
		pred.InterPredLumaH264(predicted[8*16:], 16, ref1.Y, ref1.StrideY, mbX*16, mbY*16+8, 16, 8, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		d.applyWeightedPredL0Rect(predicted[:], mb.RefIdx[1], 0, 8, 16, 8)
		d.writeInterResidual(f, mb, predicted[:], mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP8x16:
		var predicted [256]uint8
		ref0 := d.refL0(mb.RefIdx[0])
		if ref0 == nil {
			ref0 = ref
		}
		mv0 := mb.MV[0]
		pred.InterPredLumaH264(predicted[:], 16, ref0.Y, ref0.StrideY, mbX*16, mbY*16, 8, 16, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		d.applyWeightedPredL0Rect(predicted[:], mb.RefIdx[0], 0, 0, 8, 16)
		ref1 := d.refL0(mb.RefIdx[1])
		if ref1 == nil {
			ref1 = ref
		}
		mv1 := mb.MV[1]
		pred.InterPredLumaH264(predicted[8:], 16, ref1.Y, ref1.StrideY, mbX*16+8, mbY*16, 8, 16, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		d.applyWeightedPredL0Rect(predicted[:], mb.RefIdx[1], 8, 0, 8, 16)
		d.writeInterResidual(f, mb, predicted[:], mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		var predicted [256]uint8
		for part := 0; part < 4; part++ {
			partRef := ref
			if mb.MBType != syntax.PMBTypeP8x8ref0 {
				if r := d.refL0(mb.RefIdx[part]); r != nil {
					partRef = r
				}
			}
			baseX := (part & 1) * 8
			baseY := (part >> 1) * 8
			switch mb.SubMBType[part] {
			case 0: // P_L0_8x8
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 8, mb.SubMV[part*4])
			case 1: // P_L0_8x4
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 4, mb.SubMV[part*4])
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX, mbY*16+baseY+4, baseX, baseY+4, 8, 4, mb.SubMV[part*4+1])
			case 2: // P_L0_4x8
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 4, 8, mb.SubMV[part*4])
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX+4, mbY*16+baseY, baseX+4, baseY, 4, 8, mb.SubMV[part*4+1])
			case 3: // P_L0_4x4
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 4, 4, mb.SubMV[part*4])
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX+4, mbY*16+baseY, baseX+4, baseY, 4, 4, mb.SubMV[part*4+1])
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX, mbY*16+baseY+4, baseX, baseY+4, 4, 4, mb.SubMV[part*4+2])
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX+4, mbY*16+baseY+4, baseX+4, baseY+4, 4, 4, mb.SubMV[part*4+3])
			default:
				d.copyInterSubRect(predicted[:], partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 8, mb.SubMV[part*4])
			}
			d.applyWeightedPredL0Rect(predicted[:], mb.RefIdx[part], baseX, baseY, 8, 8)
		}
		d.writeInterResidual(f, mb, predicted[:], mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	default:
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				srcX := mbX*16 + x
				srcY := mbY*16 + y
				if srcX < ref.Width && srcY < ref.Height {
					f.SetPixelY(srcX, srcY, ref.PixelY(srcX, srcY))
				}
			}
		}
	}
}

func (d *Decoder) reconstructChromaInter(f, ref *frame.Frame, mb *syntax.MBInter, mbX, mbY, qp int) {
	if f == nil || mb == nil {
		return
	}
	var predU, predV [64]uint8
	fillBoth := func(partRef *frame.Frame, refIdx int8, baseX, baseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
		if partRef == nil {
			partRef = ref
		}
		if partRef == nil {
			return
		}
		// Inter prediction addresses the coded macroblock raster, including padded
		// rows beyond the display crop (FFmpeg uses mb_height here, not crop height).
		codedChromaHeight := frameChromaHeight(partRef)
		d.fillChromaInterPredRect(predU[:], partRef.U, partRef.StrideC, partRef.Width/2, codedChromaHeight, baseX, baseY, dstX, dstY, w, h, mv)
		d.fillChromaInterPredRect(predV[:], partRef.V, partRef.StrideC, partRef.Width/2, codedChromaHeight, baseX, baseY, dstX, dstY, w, h, mv)
		d.applyWeightedChromaL0Rect(predU[:], 0, refIdx, dstX, dstY, w, h)
		d.applyWeightedChromaL0Rect(predV[:], 1, refIdx, dstX, dstY, w, h)
	}
	baseX, baseY := mbX*8, mbY*8
	switch mb.MBType {
	case syntax.PMBTypeP16x8:
		for part := 0; part < 2; part++ {
			partRef := d.refL0(mb.RefIdx[part])
			fillBoth(partRef, mb.RefIdx[part], baseX, baseY+part*4, 0, part*4, 8, 4, mb.MV[part])
		}
	case syntax.PMBTypeP8x16:
		for part := 0; part < 2; part++ {
			partRef := d.refL0(mb.RefIdx[part])
			fillBoth(partRef, mb.RefIdx[part], baseX+part*4, baseY, part*4, 0, 4, 8, mb.MV[part])
		}
	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		for part := 0; part < 4; part++ {
			partRef := ref
			if mb.MBType != syntax.PMBTypeP8x8ref0 {
				partRef = d.refL0(mb.RefIdx[part])
			}
			dstX, dstY := (part&1)*4, (part>>1)*4
			switch mb.SubMBType[part] {
			case 1: // P_L0_8x4 -> two 4x2 chroma regions
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX, baseY+dstY, dstX, dstY, 4, 2, mb.SubMV[part*4])
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX, baseY+dstY+2, dstX, dstY+2, 4, 2, mb.SubMV[part*4+1])
			case 2: // P_L0_4x8 -> two 2x4 chroma regions
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX, baseY+dstY, dstX, dstY, 2, 4, mb.SubMV[part*4])
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX+2, baseY+dstY, dstX+2, dstY, 2, 4, mb.SubMV[part*4+1])
			case 3: // P_L0_4x4 -> four 2x2 chroma regions
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX, baseY+dstY, dstX, dstY, 2, 2, mb.SubMV[part*4])
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX+2, baseY+dstY, dstX+2, dstY, 2, 2, mb.SubMV[part*4+1])
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX, baseY+dstY+2, dstX, dstY+2, 2, 2, mb.SubMV[part*4+2])
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX+2, baseY+dstY+2, dstX+2, dstY+2, 2, 2, mb.SubMV[part*4+3])
			default:
				fillBoth(partRef, mb.RefIdx[part], baseX+dstX, baseY+dstY, dstX, dstY, 4, 4, mb.SubMV[part*4])
			}
		}
	default:
		fillBoth(ref, mb.RefIdx[0], baseX, baseY, 0, 0, 8, 8, mb.MV[0])
	}
	d.writeChromaInterResidual(f, mb, predU[:], 0, mbX, mbY, qp)
	d.writeChromaInterResidual(f, mb, predV[:], 1, mbX, mbY, qp)
}

func (d *Decoder) fillChromaInterPredRect(dst []uint8, plane []uint8, stride, width, height, baseX, baseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
	if len(dst) < 64 || w <= 0 || h <= 0 || dstX < 0 || dstY < 0 || dstX+w > 8 || dstY+h > 8 {
		return
	}
	// The former temporary made each partition read a snapshot of the source.
	// Keep that behavior for overlapping library inputs; decoder-owned prediction
	// and reference storage are disjoint and can use the final destination.
	if byteSlicesOverlapPortable(dst, plane) {
		var tmp [64]uint8
		fillChromaInterPredBlock(tmp[:], plane, stride, width, height, baseX, baseY, w, h, mv)
		for y := 0; y < h; y++ {
			copy(dst[(dstY+y)*8+dstX:(dstY+y)*8+dstX+w], tmp[y*8:y*8+w])
		}
		return
	}
	if !fillChromaInterPredBlock(dst[dstY*8+dstX:], plane, stride, width, height, baseX, baseY, w, h, mv) {
		// Preserve the zero prediction previously copied from the temporary
		// block when the reference plane was unusable.
		for y := 0; y < h; y++ {
			clear(dst[(dstY+y)*8+dstX : (dstY+y)*8+dstX+w])
		}
	}
}

func (d *Decoder) fillChromaInterPred(dst []uint8, plane []uint8, stride, width, height, baseX, baseY int, mv syntax.MotionVector) {
	if len(dst) < 64 {
		return
	}
	fillChromaInterPredBlock(dst, plane, stride, width, height, baseX, baseY, 8, 8, mv)
}

// fillChromaInterPredBlock writes only the requested w-by-h prediction, with
// destination stride 8. Its callers validate the destination rectangle. A false
// result leaves dst untouched because the reference plane is unusable.
func fillChromaInterPredBlock(dst []uint8, plane []uint8, stride, width, height, baseX, baseY, w, h int, mv syntax.MotionVector) bool {
	if len(plane) == 0 || stride <= 0 || width <= 0 || height <= 0 || width > stride {
		return false
	}
	lastPixel := (height-1)*stride + (width - 1)
	if lastPixel < 0 || lastPixel >= len(plane) {
		return false
	}
	intX := int(mv.X) >> 3
	intY := int(mv.Y) >> 3
	fracX := int(mv.X) & 7
	fracY := int(mv.Y) & 7
	sx0, sy0 := baseX+intX, baseY+intY
	if fracX == 0 && fracY == 0 && sx0 >= 0 && sy0 >= 0 && sx0+w <= width && sy0+h <= height {
		for y := 0; y < h; y++ {
			copy(dst[y*8:y*8+w], plane[(sy0+y)*stride+sx0:(sy0+y)*stride+sx0+w])
		}
		return true
	}
	sample := func(x, y int) int {
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if x >= width {
			x = width - 1
		}
		if y >= height {
			y = height - 1
		}
		return int(plane[y*stride+x])
	}
	if fracX == 0 && fracY == 0 {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst[y*8+x] = uint8(sample(sx0+x, sy0+y))
			}
		}
		return true
	}
	// The existing x86 kernel writes a full 8x8 block, not a subpartition.
	if w == 8 && h == 8 && chromaInter8Fast(dst, plane, stride, width, height, sx0, sy0, fracX, fracY) {
		return true
	}
	wx0, wx1 := 8-fracX, fracX
	wy0, wy1 := 8-fracY, fracY
	wa, wb, wc, wd := wx0*wy0, wx1*wy0, wx0*wy1, wx1*wy1
	// A full-block library call may alias its source. Read each bilinear
	// neighborhood immediately before writing, as the original scalar loop did.
	if byteSlicesOverlapPortable(dst, plane) {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sx, sy := sx0+x, sy0+y
				v := wa*sample(sx, sy) + wb*sample(sx+1, sy) + wc*sample(sx, sy+1) + wd*sample(sx+1, sy+1)
				dst[y*8+x] = uint8((v + 32) >> 6)
			}
		}
		return true
	}
	// A fully interior block needs no per-sample edge extension. Keep the extra
	// row and column in each source window for the bilinear neighbors.
	if sx0 >= 0 && sy0 >= 0 && sx0+w < width && sy0+h < height {
		if chromaInterSIMD(dst, plane[sy0*stride+sx0:], stride, w, h, wa, wb, wc, wd) {
			return true
		}
		for y := 0; y < h; y++ {
			start := (sy0+y)*stride + sx0
			top := plane[start : start+w+1]
			bottom := plane[start+stride : start+stride+w+1]
			row := dst[y*8 : y*8+w]
			for x := range row {
				v := wa*int(top[x]) + wb*int(top[x+1]) + wc*int(bottom[x]) + wd*int(bottom[x+1])
				row[x] = uint8((v + 32) >> 6)
			}
		}
		return true
	}
	// Extend the small bilinear footprint once. The scalar edge path used
	// four separately clamped samples per output pixel; this computes the
	// clamped columns/rows once and lets the existing SIMD kernel handle edges.
	// Callers validated a destination rectangle of at most 8 by 8 pixels.
	var edge [9 * 9]byte
	var columns [9]int
	for x := 0; x <= w; x++ {
		columns[x] = min(max(sx0+x, 0), width-1)
	}
	for y := 0; y <= h; y++ {
		sy := min(max(sy0+y, 0), height-1)
		row := plane[sy*stride : sy*stride+width]
		for x := 0; x <= w; x++ {
			edge[y*9+x] = row[columns[x]]
		}
	}
	if chromaInterSIMD(dst, edge[:], 9, w, h, wa, wb, wc, wd) {
		return true
	}
	for y := 0; y < h; y++ {
		top, bottom := edge[y*9:y*9+w+1], edge[(y+1)*9:(y+1)*9+w+1]
		row := dst[y*8 : y*8+w]
		for x := range row {
			v := wa*int(top[x]) + wb*int(top[x+1]) + wc*int(bottom[x]) + wd*int(bottom[x+1])
			row[x] = uint8((v + 32) >> 6)
		}
	}
	return true
}

func (d *Decoder) writeChromaInterResidual(f *frame.Frame, mb *syntax.MBInter, predicted []uint8, comp int, mbX, mbY, qp int) {
	if f == nil || mb == nil || len(predicted) < 64 || f.StrideC <= 0 {
		return
	}
	dstBaseX := mbX * 8
	dstBaseY := mbY * 8
	chromaH := frameChromaHeight(f)
	if dstBaseX < 0 || dstBaseY < 0 || dstBaseX+8 > f.Width/2 || dstBaseY+8 > chromaH || (dstBaseY+7)*f.StrideC+dstBaseX+8 > len(f.U) || (dstBaseY+7)*f.StrideC+dstBaseX+8 > len(f.V) {
		return
	}
	plane := f.U
	if comp != 0 {
		plane = f.V
	}
	if (mb.CBP>>4)&0x3 == 0 {
		for y := 0; y < 8; y++ {
			copy(plane[(dstBaseY+y)*f.StrideC+dstBaseX:(dstBaseY+y)*f.StrideC+dstBaseX+8], predicted[y*8:y*8+8])
		}
		return
	}
	chromaQP := frame.ChromaQP(qp, d.chromaQPOffset)
	var dc [4]int16
	for i := 0; i < 4; i++ {
		dc[i] = mb.CoeffsChroma[comp][i][0]
	}
	transform.Hadamard2x2DC(dc[:], chromaQP)
	var residual [4][16]int16
	var idctMask uint64
	for blk := 0; blk < 4; blk++ {
		if dc[blk] == 0 && mb.ChromaTotalCoeff[comp][blk] == 0 {
			continue
		}
		residual[blk] = mb.CoeffsChroma[comp][blk]
		residual[blk][0] = dc[blk]
		transform.Dequant4x4AC(residual[blk][:], chromaQP)
		idctMask |= uint64(1) << uint(blk)
	}
	transform.IDCT4x4BatchMask(residual[:], idctMask)
	for blk := 0; blk < 4; blk++ {
		bx, by := (blk&1)*4, (blk>>1)*4
		if idctMask&(uint64(1)<<uint(blk)) == 0 {
			for y := 0; y < 4; y++ {
				copy(plane[(dstBaseY+by+y)*f.StrideC+dstBaseX+bx:(dstBaseY+by+y)*f.StrideC+dstBaseX+bx+4], predicted[(by+y)*8+bx:(by+y)*8+bx+4])
			}
			continue
		}
		dstOff := (dstBaseY+by)*f.StrideC + dstBaseX + bx
		predOff := by*8 + bx
		residualAddStore(plane[dstOff:], f.StrideC, predicted[predOff:], 8, residual[blk][:], 4, 4, 4)
	}
}

func (d *Decoder) copyInterSubRect(dst []uint8, ref *frame.Frame, srcBaseX, srcBaseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
	if len(dst) < 256 || ref == nil || ref.StrideY <= 0 || len(ref.Y) == 0 || ref.Width <= 0 || ref.Width > ref.StrideY || w <= 0 || h <= 0 || dstX < 0 || dstY < 0 || dstX+w > 16 || dstY+h > 16 {
		return
	}
	refH := len(ref.Y) / ref.StrideY
	if refH <= 0 || ref.Height > refH {
		return
	}
	if int(mv.X)&3 == 0 && int(mv.Y)&3 == 0 {
		sx0 := srcBaseX + (int(mv.X) >> 2)
		sy0 := srcBaseY + (int(mv.Y) >> 2)
		if sx0 >= 0 && sy0 >= 0 && sx0+w <= ref.Width && sy0+h <= refH {
			for y := 0; y < h; y++ {
				copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], ref.Y[(sy0+y)*ref.StrideY+sx0:(sy0+y)*ref.StrideY+sx0+w])
			}
			return
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sx := clampInt(sx0+x, 0, ref.Width-1)
				sy := clampInt(sy0+y, 0, refH-1)
				dst[(dstY+y)*16+dstX+x] = ref.Y[sy*ref.StrideY+sx]
			}
		}
		return
	}
	// Preserve predict-then-copy semantics for overlapping library inputs.
	if byteSlicesOverlapPortable(dst, ref.Y) {
		var tmp [256]uint8
		pred.InterPredLumaH264(tmp[:], 16, ref.Y, ref.StrideY, srcBaseX, srcBaseY, w, h, pred.MotionVector{X: mv.X, Y: mv.Y})
		for y := 0; y < h; y++ {
			copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], tmp[y*16:y*16+w])
		}
		return
	}
	// Write the partition at its final offset; the predictor's destination
	// stride keeps the rest of the macroblock untouched.
	pred.InterPredLumaH264(dst[dstY*16+dstX:], 16, ref.Y, ref.StrideY, srcBaseX, srcBaseY, w, h, pred.MotionVector{X: mv.X, Y: mv.Y})
}

func (d *Decoder) writeInterResidual(f *frame.Frame, mb *syntax.MBInter, predicted []uint8, mbX, mbY, qp int) {
	if f == nil || mb == nil || len(predicted) < 256 || f.StrideY <= 0 {
		return
	}
	dstBaseX := mbX * 16
	dstBaseY := mbY * 16
	if dstBaseX < 0 || dstBaseY < 0 || dstBaseX+16 > f.Width || dstBaseY+16 > frameLumaHeight(f) || (dstBaseY+15)*f.StrideY+dstBaseX+16 > len(f.Y) {
		return
	}
	cbpLuma := mb.CBP & 0xF
	if cbpLuma == 0 {
		// No luma residual is present: copy the prediction as whole rows instead
		// of visiting sixteen empty 4x4 (or four empty 8x8) transform blocks.
		for y := 0; y < 16; y++ {
			start := (dstBaseY+y)*f.StrideY + dstBaseX
			copy(f.Y[start:start+16], predicted[y*16:y*16+16])
		}
		return
	}
	if mb.Use8x8Transform {
		for group := 0; group < 4; group++ {
			groupX := (group % 2) * 8
			groupY := (group / 2) * 8
			dstX := mbX*16 + groupX
			dstY := mbY*16 + groupY
			if cbpLuma&(1<<uint(group)) == 0 {
				for py := 0; py < 8; py++ {
					copy(f.Y[(dstY+py)*f.StrideY+dstX:(dstY+py)*f.StrideY+dstX+8], predicted[(groupY+py)*16+groupX:(groupY+py)*16+groupX+8])
				}
				continue
			}
			block := joinLuma8x8Residual(mb.Coeffs, group)
			if !coeff8x8NonZero(block) {
				for py := 0; py < 8; py++ {
					copy(f.Y[(dstY+py)*f.StrideY+dstX:(dstY+py)*f.StrideY+dstX+8], predicted[(groupY+py)*16+groupX:(groupY+py)*16+groupX+8])
				}
				continue
			}
			transform.Dequant8x8(block[:], qp)
			transform.IDCT8x8(block[:])
			dstOff := dstY*f.StrideY + dstX
			predOff := groupY*16 + groupX
			residualAddStore(f.Y[dstOff:], f.StrideY, predicted[predOff:], 16, block[:], 8, 8, 8)
		}
	} else {
		if transform.Reconstruct4x4Available() {
			dstBaseX, dstBaseY := mbX*16, mbY*16
			for blkIdx := 0; blkIdx < 16; blkIdx++ {
				bx, by := blk4x4X[blkIdx], blk4x4Y[blkIdx]
				dst := f.Y[(dstBaseY+by)*f.StrideY+dstBaseX+bx:]
				prediction := predicted[by*16+bx:]
				if cbpLuma&(1<<uint(blkIdx/4)) != 0 && mb.TotalCoeff[blkIdx] != 0 {
					transform.Reconstruct4x4(dst, prediction, &mb.Coeffs[blkIdx], f.StrideY, 16, qp)
				} else {
					for py := 0; py < 4; py++ {
						copy(dst[py*f.StrideY:py*f.StrideY+4], prediction[py*16:py*16+4])
					}
				}
			}
			return
		}
		var residual [16][16]int16
		var idctMask uint64
		for blkIdx := 0; blkIdx < 16; blkIdx++ {
			group := blkIdx / 4
			if cbpLuma&(1<<uint(group)) != 0 && mb.TotalCoeff[blkIdx] != 0 {
				residual[blkIdx] = mb.Coeffs[blkIdx]
				transform.Dequant4x4Block(&residual[blkIdx], qp)
				idctMask |= uint64(1) << uint(blkIdx)
			}
		}
		transform.IDCT4x4BatchMask(residual[:], idctMask)
		dstBaseX := mbX * 16
		dstBaseY := mbY * 16
		for blkIdx := 0; blkIdx < 16; blkIdx++ {
			bx := blk4x4X[blkIdx]
			by := blk4x4Y[blkIdx]
			if idctMask&(uint64(1)<<uint(blkIdx)) == 0 {
				for py := 0; py < 4; py++ {
					copy(f.Y[(dstBaseY+by+py)*f.StrideY+dstBaseX+bx:(dstBaseY+by+py)*f.StrideY+dstBaseX+bx+4], predicted[(by+py)*16+bx:(by+py)*16+bx+4])
				}
				continue
			}
			dstOff := (dstBaseY+by)*f.StrideY + dstBaseX + bx
			predOff := by*16 + bx
			residualAddStore(f.Y[dstOff:], f.StrideY, predicted[predOff:], 16, residual[blkIdx][:], 4, 4, 4)
		}
	}
}

func fillBPredBlock(dst []uint8, ref *frame.Frame, srcBaseX, srcBaseY, dstX, dstY, w, h int, mv syntax.MotionVector) bool {
	refH := frameLumaHeight(ref)
	if ref == nil || ref.Width <= 0 || refH <= 0 || ref.StrideY <= 0 || ref.Width > ref.StrideY || len(dst) < 256 || !valid16x16Rect(dstX, dstY, w, h) {
		return false
	}
	lastPixel := (refH-1)*ref.StrideY + (ref.Width - 1)
	if lastPixel < 0 || lastPixel >= len(ref.Y) {
		return false
	}
	// H.264 6-tap luma inter prediction for B-frame sub-blocks. Production
	// predictors use distinct macroblock/reference storage and can write directly
	// at the final stride. Retain predict-then-copy semantics for overlapping
	// direct-library inputs because scalar write-through would otherwise be visible.
	if byteSlicesOverlapPortable(dst, ref.Y) {
		var tmp [256]uint8
		pred.InterPredLumaH264(tmp[:], 16, ref.Y, ref.StrideY, srcBaseX, srcBaseY, w, h, pred.MotionVector{X: mv.X, Y: mv.Y})
		for y := 0; y < h; y++ {
			copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], tmp[y*16:y*16+w])
		}
		return true
	}
	pred.InterPredLumaH264(dst[dstY*16+dstX:], 16, ref.Y, ref.StrideY, srcBaseX, srcBaseY, w, h, pred.MotionVector{X: mv.X, Y: mv.Y})
	return true
}

func byteSlicesOverlapPortable(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	ap, bp := uintptr(unsafe.Pointer(&a[0])), uintptr(unsafe.Pointer(&b[0]))
	return ap <= bp && bp-ap < uintptr(len(a)) || bp < ap && ap-bp < uintptr(len(b))
}

func clear16x16Rect(dst []byte, dstX, dstY, w, h int) {
	for y := 0; y < h; y++ {
		clear(dst[(dstY+y)*16+dstX : (dstY+y)*16+dstX+w])
	}
}

func valid16x16Rect(x, y, w, h int) bool {
	return w > 0 && h > 0 && x >= 0 && y >= 0 && x+w <= 16 && y+h <= 16
}

func bMacroblockPartCount(mbType uint32) int {
	if mbType >= 4 && mbType <= 21 {
		return 2
	}
	return 1
}

func bPartRect(mbType uint32, part int) (x, y, w, h int) {
	if part != 1 {
		part = 0
	}
	if mbType == syntax.BMBTypeL016x8 || mbType == syntax.BMBTypeL116x8 || mbType == syntax.BMBTypeBi16x8 || mbType == 10 || mbType == 12 || mbType == 14 || mbType == 16 || mbType == 18 || mbType == 20 {
		return 0, part * 8, 16, 8
	}
	return part * 8, 0, 8, 16
}

func (d *Decoder) fillBPartPrediction(dst []uint8, mb *syntax.MBBidi, fallback *frame.Frame, mbX, mbY, dstX, dstY, w, h, part int) {
	if d == nil || mb == nil || len(dst) < 256 {
		return
	}
	d.fillBPredByUse(dst, fallback, mbX, mbY, dstX, dstY, w, h, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.MVL0[part], mb.MVL1[part], syntax.BPartUsesL0(mb.MBType, part), syntax.BPartUsesL1(mb.MBType, part))
}

func (d *Decoder) fillBSubPrediction(dst []uint8, mb *syntax.MBBidi, fallback *frame.Frame, mbX, mbY, dstX, dstY, part int) {
	if d == nil || mb == nil || len(dst) < 256 || part < 0 || part >= 4 {
		return
	}
	t := mb.SubMBType[part]
	useL0 := syntax.BSubUsesL0(t)
	useL1 := syntax.BSubUsesL1(t)
	if t == 0 {
		// Direct B sub-MBs use only lists whose derived reference index is valid.
		// Treating -1 as list index zero blends an unavailable list into spatial
		// Direct blocks and differs from FFmpeg's IS_DIR flags.
		d.fillBPredByUse(dst, fallback, mbX, mbY, dstX, dstY, 8, 8, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.SubMVL0[part*4], mb.SubMVL1[part*4], mb.RefIdxL0[part] >= 0, mb.RefIdxL1[part] >= 0)
		return
	}
	w4, h4 := syntax.BMBSubPartFillDims(t)
	parts := syntax.BMBSubPartCount(t)
	for j := 0; j < parts; j++ {
		ox4, oy4 := bSubPartOffset4x4(t, j)
		idx := part*4 + j
		d.fillBPredByUse(dst, fallback, mbX, mbY, dstX+ox4*4, dstY+oy4*4, w4*4, h4*4, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.SubMVL0[idx], mb.SubMVL1[idx], useL0, useL1)
	}
}

func (d *Decoder) fillBPredByUse(dst []uint8, fallback *frame.Frame, mbX, mbY, dstX, dstY, w, h int, refIdxL0, refIdxL1 int8, mvL0, mvL1 syntax.MotionVector, useL0, useL1 bool) {
	if len(dst) < 256 || !valid16x16Rect(dstX, dstY, w, h) || (!useL0 && !useL1) {
		return
	}
	currentPOC := 0
	if fallback != nil {
		currentPOC = fallback.POC
	}
	refFor := func(list int, refIdx int8) *frame.Frame {
		var ref *frame.Frame
		if list == 0 {
			ref = d.refBidiL0(refIdx, currentPOC)
		} else {
			ref = d.refBidiL1(refIdx, currentPOC)
		}
		if ref == nil {
			ref = fallback
		}
		return ref
	}
	if useL0 {
		if !fillBPredBlock(dst, refFor(0, refIdxL0), mbX*16+dstX, mbY*16+dstY, dstX, dstY, w, h, mvL0) {
			clear16x16Rect(dst, dstX, dstY, w, h)
		}
	}
	if !useL1 {
		d.applyExplicitUniLuma(dst, 0, refIdxL0, dstX, dstY, w, h)
		if d == nil || d.weightedBipredIDC != 1 {
			_, _ = d.biWeightsForRefs(refIdxL0, refIdxL1, currentPOC)
		}
		return
	}
	if !useL0 {
		if !fillBPredBlock(dst, refFor(1, refIdxL1), mbX*16+dstX, mbY*16+dstY, dstX, dstY, w, h, mvL1) {
			clear16x16Rect(dst, dstX, dstY, w, h)
		}
		d.applyExplicitUniLuma(dst, 1, refIdxL1, dstX, dstY, w, h)
		if d == nil || d.weightedBipredIDC != 1 {
			_, _ = d.biWeightsForRefs(refIdxL0, refIdxL1, currentPOC)
		}
		return
	}
	var predL1 [256]uint8
	fillBPredBlock(predL1[:], refFor(1, refIdxL1), mbX*16+dstX, mbY*16+dstY, dstX, dstY, w, h, mvL1)
	off := dstY*16 + dstX
	if d != nil && d.weightedBipredIDC == 1 {
		p := d.explicitBiLumaParams(refIdxL0, refIdxL1)
		biBlendRectParams(dst[off:], dst[off:], predL1[off:], 16, w, h, p.w0, p.w1, p.round, p.shift, p.offset)
		return
	}
	w0, w1 := d.biWeightsForRefs(refIdxL0, refIdxL1, currentPOC)
	biBlendRectPixels(dst[off:], dst[off:], predL1[off:], 16, w, h, w0, w1)
}

func (d *Decoder) fillBChromaByUse(dst []uint8, comp int, fallback *frame.Frame, mbX, mbY, dstX, dstY, w, h int, refIdxL0, refIdxL1 int8, mvL0, mvL1 syntax.MotionVector, useL0, useL1 bool) {
	if len(dst) < 64 || dstX < 0 || dstY < 0 || w <= 0 || h <= 0 || dstX+w > 8 || dstY+h > 8 {
		return
	}
	var predL0, predL1 [64]uint8
	fill := func(out []uint8, ref *frame.Frame, mv syntax.MotionVector) {
		if ref == nil {
			ref = fallback
		}
		if ref == nil {
			return
		}
		plane := ref.U
		if comp != 0 {
			plane = ref.V
		}
		d.fillChromaInterPredRect(out, plane, ref.StrideC, ref.Width/2, frameChromaHeight(ref), mbX*8+dstX, mbY*8+dstY, dstX, dstY, w, h, mv)
	}
	currentPOC := 0
	if fallback != nil {
		currentPOC = fallback.POC
	}
	if useL0 {
		fill(predL0[:], d.refBidiL0(refIdxL0, currentPOC), mvL0)
	}
	if useL1 {
		fill(predL1[:], d.refBidiL1(refIdxL1, currentPOC), mvL1)
	}
	if useL0 && useL1 {
		off := dstY*8 + dstX
		if d != nil && d.weightedBipredIDC == 1 {
			p := d.biChromaParams(comp, refIdxL0, refIdxL1, currentPOC)
			biBlendRectParams(dst[off:], predL0[off:], predL1[off:], 8, w, h, p.w0, p.w1, p.round, p.shift, p.offset)
		} else {
			w0, w1 := d.biWeightsForRefs(refIdxL0, refIdxL1, currentPOC)
			biBlendRectPixels(dst[off:], predL0[off:], predL1[off:], 8, w, h, w0, w1)
		}
		return
	}
	if d == nil || d.weightedBipredIDC != 1 {
		_, _ = d.biWeightsForRefs(refIdxL0, refIdxL1, currentPOC)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (dstY+y)*8 + dstX + x
			if useL1 {
				dst[idx] = predL1[idx]
			} else if useL0 {
				dst[idx] = predL0[idx]
			}
		}
	}
	if useL1 {
		d.applyExplicitUniChroma(dst, comp, 1, refIdxL1, dstX, dstY, w, h)
	} else if useL0 {
		d.applyExplicitUniChroma(dst, comp, 0, refIdxL0, dstX, dstY, w, h)
	}
}

func coeff8x8NonZero(block [64]int16) bool {
	for i := range block {
		if block[i] != 0 {
			return true
		}
	}
	return false
}

func (d *Decoder) traceDirectMB(f *frame.Frame, mb *syntax.MBBidi, mbX, mbY int) {
	if d == nil || !d.trace.enabled(traceDirect) || f == nil || mb == nil || (mb.MBType != syntax.BMBTypeDirect16x16 && mb.MBType != syntax.BMBTypeB8x8) {
		return
	}
	sub0, sub1, sub2, sub3 := directTraceSubTypes(mb)
	smv0, smv1, smv2, smv3 := directTraceSubMVs(mb)
	fmt.Fprintf(os.Stderr,
		"GODIRECT mb=%04d x=%02d y=%02d poc=%d spatial=%d mb_type=%d ref0=%d ref1=%d mv0={%d,%d} mv1={%d,%d} sub0=%d sub1=%d sub2=%d sub3=%d submv0={%d,%d} submv1={%d,%d} submv2={%d,%d} submv3={%d,%d}\n",
		mbY*d.mbW+mbX, mbX, mbY, f.POC, boolInt(mb.DirectSpatial), mb.MBType,
		mb.RefIdxL0[0], mb.RefIdxL1[0], mb.MVL0[0].X, mb.MVL0[0].Y, mb.MVL1[0].X, mb.MVL1[0].Y,
		sub0, sub1, sub2, sub3,
		smv0.X, smv0.Y, smv1.X, smv1.Y, smv2.X, smv2.Y, smv3.X, smv3.Y)
}

func (d *Decoder) traceBidiMB(f *frame.Frame, mb *syntax.MBBidi, mbX, mbY int) {
	if d == nil || !d.trace.enabled(traceBMB) || f == nil || mb == nil {
		return
	}
	sub0, sub1, sub2, sub3 := directTraceSubTypes(mb)
	smv0, smv1, smv2, smv3 := directTraceSubMVs(mb)
	p0L0, p0L1 := bTracePart0MVs(mb)
	p1L0, p1L1 := bTracePart1MVs(mb)
	ref0p1, ref1p1 := mb.RefIdxL0[0], mb.RefIdxL1[0]
	if mb.MBType == syntax.BMBTypeB8x8 || cabacBPartsForType(mb.MBType) == 2 {
		ref0p1, ref1p1 = mb.RefIdxL0[1], mb.RefIdxL1[1]
	}
	fmt.Fprintf(os.Stderr,
		"GOBIDI mb=%04d x=%02d y=%02d poc=%d spatial=%d mb_type=%d ref0=%d ref1=%d ref0p1=%d ref1p1=%d mv0={%d,%d} mv1={%d,%d} mv0p1={%d,%d} mv1p1={%d,%d} cbp=%02x qpd=%d amvd0={%d,%d} mvd0={%d,%d} mvp0={%d,%d} amvd0p1={%d,%d} mvd0p1={%d,%d} mvp0p1={%d,%d} amvd1={%d,%d} mvd1={%d,%d} mvp1={%d,%d} amvd1p1={%d,%d} mvd1p1={%d,%d} mvp1p1={%d,%d} sub0=%d sub1=%d sub2=%d sub3=%d submv0={%d,%d} submv1={%d,%d} submv2={%d,%d} submv3={%d,%d}\n",
		mbY*d.mbW+mbX, mbX, mbY, f.POC, boolInt(mb.DirectSpatial), mb.MBType,
		mb.RefIdxL0[0], mb.RefIdxL1[0], ref0p1, ref1p1, p0L0.X, p0L0.Y, p0L1.X, p0L1.Y,
		p1L0.X, p1L0.Y, p1L1.X, p1L1.Y,
		mb.CBP, mb.QPDelta,
		mb.AMVDL0[0].X, mb.AMVDL0[0].Y, mb.MVDL0[0].X, mb.MVDL0[0].Y, mb.MVPL0[0].X, mb.MVPL0[0].Y,
		mb.AMVDL0[1].X, mb.AMVDL0[1].Y, mb.MVDL0[1].X, mb.MVDL0[1].Y, mb.MVPL0[1].X, mb.MVPL0[1].Y,
		mb.AMVDL1[0].X, mb.AMVDL1[0].Y, mb.MVDL1[0].X, mb.MVDL1[0].Y, mb.MVPL1[0].X, mb.MVPL1[0].Y,
		mb.AMVDL1[1].X, mb.AMVDL1[1].Y, mb.MVDL1[1].X, mb.MVDL1[1].Y, mb.MVPL1[1].X, mb.MVPL1[1].Y,
		sub0, sub1, sub2, sub3,
		smv0.X, smv0.Y, smv1.X, smv1.Y, smv2.X, smv2.Y, smv3.X, smv3.Y)
}

func (d *Decoder) reconstructMBBidi(f *frame.Frame, mb *syntax.MBBidi, mbX, mbY, qp int) {
	if d == nil || f == nil || mb == nil {
		return
	}
	d.traceDirectMB(f, mb, mbX, mbY)
	d.traceBidiMB(f, mb, mbX, mbY)
	dstBaseX := mbX * 16
	dstBaseY := mbY * 16
	if dstBaseX < 0 || dstBaseY < 0 || dstBaseX+16 > f.Width || dstBaseY+16 > frameLumaHeight(f) {
		return
	}
	refL0 := d.refBidiL0(mb.RefIdxL0[0], f.POC)
	refL1 := d.refBidiL1(mb.RefIdxL1[0], f.POC)
	if refL0 == nil {
		refL0 = f
	}
	if refL1 == nil {
		refL1 = refL0
	}
	if refL0 == nil || refL1 == nil || refL0.Width <= 0 || refL0.Height <= 0 || refL1.Width <= 0 || refL1.Height <= 0 {
		return
	}

	if d.trace.enabled(traceDirectCol) && mb.MBType == syntax.BMBTypeDirect16x16 {
		if colocated := d.refBidiL1(0, f.POC); colocatedDirectUses8x8(colocated, mbX, mbY) {
			for part := 0; part < 4; part++ {
				_ = colocatedDirect8x8ZeroConfig(colocated, mbX, mbY, part, f.POC, &d.trace)
			}
		}
	}
	var blended [256]uint8
	var blendedU, blendedV [64]uint8
	fillChromaRect := func(dstX, dstY, w, h int, refIdxL0, refIdxL1 int8, mvL0, mvL1 syntax.MotionVector, useL0, useL1 bool) {
		d.fillBChromaByUse(blendedU[:], 0, f, mbX, mbY, dstX, dstY, w, h, refIdxL0, refIdxL1, mvL0, mvL1, useL0, useL1)
		d.fillBChromaByUse(blendedV[:], 1, f, mbX, mbY, dstX, dstY, w, h, refIdxL0, refIdxL1, mvL0, mvL1, useL0, useL1)
	}
	if mb.MBType == syntax.BMBTypeB8x8 {
		for part := 0; part < 4; part++ {
			x0 := (part & 1) * 8
			y0 := (part >> 1) * 8
			d.fillBSubPrediction(blended[:], mb, f, mbX, mbY, x0, y0, part)
			t := mb.SubMBType[part]
			useL0 := syntax.BSubUsesL0(t)
			useL1 := syntax.BSubUsesL1(t)
			cx0, cy0 := x0/2, y0/2
			if t == 0 {
				fillChromaRect(cx0, cy0, 4, 4, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.SubMVL0[part*4], mb.SubMVL1[part*4], mb.RefIdxL0[part] >= 0, mb.RefIdxL1[part] >= 0)
				continue
			}
			w4, h4 := syntax.BMBSubPartFillDims(t)
			parts := syntax.BMBSubPartCount(t)
			for j := 0; j < parts; j++ {
				ox4, oy4 := bSubPartOffset4x4(t, j)
				idx := part*4 + j
				fillChromaRect(cx0+ox4*2, cy0+oy4*2, w4*2, h4*2, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.SubMVL0[idx], mb.SubMVL1[idx], useL0, useL1)
			}
		}
	} else if bMacroblockPartCount(mb.MBType) == 2 {
		for part := 0; part < 2; part++ {
			x0, y0, w, h := bPartRect(mb.MBType, part)
			d.fillBPartPrediction(blended[:], mb, f, mbX, mbY, x0, y0, w, h, part)
			fillChromaRect(x0/2, y0/2, w/2, h/2, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.MVL0[part], mb.MVL1[part], syntax.BPartUsesL0(mb.MBType, part), syntax.BPartUsesL1(mb.MBType, part))
		}
	} else if mb.MBType == syntax.BMBTypeDirect16x16 && direct16HasSubMVs(mb) {
		for part := 0; part < 4; part++ {
			x0 := (part & 1) * 8
			y0 := (part >> 1) * 8
			useL0 := mb.RefIdxL0[part] >= 0
			useL1 := mb.RefIdxL1[part] >= 0
			d.fillBPredByUse(blended[:], f, mbX, mbY, x0, y0, 8, 8, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.SubMVL0[part*4], mb.SubMVL1[part*4], useL0, useL1)
			fillChromaRect(x0/2, y0/2, 4, 4, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.SubMVL0[part*4], mb.SubMVL1[part*4], useL0, useL1)
		}
	} else {
		// Determine prediction direction. Explicit B_L0/B_L1/B_Bi types map
		// directly; B_Direct_16x16 (spatial direct, uniform MVs) is bi-predictive
		// whenever both derived reference indices are valid, matching FFmpeg's
		// direct reconstruction. Earlier this path fell back to L0-only, which
		// darkens fade-in B frames by averaging out one reference.
		useL0 := true
		useL1 := false
		switch mb.MBType {
		case syntax.BMBTypeBi16x16:
			useL0, useL1 = true, true
		case syntax.BMBTypeL116x16:
			useL0, useL1 = false, true
		case syntax.BMBTypeDirect16x16:
			useL0 = mb.RefIdxL0[0] >= 0
			useL1 = mb.RefIdxL1[0] >= 0
			if !useL0 && !useL1 {
				useL0 = true
			}
		}
		switch {
		case useL0 && useL1:
			var predL1 [256]uint8
			fillBPredBlock(blended[:], refL0, mbX*16, mbY*16, 0, 0, 16, 16, mb.MVL0[0])
			fillBPredBlock(predL1[:], refL1, mbX*16, mbY*16, 0, 0, 16, 16, mb.MVL1[0])
			d.biBlendRect(blended[:], blended[:], predL1[:], refL0, refL1, mb.RefIdxL0[0], mb.RefIdxL1[0], 0, 0, 16, 16)
			fillChromaRect(0, 0, 8, 8, mb.RefIdxL0[0], mb.RefIdxL1[0], mb.MVL0[0], mb.MVL1[0], true, true)
		case useL1:
			fillBPredBlock(blended[:], refL1, mbX*16, mbY*16, 0, 0, 16, 16, mb.MVL1[0])
			d.applyExplicitUniLuma(blended[:], 1, mb.RefIdxL1[0], 0, 0, 16, 16)
			fillChromaRect(0, 0, 8, 8, mb.RefIdxL0[0], mb.RefIdxL1[0], mb.MVL0[0], mb.MVL1[0], false, true)
		default:
			fillBPredBlock(blended[:], refL0, mbX*16, mbY*16, 0, 0, 16, 16, mb.MVL0[0])
			d.applyExplicitUniLuma(blended[:], 0, mb.RefIdxL0[0], 0, 0, 16, 16)
			fillChromaRect(0, 0, 8, 8, mb.RefIdxL0[0], mb.RefIdxL1[0], mb.MVL0[0], mb.MVL1[0], true, false)
		}
	}
	residualMB := &syntax.MBInter{
		CBP:              mb.CBP,
		Use8x8Transform:  mb.Use8x8Transform,
		Coeffs:           mb.Coeffs,
		TotalCoeff:       mb.TotalCoeff,
		CoeffsChroma:     mb.CoeffsChroma,
		ChromaTotalCoeff: mb.ChromaTotalCoeff,
	}
	d.writeInterResidual(f, residualMB, blended[:], mbX, mbY, qp)
	d.writeChromaInterResidual(f, residualMB, blendedU[:], 0, mbX, mbY, qp)
	d.writeChromaInterResidual(f, residualMB, blendedV[:], 1, mbX, mbY, qp)
}

func directTraceSubTypes(mb *syntax.MBBidi) (uint32, uint32, uint32, uint32) {
	if mb == nil {
		return 0, 0, 0, 0
	}
	if mb.MBType == syntax.BMBTypeDirect16x16 {
		// FFmpeg's direct trace reports internal MB_TYPE flags for direct sub-MBs.
		// 12552 = MB_TYPE_16x16 | MB_TYPE_DIRECT2 | MB_TYPE_L0; this is the
		// common full-direct shape while our syntax-level representation remains 0.
		return 12552, 12552, 12552, 12552
	}
	return directTraceSubType(mb.SubMBType[0]), directTraceSubType(mb.SubMBType[1]), directTraceSubType(mb.SubMBType[2]), directTraceSubType(mb.SubMBType[3])
}

func directTraceSubType(t uint32) uint32 {
	// FFmpeg FFDIRECT reports internal MB_TYPE flags, not H.264 syntax sub_mb_type.
	// Constants from libavcodec/mpegutils.h:
	// 16x16=8, 16x8=16, 8x16=32, 8x8=64, DIRECT2=256,
	// P0L0=4096, P1L0=8192, P0L1=16384, P1L1=32768.
	var ffBSubType = [...]uint32{
		0:  12552, // DIRECT2 | 16x16 | L0
		1:  4104,  // L0_8x8
		2:  16392, // L1_8x8
		3:  20488, // Bi_8x8
		4:  12304, // L0_8x4
		5:  12320, // L0_4x8
		6:  49168, // L1_8x4
		7:  49184, // L1_4x8
		8:  61456, // Bi_8x4
		9:  61472, // Bi_4x8
		10: 12352, // L0_4x4
		11: 49216, // L1_4x4
		12: 61504, // Bi_4x4
	}
	if t < uint32(len(ffBSubType)) {
		return ffBSubType[t]
	}
	return t
}

func bTracePart0MVs(mb *syntax.MBBidi) (syntax.MotionVector, syntax.MotionVector) {
	if mb == nil {
		return syntax.MotionVector{}, syntax.MotionVector{}
	}
	if mb.MBType == syntax.BMBTypeB8x8 {
		return mb.SubMVL0[0], mb.SubMVL1[0]
	}
	return mb.MVL0[0], mb.MVL1[0]
}

func bTracePart1MVs(mb *syntax.MBBidi) (syntax.MotionVector, syntax.MotionVector) {
	if mb == nil {
		return syntax.MotionVector{}, syntax.MotionVector{}
	}
	if bMacroblockPartCount(mb.MBType) == 1 && mb.MBType != syntax.BMBTypeB8x8 {
		return mb.MVL0[0], mb.MVL1[0]
	}
	if mb.MBType == syntax.BMBTypeB8x8 {
		return mb.SubMVL0[4], mb.SubMVL1[4]
	}
	return mb.MVL0[1], mb.MVL1[1]
}

func directTraceSubMVs(mb *syntax.MBBidi) (syntax.MotionVector, syntax.MotionVector, syntax.MotionVector, syntax.MotionVector) {
	if mb == nil {
		return syntax.MotionVector{}, syntax.MotionVector{}, syntax.MotionVector{}, syntax.MotionVector{}
	}
	if mb.MBType != syntax.BMBTypeB8x8 && mb.MBType != syntax.BMBTypeDirect16x16 {
		return directTracePartitionMVs(mb)
	}
	return mb.SubMVL0[0], mb.SubMVL0[4], mb.SubMVL0[8], mb.SubMVL0[12]
}

func direct16HasSubMVs(mb *syntax.MBBidi) bool {
	if mb == nil || mb.MBType != syntax.BMBTypeDirect16x16 {
		return false
	}
	for part := 0; part < 4; part++ {
		idx := part * 4
		if mb.SubMVL0[idx] != mb.MVL0[0] || mb.SubMVL1[idx] != mb.MVL1[0] || mb.RefIdxL0[part] != mb.RefIdxL0[0] || mb.RefIdxL1[part] != mb.RefIdxL1[0] {
			return true
		}
	}
	return false
}

func directTracePartitionMVs(mb *syntax.MBBidi) (syntax.MotionVector, syntax.MotionVector, syntax.MotionVector, syntax.MotionVector) {
	s0, s1, s2, s3 := mb.MVL0[0], mb.MVL0[0], mb.MVL0[0], mb.MVL0[0]
	if cabacBPartsForType(mb.MBType) == 2 {
		if cabacBIs8x16(mb.MBType) {
			s1, s3 = mb.MVL0[1], mb.MVL0[1]
		} else {
			s2, s3 = mb.MVL0[1], mb.MVL0[1]
		}
	}
	return s0, s1, s2, s3
}

// bidiL0Frames returns the ordered L0 reference frame list for a B-slice.
func (d *Decoder) bidiL0Frames(currentPOC int) []*frame.Frame {
	return d.bidiL0FramesWithMods(currentPOC, 0, nil)
}

func (d *Decoder) bidiL0FramesWithMods(currentPOC int, currentFrameNum uint32, mods []syntax.RefPicListModification) []*frame.Frame {
	if d == nil || d.DPB == nil {
		return nil
	}
	currentOrderPOC := d.currentBidiOrderPOC(currentPOC)
	var frames []*frame.Frame
	for _, fr := range d.DPB.Frames {
		if fr != nil && fr.IsRef && frameOrderPOC(fr) < currentOrderPOC {
			frames = append(frames, fr)
		}
	}
	// Sort by descending unwrapped POC (nearest past first).
	for i := 0; i < len(frames)-1; i++ {
		for j := i + 1; j < len(frames); j++ {
			if frameOrderPOC(frames[j]) > frameOrderPOC(frames[i]) {
				frames[i], frames[j] = frames[j], frames[i]
			}
		}
	}
	if len(mods) > 0 {
		maxPicNum := 16
		pred := int(currentFrameNum) & (maxPicNum - 1)
		for index, mod := range mods {
			if index >= len(frames) || (mod.Op != 0 && mod.Op != 1) {
				continue
			}
			diff := int(mod.Val) + 1
			if mod.Op == 0 {
				pred = (pred - diff) & (maxPicNum - 1)
			} else {
				pred = (pred + diff) & (maxPicNum - 1)
			}
			found := -1
			for i, fr := range frames {
				if fr != nil && fr.FrameNum == pred {
					found = i
					break
				}
			}
			if found < 0 || found < index {
				continue
			}
			ref := frames[found]
			if index >= len(frames) {
				frames = append(frames, ref)
				continue
			}
			if found < index {
				frames = append(frames, nil)
				copy(frames[index+1:], frames[index:len(frames)-1])
				frames[index] = ref
				continue
			}
			if found > index {
				copy(frames[index+1:found+1], frames[index:found])
			}
			frames[index] = ref
		}
	}
	return frames
}
