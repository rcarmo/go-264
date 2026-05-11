package decode

// decode/reconstruct_inter.go — inter macroblock reconstruction.
// Covers P-skip, P16x16/P16x8/P8x16/P8x8 motion compensation, B-frame blending,
// and chroma inter prediction.

import (
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
	"github.com/rcarmo/go-264/transform"
)

func (d *Decoder) refL0(refIdx int8) *frame.Frame {
	if len(d.DPB.Frames) == 0 {
		return nil
	}
	idx := int(refIdx)
	if idx < 0 || idx >= len(d.DPB.Frames) {
		idx = 0
	}
	return d.DPB.Frames[len(d.DPB.Frames)-1-idx]
}

func (d *Decoder) reconstructMBInter(f *frame.Frame, mb *syntax.MBInter, mbX, mbY, qp int) {
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
		predicted := make([]uint8, 256)
		pred.InterPred16x16At(predicted, ref.Y, ref.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv.X, Y: mv.Y})
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP16x8:
		predicted := make([]uint8, 256)
		tmp := make([]uint8, 256)
		ref0 := d.refL0(mb.RefIdx[0])
		if ref0 == nil {
			ref0 = ref
		}
		mv0 := mb.MV[0]
		pred.InterPred16x16At(tmp, ref0.Y, ref0.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		for y := 0; y < 8; y++ {
			copy(predicted[y*16:y*16+16], tmp[y*16:y*16+16])
		}
		ref1 := d.refL0(mb.RefIdx[1])
		if ref1 == nil {
			ref1 = ref
		}
		mv1 := mb.MV[1]
		pred.InterPred16x16At(tmp, ref1.Y, ref1.StrideY, mbX*16, mbY*16+8, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		for y := 0; y < 8; y++ {
			copy(predicted[(y+8)*16:(y+8)*16+16], tmp[y*16:y*16+16])
		}
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP8x16:
		predicted := make([]uint8, 256)
		tmp := make([]uint8, 256)
		ref0 := d.refL0(mb.RefIdx[0])
		if ref0 == nil {
			ref0 = ref
		}
		mv0 := mb.MV[0]
		pred.InterPred16x16At(tmp, ref0.Y, ref0.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		for y := 0; y < 16; y++ {
			copy(predicted[y*16:y*16+8], tmp[y*16:y*16+8])
		}
		ref1 := d.refL0(mb.RefIdx[1])
		if ref1 == nil {
			ref1 = ref
		}
		mv1 := mb.MV[1]
		pred.InterPred16x16At(tmp, ref1.Y, ref1.StrideY, mbX*16+8, mbY*16, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		for y := 0; y < 16; y++ {
			copy(predicted[y*16+8:y*16+16], tmp[y*16:y*16+8])
		}
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		predicted := make([]uint8, 256)
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
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 8, mb.SubMV[part*4])
			case 1: // P_L0_8x4
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 4, mb.SubMV[part*4])
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX, mbY*16+baseY+4, baseX, baseY+4, 8, 4, mb.SubMV[part*4+1])
			case 2: // P_L0_4x8
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 4, 8, mb.SubMV[part*4])
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX+4, mbY*16+baseY, baseX+4, baseY, 4, 8, mb.SubMV[part*4+1])
			case 3: // P_L0_4x4
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 4, 4, mb.SubMV[part*4])
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX+4, mbY*16+baseY, baseX+4, baseY, 4, 4, mb.SubMV[part*4+1])
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX, mbY*16+baseY+4, baseX, baseY+4, 4, 4, mb.SubMV[part*4+2])
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX+4, mbY*16+baseY+4, baseX+4, baseY+4, 4, 4, mb.SubMV[part*4+3])
			default:
				d.copyInterSubRect(predicted, partRef, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 8, mb.SubMV[part*4])
			}
		}
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
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
	var predU, predV [64]uint8
	mv := mb.MV[0]
	if mb.MBType == syntax.PMBTypeP8x8 || mb.MBType == syntax.PMBTypeP8x8ref0 {
		mv = mb.SubMV[0]
	}
	d.fillChromaInterPred(predU[:], ref.U, ref.StrideC, ref.Width/2, ref.Height/2, mbX*8, mbY*8, mv)
	d.fillChromaInterPred(predV[:], ref.V, ref.StrideC, ref.Width/2, ref.Height/2, mbX*8, mbY*8, mv)
	d.writeChromaInterResidual(f, mb, predU[:], 0, mbX, mbY, qp)
	d.writeChromaInterResidual(f, mb, predV[:], 1, mbX, mbY, qp)
}

