package decode

// decode/reconstruct_inter.go — inter macroblock reconstruction.
// Covers P-skip, P16x16/P16x8/P8x16/P8x8 motion compensation, B-frame blending,
// and chroma inter prediction.

import (
	"fmt"
	"os"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
	"github.com/rcarmo/go-264/transform"
)

func dpbHasReferenceFrames(frames []*frame.Frame) bool {
	for _, fr := range frames {
		if fr != nil && fr.IsRef {
			return true
		}
	}
	return false
}

func (d *Decoder) refL0(refIdx int8) *frame.Frame {
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
		if seen == idx {
			return fr
		}
		seen++
	}
	for i := len(d.DPB.Frames) - 1; i >= 0; i-- {
		if d.DPB.Frames[i] != nil {
			return d.DPB.Frames[i]
		}
	}
	return nil
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
// Uses POC-ordered lookup when the DPB has frames with distinct POCs; falls back
// to simple index-from-end when all frames share the same POC.
func (d *Decoder) refBidiL0(refIdx int8, currentPOC int) *frame.Frame {
	if d == nil || d.DPB == nil || len(d.DPB.Frames) == 0 {
		return nil
	}
	// Build ordered L0 list: frames with POC < currentPOC, sorted by descending POC.
	var pastFrames []*frame.Frame
	for _, fr := range d.DPB.Frames {
		if fr != nil && fr.IsRef && fr.POC < currentPOC {
			pastFrames = append(pastFrames, fr)
		}
	}
	// Sort by descending POC (most recent past first).
	for i := 0; i < len(pastFrames)-1; i++ {
		for j := i + 1; j < len(pastFrames); j++ {
			if pastFrames[j].POC > pastFrames[i].POC {
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
	if d == nil || d.DPB == nil || len(d.DPB.Frames) == 0 {
		return nil
	}
	// Build ordered L1 list per H.264 §8.2.4.2.3:
	// 1. Short-term refs with POC > currentPOC, sorted by ascending POC
	// 2. Short-term refs with POC <= currentPOC, sorted by descending POC
	var futureFrames, pastFrames []*frame.Frame
	for _, fr := range d.DPB.Frames {
		if fr != nil && fr.IsRef {
			if fr.POC > currentPOC {
				futureFrames = append(futureFrames, fr)
			} else {
				pastFrames = append(pastFrames, fr)
			}
		}
	}
	// Sort future by ascending POC.
	for i := 0; i < len(futureFrames)-1; i++ {
		for j := i + 1; j < len(futureFrames); j++ {
			if futureFrames[j].POC < futureFrames[i].POC {
				futureFrames[i], futureFrames[j] = futureFrames[j], futureFrames[i]
			}
		}
	}
	// Sort past by descending POC.
	for i := 0; i < len(pastFrames)-1; i++ {
		for j := i + 1; j < len(pastFrames); j++ {
			if pastFrames[j].POC > pastFrames[i].POC {
				pastFrames[i], pastFrames[j] = pastFrames[j], pastFrames[i]
			}
		}
	}
	// Concatenate: future first, then past.
	l1 := append(futureFrames, pastFrames...)
	idx := int(refIdx)
	if idx < 0 {
		idx = 0
	}
	if idx < len(l1) {
		return l1[idx]
	}
	if len(l1) > 0 {
		return l1[len(l1)-1]
	}
	return nil
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
		d.writeInterResidual(f, mb, predicted[:], mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP16x8:
		var predicted [256]uint8
		var tmp [256]uint8
		ref0 := d.refL0(mb.RefIdx[0])
		if ref0 == nil {
			ref0 = ref
		}
		mv0 := mb.MV[0]
		pred.InterPred16x16At(tmp[:], ref0.Y, ref0.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		for y := 0; y < 8; y++ {
			copy(predicted[y*16:y*16+16], tmp[y*16:y*16+16])
		}
		ref1 := d.refL0(mb.RefIdx[1])
		if ref1 == nil {
			ref1 = ref
		}
		mv1 := mb.MV[1]
		pred.InterPred16x16At(tmp[:], ref1.Y, ref1.StrideY, mbX*16, mbY*16+8, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		for y := 0; y < 8; y++ {
			copy(predicted[(y+8)*16:(y+8)*16+16], tmp[y*16:y*16+16])
		}
		d.writeInterResidual(f, mb, predicted[:], mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP8x16:
		var predicted [256]uint8
		var tmp [256]uint8
		ref0 := d.refL0(mb.RefIdx[0])
		if ref0 == nil {
			ref0 = ref
		}
		mv0 := mb.MV[0]
		pred.InterPred16x16At(tmp[:], ref0.Y, ref0.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		for y := 0; y < 16; y++ {
			copy(predicted[y*16:y*16+8], tmp[y*16:y*16+8])
		}
		ref1 := d.refL0(mb.RefIdx[1])
		if ref1 == nil {
			ref1 = ref
		}
		mv1 := mb.MV[1]
		pred.InterPred16x16At(tmp[:], ref1.Y, ref1.StrideY, mbX*16+8, mbY*16, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		for y := 0; y < 16; y++ {
			copy(predicted[y*16+8:y*16+16], tmp[y*16:y*16+8])
		}
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
	fillBoth := func(partRef *frame.Frame, baseX, baseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
		if partRef == nil {
			partRef = ref
		}
		if partRef == nil {
			return
		}
		d.fillChromaInterPredRect(predU[:], partRef.U, partRef.StrideC, partRef.Width/2, partRef.Height/2, baseX, baseY, dstX, dstY, w, h, mv)
		d.fillChromaInterPredRect(predV[:], partRef.V, partRef.StrideC, partRef.Width/2, partRef.Height/2, baseX, baseY, dstX, dstY, w, h, mv)
	}
	baseX, baseY := mbX*8, mbY*8
	switch mb.MBType {
	case syntax.PMBTypeP16x8:
		for part := 0; part < 2; part++ {
			partRef := d.refL0(mb.RefIdx[part])
			fillBoth(partRef, baseX, baseY+part*4, 0, part*4, 8, 4, mb.MV[part])
		}
	case syntax.PMBTypeP8x16:
		for part := 0; part < 2; part++ {
			partRef := d.refL0(mb.RefIdx[part])
			fillBoth(partRef, baseX+part*4, baseY, part*4, 0, 4, 8, mb.MV[part])
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
				fillBoth(partRef, baseX+dstX, baseY+dstY, dstX, dstY, 4, 2, mb.SubMV[part*4])
				fillBoth(partRef, baseX+dstX, baseY+dstY+2, dstX, dstY+2, 4, 2, mb.SubMV[part*4+1])
			case 2: // P_L0_4x8 -> two 2x4 chroma regions
				fillBoth(partRef, baseX+dstX, baseY+dstY, dstX, dstY, 2, 4, mb.SubMV[part*4])
				fillBoth(partRef, baseX+dstX+2, baseY+dstY, dstX+2, dstY, 2, 4, mb.SubMV[part*4+1])
			case 3: // P_L0_4x4 -> four 2x2 chroma regions
				fillBoth(partRef, baseX+dstX, baseY+dstY, dstX, dstY, 2, 2, mb.SubMV[part*4])
				fillBoth(partRef, baseX+dstX+2, baseY+dstY, dstX+2, dstY, 2, 2, mb.SubMV[part*4+1])
				fillBoth(partRef, baseX+dstX, baseY+dstY+2, dstX, dstY+2, 2, 2, mb.SubMV[part*4+2])
				fillBoth(partRef, baseX+dstX+2, baseY+dstY+2, dstX+2, dstY+2, 2, 2, mb.SubMV[part*4+3])
			default:
				fillBoth(partRef, baseX+dstX, baseY+dstY, dstX, dstY, 4, 4, mb.SubMV[part*4])
			}
		}
	default:
		fillBoth(ref, baseX, baseY, 0, 0, 8, 8, mb.MV[0])
	}
	d.writeChromaInterResidual(f, mb, predU[:], 0, mbX, mbY, qp)
	d.writeChromaInterResidual(f, mb, predV[:], 1, mbX, mbY, qp)
}

func (d *Decoder) fillChromaInterPredRect(dst []uint8, plane []uint8, stride, width, height, baseX, baseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
	if len(dst) < 64 || w <= 0 || h <= 0 || dstX < 0 || dstY < 0 || dstX+w > 8 || dstY+h > 8 {
		return
	}
	var tmp [64]uint8
	d.fillChromaInterPred(tmp[:], plane, stride, width, height, baseX, baseY, mv)
	for y := 0; y < h; y++ {
		copy(dst[(dstY+y)*8+dstX:(dstY+y)*8+dstX+w], tmp[y*8:y*8+w])
	}
}

func (d *Decoder) fillChromaInterPred(dst []uint8, plane []uint8, stride, width, height, baseX, baseY int, mv syntax.MotionVector) {
	if len(dst) < 64 || len(plane) == 0 || stride <= 0 || width <= 0 || height <= 0 || width > stride {
		return
	}
	lastPixel := (height-1)*stride + (width - 1)
	if lastPixel < 0 || lastPixel >= len(plane) {
		return
	}
	dx := int(mv.X) >> 3
	dy := int(mv.Y) >> 3
	sx0, sy0 := baseX+dx, baseY+dy
	if sx0 >= 0 && sy0 >= 0 && sx0+8 <= width && sy0+8 <= height {
		for y := 0; y < 8; y++ {
			copy(dst[y*8:y*8+8], plane[(sy0+y)*stride+sx0:(sy0+y)*stride+sx0+8])
		}
		return
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			sx, sy := sx0+x, sy0+y
			if sx < 0 {
				sx = 0
			}
			if sy < 0 {
				sy = 0
			}
			if sx >= width {
				sx = width - 1
			}
			if sy >= height {
				sy = height - 1
			}
			dst[y*8+x] = plane[sy*stride+sx]
		}
	}
}

func (d *Decoder) writeChromaInterResidual(f *frame.Frame, mb *syntax.MBInter, predicted []uint8, comp int, mbX, mbY, qp int) {
	if f == nil || mb == nil || len(predicted) < 64 || f.StrideC <= 0 {
		return
	}
	dstBaseX := mbX * 8
	dstBaseY := mbY * 8
	if dstBaseX < 0 || dstBaseY < 0 || dstBaseX+8 > f.Width/2 || dstBaseY+8 > f.Height/2 || (dstBaseY+7)*f.StrideC+dstBaseX+8 > len(f.U) || (dstBaseY+7)*f.StrideC+dstBaseX+8 > len(f.V) {
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
		for y := 0; y < 4; y++ {
			dstRow := plane[(dstBaseY+by+y)*f.StrideC+dstBaseX+bx:]
			predRow := predicted[(by+y)*8+bx:]
			resRow := residual[blk][y*4:]
			for x := 0; x < 4; x++ {
				v := int(predRow[x]) + int(resRow[x])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				dstRow[x] = uint8(v)
			}
		}
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
	var tmp [256]uint8
	pred.InterPred16x16At(tmp[:], ref.Y, ref.StrideY, srcBaseX, srcBaseY, pred.MotionVector{X: mv.X, Y: mv.Y})
	for y := 0; y < h; y++ {
		copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], tmp[y*16:y*16+w])
	}
}

func (d *Decoder) writeInterResidual(f *frame.Frame, mb *syntax.MBInter, predicted []uint8, mbX, mbY, qp int) {
	if f == nil || mb == nil || len(predicted) < 256 || f.StrideY <= 0 {
		return
	}
	dstBaseX := mbX * 16
	dstBaseY := mbY * 16
	if dstBaseX < 0 || dstBaseY < 0 || dstBaseX+16 > f.Width || dstBaseY+16 > f.Height || (dstBaseY+15)*f.StrideY+dstBaseX+16 > len(f.Y) {
		return
	}
	cbpLuma := mb.CBP & 0xF
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
			for py := 0; py < 8; py++ {
				dstRow := f.Y[(dstY+py)*f.StrideY+dstX:]
				predRow := predicted[(groupY+py)*16+groupX:]
				blockRow := block[py*8:]
				for px := 0; px < 8; px++ {
					v := int(predRow[px]) + int(blockRow[px])
					if v < 0 {
						v = 0
					}
					if v > 255 {
						v = 255
					}
					dstRow[px] = uint8(v)
				}
			}
		}
	} else {
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
			for py := 0; py < 4; py++ {
				dstRow := f.Y[(dstBaseY+by+py)*f.StrideY+dstBaseX+bx:]
				predRow := predicted[(by+py)*16+bx:]
				resRow := residual[blkIdx][py*4:]
				for px := 0; px < 4; px++ {
					v := int(predRow[px]) + int(resRow[px])
					if v < 0 {
						v = 0
					}
					if v > 255 {
						v = 255
					}
					dstRow[px] = uint8(v)
				}
			}
		}
	}
}

func fillBPredBlock(dst []uint8, ref *frame.Frame, srcBaseX, srcBaseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
	if ref == nil || ref.Width <= 0 || ref.Height <= 0 || ref.StrideY <= 0 || ref.Width > ref.StrideY || len(dst) < 256 || !valid16x16Rect(dstX, dstY, w, h) {
		return
	}
	lastPixel := (ref.Height-1)*ref.StrideY + (ref.Width - 1)
	if lastPixel < 0 || lastPixel >= len(ref.Y) {
		return
	}
	// Use bilinear fractional-pixel interpolation (same as P-frame path).
	// InterPred16x16At writes a 16×16 block from the reference plane into dst
	// with quarter-pixel bilinear weighting, handling clipped edges.
	var tmp [256]uint8
	pred.InterPred16x16At(tmp[:], ref.Y, ref.StrideY, srcBaseX, srcBaseY, pred.MotionVector{X: mv.X, Y: mv.Y})
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst[(dstY+y)*16+dstX+x] = tmp[y*16+x]
		}
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
		// Direct B sub-MBs still need full colocated temporal derivation, but the
		// decoder stores the spatial direct fallback in SubMVL* so reconstruction
		// and subsequent B_8x8 MVP diagnostics use the same cache-resolved motion.
		d.fillBPredByUse(dst, fallback, mbX, mbY, dstX, dstY, 8, 8, mb.RefIdxL0[part], mb.RefIdxL1[part], mb.SubMVL0[part*4], mb.SubMVL1[part*4], true, true)
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
	if len(dst) < 256 || !valid16x16Rect(dstX, dstY, w, h) {
		return
	}
	var predL0, predL1 [256]uint8
	if useL0 {
		ref := d.refL0(refIdxL0)
		if ref == nil {
			ref = fallback
		}
		fillBPredBlock(predL0[:], ref, mbX*16+dstX, mbY*16+dstY, dstX, dstY, w, h, mvL0)
	}
	if useL1 {
		ref := d.refL1(refIdxL1)
		if ref == nil {
			ref = fallback
		}
		fillBPredBlock(predL1[:], ref, mbX*16+dstX, mbY*16+dstY, dstX, dstY, w, h, mvL1)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (dstY+y)*16 + dstX + x
			switch {
			case useL0 && useL1:
				dst[idx] = uint8((uint16(predL0[idx]) + uint16(predL1[idx]) + 1) >> 1)
			case useL1:
				dst[idx] = predL1[idx]
			case useL0:
				dst[idx] = predL0[idx]
			}
		}
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
	if os.Getenv("GO264_DIRECT_TRACE") == "" || d == nil || f == nil || mb == nil || (mb.MBType != syntax.BMBTypeDirect16x16 && mb.MBType != syntax.BMBTypeB8x8) {
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
	if os.Getenv("GO264_B_MB_TRACE") == "" || d == nil || f == nil || mb == nil {
		return
	}
	sub0, sub1, sub2, sub3 := directTraceSubTypes(mb)
	smv0, smv1, smv2, smv3 := directTraceSubMVs(mb)
	p0L0, p0L1 := bTracePart0MVs(mb)
	p1L0, p1L1 := bTracePart1MVs(mb)
	ref0p1, ref1p1 := mb.RefIdxL0[0], mb.RefIdxL1[0]
	if cabacBPartsForType(mb.MBType) == 2 {
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
	if dstBaseX < 0 || dstBaseY < 0 || dstBaseX+16 > f.Width || dstBaseY+16 > f.Height {
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

	if os.Getenv("GO264_DIRECT_COL_TRACE") != "" && mb.MBType == syntax.BMBTypeDirect16x16 {
		if colocated := d.refBidiL1(0, f.POC); colocatedDirectUses8x8(colocated, mbX, mbY) {
			for part := 0; part < 4; part++ {
				_ = colocatedDirect8x8Zero(colocated, mbX, mbY, part, f.POC)
			}
		}
	}
	var blended [256]uint8
	if mb.MBType == syntax.BMBTypeB8x8 {
		for part := 0; part < 4; part++ {
			x0 := (part & 1) * 8
			y0 := (part >> 1) * 8
			d.fillBSubPrediction(blended[:], mb, f, mbX, mbY, x0, y0, part)
		}
	} else if bMacroblockPartCount(mb.MBType) == 2 {
		for part := 0; part < 2; part++ {
			x0, y0, w, h := bPartRect(mb.MBType, part)
			d.fillBPartPrediction(blended[:], mb, f, mbX, mbY, x0, y0, w, h, part)
		}
	} else {
		var predL0 [256]uint8
		var predL1 [256]uint8
		fillBPredBlock(predL0[:], refL0, mbX*16, mbY*16, 0, 0, 16, 16, mb.MVL0[0])
		fillBPredBlock(predL1[:], refL1, mbX*16, mbY*16, 0, 0, 16, 16, mb.MVL1[0])
		// Full B_Direct_16x16 still lacks colocated temporal/spatial derivation.
		// Prefer list-0 fallback over zero-MV bi-blend: it matches the dominant
		// reference direction in this stream and avoids averaging in an unrelated
		// future frame until proper direct MV derivation is available.
		useBi := mb.MBType == syntax.BMBTypeBi16x16
		if useBi {
			syntax.BiPredBlend(blended[:], predL0[:], predL1[:], 256)
		} else if syntax.BMBTypeL116x16 == mb.MBType {
			copy(blended[:], predL1[:])
		} else {
			copy(blended[:], predL0[:])
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
	if d == nil || d.DPB == nil {
		return nil
	}
	var frames []*frame.Frame
	for _, fr := range d.DPB.Frames {
		if fr != nil && fr.IsRef && fr.POC < currentPOC {
			frames = append(frames, fr)
		}
	}
	// Sort by descending POC (nearest past first).
	for i := 0; i < len(frames)-1; i++ {
		for j := i + 1; j < len(frames); j++ {
			if frames[j].POC > frames[i].POC {
				frames[i], frames[j] = frames[j], frames[i]
			}
		}
	}
	return frames
}