func (d *Decoder) fillChromaInterPred(dst []uint8, plane []uint8, stride, width, height, baseX, baseY int, mv syntax.MotionVector) {
	if len(dst) < 64 || len(plane) == 0 || stride <= 0 || width <= 0 || height <= 0 {
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
	chromaQP := frame.ChromaQP(qp, d.chromaQPOffset)
	var dc [4]int16
	for i := 0; i < 4; i++ {
		dc[i] = mb.CoeffsChroma[comp][i][0]
	}
	transform.Hadamard2x2DC(dc[:], chromaQP)
	var residual [4][16]int16
	for blk := 0; blk < 4; blk++ {
		residual[blk] = mb.CoeffsChroma[comp][blk]
		residual[blk][0] = dc[blk]
		transform.Dequant4x4AC(residual[blk][:], chromaQP)
	}
	transform.IDCT4x4Batch(residual[:])
	for blk := 0; blk < 4; blk++ {
		bx, by := (blk&1)*4, (blk>>1)*4
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				v := int(predicted[(by+y)*8+bx+x]) + int(residual[blk][y*4+x])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				cx, cy := mbX*8+bx+x, mbY*8+by+y
				if comp == 0 {
					f.SetPixelU(cx, cy, uint8(v))
				} else {
					f.SetPixelV(cx, cy, uint8(v))
				}
			}
		}
	}
}

func (d *Decoder) copyInterSubRect(dst []uint8, ref *frame.Frame, srcBaseX, srcBaseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
	var tmp [256]uint8
	pred.InterPred16x16At(tmp[:], ref.Y, ref.StrideY, srcBaseX, srcBaseY, pred.MotionVector{X: mv.X, Y: mv.Y})
	for y := 0; y < h; y++ {
		copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], tmp[y*16:y*16+w])
	}
}

func (d *Decoder) writeInterResidual(f *frame.Frame, mb *syntax.MBInter, predicted []uint8, mbX, mbY, qp int) {
	cbpLuma := mb.CBP & 0xF
	if mb.Use8x8Transform {
		for group := 0; group < 4; group++ {
			if cbpLuma&(1<<uint(group)) == 0 {
				continue
			}
			var block [64]int16
			for sub := 0; sub < 4; sub++ {
				for j := 0; j < 16; j++ {
					block[sub*16+j] = mb.Coeffs[group*4+sub][j]
				}
			}
			transform.Dequant8x8(block[:], qp)
			transform.IDCT8x8(block[:])
			groupX := (group % 2) * 8
			groupY := (group / 2) * 8
			for py := 0; py < 8; py++ {
				for px := 0; px < 8; px++ {
					pidx := (groupY+py)*16 + (groupX + px)
					v := int(predicted[pidx]) + int(block[py*8+px])
					if v < 0 {
						v = 0
					}
					if v > 255 {
						v = 255
					}
					f.SetPixelY(mbX*16+groupX+px, mbY*16+groupY+py, uint8(v))
				}
			}
		}
	} else {
		var residual [16][16]int16
		for blkIdx := 0; blkIdx < 16; blkIdx++ {
			group := blkIdx / 4
			if cbpLuma&(1<<uint(group)) != 0 {
				residual[blkIdx] = mb.Coeffs[blkIdx]
				transform.Dequant4x4(residual[blkIdx][:], qp)
			}
		}
		transform.IDCT4x4Batch(residual[:])
		for blkIdx := 0; blkIdx < 16; blkIdx++ {
			bx := blk4x4X[blkIdx]
			by := blk4x4Y[blkIdx]
			for py := 0; py < 4; py++ {
				for px := 0; px < 4; px++ {
					pidx := (by+py)*16 + (bx + px)
					v := int(predicted[pidx]) + int(residual[blkIdx][py*4+px])
					if v < 0 {
						v = 0
					}
					if v > 255 {
						v = 255
					}
					f.SetPixelY(mbX*16+bx+px, mbY*16+by+py, uint8(v))
				}
			}
		}
	}
}

func (d *Decoder) reconstructMBBidi(f *frame.Frame, mb *syntax.MBBidi, mbX, mbY, qp int) {
	var refL0, refL1 *frame.Frame
	if len(d.DPB.Frames) > 0 {
		refL0 = d.DPB.Frames[len(d.DPB.Frames)-1]
	}
	if len(d.DPB.Frames) > 1 {
		refL1 = d.DPB.Frames[len(d.DPB.Frames)-2]
	}
	if refL0 == nil {
		refL0 = f
	}
	if refL1 == nil {
		refL1 = refL0
	}

	predL0 := make([]uint8, 256)
	predL1 := make([]uint8, 256)
	mvL0 := mb.MVL0[0]
	mvL1 := mb.MVL1[0]

	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			sx0 := clampInt(mbX*16+x+int(mvL0.X>>2), 0, refL0.Width-1)
			sy0 := clampInt(mbY*16+y+int(mvL0.Y>>2), 0, refL0.Height-1)
			predL0[y*16+x] = refL0.PixelY(sx0, sy0)
			sx1 := clampInt(mbX*16+x+int(mvL1.X>>2), 0, refL1.Width-1)
			sy1 := clampInt(mbY*16+y+int(mvL1.Y>>2), 0, refL1.Height-1)
			predL1[y*16+x] = refL1.PixelY(sx1, sy1)
		}
	}

	blended := make([]uint8, 256)
	useBi := mb.MBType == syntax.BMBTypeBi16x16 || mb.MBType == syntax.BMBTypeDirect16x16
	if useBi {
		syntax.BiPredBlend(blended, predL0, predL1, 256)
	} else if syntax.BMBTypeL116x16 == mb.MBType {
		copy(blended, predL1)
	} else {
		copy(blended, predL0)
	}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			f.SetPixelY(mbX*16+x, mbY*16+y, blended[y*16+x])
		}
	}
}
