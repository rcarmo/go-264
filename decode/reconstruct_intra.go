package decode

// decode/reconstruct_intra.go — intra macroblock reconstruction.
// Covers I_NxN (4x4 and 8x8), I_16x16, and chroma intra prediction.

import (
	"fmt"
	"os"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
	"github.com/rcarmo/go-264/transform"
)

func (d *Decoder) reconstructMB(f *frame.Frame, mb *syntax.MBIntra, mbX, mbY int, qp int, sps *nal.SPS) {
	if f == nil || mb == nil {
		return
	}
	if mb.MBType == syntax.MBTypeIPCM {
		d.reconstructIPCM(f, mb, mbX, mbY)
		return
	}
	if mb.MBType >= 1 && mb.MBType <= 24 {
		d.reconstruct16x16(f, mb, mbX, mbY, qp)
	} else if mb.MBType == 0 {
		if mb.Use8x8Transform {
			d.reconstruct8x8(f, mb, mbX, mbY, qp)
		} else {
			d.reconstruct4x4(f, mb, mbX, mbY, qp)
		}
	}
	d.reconstructChromaIntra(f, mb, mbX, mbY, qp)
}

func (d *Decoder) reconstructIPCM(f *frame.Frame, mb *syntax.MBIntra, mbX, mbY int) {
	if f == nil || mb == nil || f.StrideY <= 0 || f.StrideC <= 0 {
		return
	}
	baseX, baseY := mbX*16, mbY*16
	baseCX, baseCY := mbX*8, mbY*8
	if baseX < 0 || baseY < 0 || baseX+16 > f.Width || baseY+16 > f.Height || (baseY+15)*f.StrideY+baseX+16 > len(f.Y) || baseCX < 0 || baseCY < 0 || baseCX+8 > f.Width/2 || baseCY+8 > f.Height/2 || (baseCY+7)*f.StrideC+baseCX+8 > len(f.U) || (baseCY+7)*f.StrideC+baseCX+8 > len(f.V) {
		return
	}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			f.SetPixelY(baseX+x, baseY+y, mb.PCMY[y*16+x])
		}
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			idx := y*8 + x
			f.SetPixelU(baseCX+x, baseCY+y, mb.PCMCb[idx])
			f.SetPixelV(baseCX+x, baseCY+y, mb.PCMCr[idx])
		}
	}
}

func intraMBInFrame(f *frame.Frame, mbX, mbY int) bool {
	if f == nil || f.StrideY <= 0 {
		return false
	}
	baseX, baseY := mbX*16, mbY*16
	return baseX >= 0 && baseY >= 0 && baseX+16 <= f.Width && baseY+16 <= f.Height && (baseY+15)*f.StrideY+baseX+16 <= len(f.Y)
}

func (d *Decoder) reconstruct16x16(f *frame.Frame, mb *syntax.MBIntra, mbX, mbY, qp int) {
	if mb == nil || !intraMBInFrame(f, mbX, mbY) {
		return
	}
	mode := int(mb.Intra16x16PredMode)
	var top [16]uint8
	var left [16]uint8
	topLeft := uint8(128)
	if mbY > 0 {
		for x := 0; x < 16; x++ {
			top[x] = f.PixelY(mbX*16+x, mbY*16-1)
		}
	} else {
		for i := range top {
			top[i] = 128
		}
	}
	if mbX > 0 {
		for y := 0; y < 16; y++ {
			left[y] = f.PixelY(mbX*16-1, mbY*16+y)
		}
	} else {
		for i := range left {
			left[i] = 128
		}
	}
	if mbX > 0 && mbY > 0 {
		topLeft = f.PixelY(mbX*16-1, mbY*16-1)
	}

	var predicted [256]uint8
	if mode == pred.Intra16x16DC {
		var dc uint8
		if mbX > 0 && mbY > 0 {
			sum := 0
			for i := 0; i < 16; i++ {
				sum += int(top[i]) + int(left[i])
			}
			dc = uint8((sum + 16) >> 5)
		} else if mbY > 0 {
			sum := 0
			for i := 0; i < 16; i++ {
				sum += int(top[i])
			}
			dc = uint8((sum + 8) >> 4)
		} else if mbX > 0 {
			sum := 0
			for i := 0; i < 16; i++ {
				sum += int(left[i])
			}
			dc = uint8((sum + 8) >> 4)
		} else {
			dc = 128
		}
		for i := range predicted[:256] {
			predicted[i] = dc
		}
	} else {
		pred.PredIntra16x16(predicted[:], mode, top[:], left[:], topLeft)
	}

	// Hadamard DC transform. Intra16x16 DC is a 4x4 matrix in raster position
	// order, while mb.Coeffs is indexed by H.264 luma4x4BlkIdx.
	var dcBlock [16]int16
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		pos := (blk4x4Y[blkIdx]/4)*4 + (blk4x4X[blkIdx] / 4)
		dcBlock[pos] = mb.Coeffs[blkIdx][0]
	}
	transform.Hadamard4x4DC(dcBlock[:], qp)

	cbpLuma := mb.CodedBlockPattern & 0xF
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		bx := blk4x4X[blkIdx]
		by := blk4x4Y[blkIdx]
		pos := (by/4)*4 + (bx / 4)
		var block [16]int16
		block[0] = dcBlock[pos]
		if cbpLuma != 0 {
			for j := 1; j < 16; j++ {
				block[j] = mb.Coeffs[blkIdx][j]
			}
			qpDiv6 := uint(qp / 6)
			qpMod6 := qp % 6
			for j := 1; j < 16; j++ {
				if block[j] != 0 {
					v := int32(transform.DequantVTable()[qpMod6][transform.PosToVTable()[j]])
					block[j] = int16(int32(block[j]) * v << qpDiv6)
				}
			}
		}
		transform.IDCT4x4(block[:])
		for py := 0; py < 4; py++ {
			for px := 0; px < 4; px++ {
				v := int(predicted[(by+py)*16+(bx+px)]) + int(block[py*4+px])
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

func (d *Decoder) reconstruct4x4(f *frame.Frame, mb *syntax.MBIntra, mbX, mbY, qp int) {
	if mb == nil || !intraMBInFrame(f, mbX, mbY) {
		return
	}
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		bx := blk4x4X[blkIdx]
		by := blk4x4Y[blkIdx]
		var top [4]uint8
		var topRight [4]uint8
		var left [4]uint8
		topLeft := uint8(128)

		x0 := mbX*16 + bx
		y0 := mbY*16 + by

		for i := 0; i < 4; i++ {
			if y0 > 0 {
				top[i] = f.PixelY(x0+i, y0-1)
			} else {
				top[i] = 128
			}
			if x0 > 0 {
				left[i] = f.PixelY(x0-1, y0+i)
			} else {
				left[i] = 128
			}
		}
		for i := 0; i < 4; i++ {
			// H.264 §8.3.1.1: top-right samples are NOT available for
			// luma4x4BlkIdx 3, 7, 11, 15 (rightmost-top block of each 8×8 group).
			// Those positions decode later in the luma4x4BlkIdx scan order, so
			// their frame pixels are still zero-initialised at this point.
			topRightAvail := y0 > 0 && x0+4+i < f.Width &&
				blkIdx != 3 && blkIdx != 7 && blkIdx != 11 && blkIdx != 15
			if topRightAvail {
				topRight[i] = f.PixelY(x0+4+i, y0-1)
			} else {
				topRight[i] = top[3]
			}
		}
		if x0 > 0 && y0 > 0 {
			topLeft = f.PixelY(x0-1, y0-1)
		}

		// Compute predicted mode from neighbours (H.264 §8.3.1.1)
		blkX := mbX*4 + bx/4
		blkY := mbY*4 + by/4
		modeA := int8(2)
		modeB := int8(2)
		hasA := blkX > 0
		hasB := blkY > 0
		if hasA {
			modeA = d.intraModes[blkY*d.mbW*4+(blkX-1)]
		}
		if hasB {
			modeB = d.intraModes[(blkY-1)*d.mbW*4+blkX]
		}
		predMode := int8(2)
		if hasA && hasB {
			predMode = modeA
			if modeB < predMode {
				predMode = modeB
			}
		}

		mode := int(predMode)
		rawMode := mb.IntraPredMode[blkIdx]
		if rawMode == -1 {
			mode = int(predMode)
		} else if int(rawMode) < int(predMode) {
			mode = int(rawMode)
		} else {
			mode = int(rawMode) + 1
		}
		if mode > 8 {
			mode = 2
		}
		if blkY < d.mbH*4 && blkX < d.mbW*4 {
			d.intraModes[blkY*d.mbW*4+blkX] = int8(mode)
		}

		var predicted [16]uint8
		if mode == pred.Intra4x4DC {
			topAvail := y0 > 0
			leftAvail := x0 > 0
			var dc uint8
			if topAvail && leftAvail {
				sum := 0
				for i := 0; i < 4; i++ {
					sum += int(top[i]) + int(left[i])
				}
				dc = uint8((sum + 4) >> 3)
			} else if topAvail {
				sum := 0
				for i := 0; i < 4; i++ {
					sum += int(top[i])
				}
				dc = uint8((sum + 2) >> 2)
			} else if leftAvail {
				sum := 0
				for i := 0; i < 4; i++ {
					sum += int(left[i])
				}
				dc = uint8((sum + 2) >> 2)
			} else {
				dc = 128
			}
			for i := range predicted {
				predicted[i] = dc
			}
		} else {
			pred.PredIntra4x4(predicted[:], mode, top[:], topRight[:], left[:], topLeft)
		}

		block := mb.Coeffs[blkIdx]
		hasResidual := (mb.CodedBlockPattern & (1 << uint(blkIdx/4))) != 0
		if hasResidual {
			transform.Dequant4x4(block[:], qp)
			transform.IDCT4x4(block[:])
		}
		for py := 0; py < 4; py++ {
			for px := 0; px < 4; px++ {
				v := int(predicted[py*4+px]) + int(block[py*4+px])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				f.SetPixelY(x0+px, y0+py, uint8(v))
			}
		}
	}
}

// reconstruct8x8 handles I_NxN macroblocks using 8×8 DCT (High profile
// transform_size_8x8_flag=1).
func (d *Decoder) reconstruct8x8(f *frame.Frame, mb *syntax.MBIntra, mbX, mbY, qp int) {
	if mb == nil || !intraMBInFrame(f, mbX, mbY) {
		return
	}
	blk8x8Offsets := [4][2]int{{0, 0}, {8, 0}, {0, 8}, {8, 8}}
	for b8 := 0; b8 < 4; b8++ {
		bx := blk8x8Offsets[b8][0]
		by := blk8x8Offsets[b8][1]
		x0 := mbX*16 + bx
		y0 := mbY*16 + by

		var top [16]uint8
		var left [8]uint8
		topLeft := uint8(128)
		for i := 0; i < 8; i++ {
			if y0 > 0 {
				top[i] = f.PixelY(x0+i, y0-1)
			} else {
				top[i] = 128
			}
			if x0 > 0 {
				left[i] = f.PixelY(x0-1, y0+i)
			} else {
				left[i] = 128
			}
		}
		for i := 8; i < 16; i++ {
			if y0 > 0 && x0+i < f.Width {
				top[i] = f.PixelY(x0+i, y0-1)
			} else {
				top[i] = top[7]
			}
		}
		if x0 > 0 && y0 > 0 {
			topLeft = f.PixelY(x0-1, y0-1)
		}

		mode := int(mb.I8x8PredMode[b8])
		if mode < 0 || mode > 8 {
			mode = 2
		}
		var predicted [64]uint8
		if mode == pred.Intra4x4Horizontal && y0 == 0 && x0 > 0 {
			// FFmpeg's pred8x8l table can reconstruct this top-edge I8x8 case
			// with LEFT_DC_PRED even though the stored syntax mode is horizontal.
			// Keep the CABAC prediction-mode state unchanged, but mirror the
			// reconstruction predictor selected by FFmpeg's checked mode cache.
			filteredLeft := func() [8]int {
				var l [8]int
				l[0] = (int(left[0]) + 2*int(left[0]) + int(left[1]) + 2) >> 2
				for i := 1; i <= 6; i++ {
					l[i] = (int(left[i-1]) + 2*int(left[i]) + int(left[i+1]) + 2) >> 2
				}
				l[7] = (int(left[6]) + 3*int(left[7]) + 2) >> 2
				return l
			}
			l := filteredLeft()
			sum := 0
			for i := 0; i < 8; i++ {
				sum += l[i]
			}
			dc := uint8((sum + 4) >> 3)
			for i := range predicted {
				predicted[i] = dc
			}
		} else if mode == pred.Intra4x4DC {
			topAvail := y0 > 0
			leftAvail := x0 > 0
			hasTopLeft := x0 > 0 && y0 > 0
			hasTopRight := y0 > 0 && x0+8 < f.Width
			filteredTop := func() [8]int {
				var t [8]int
				leftRef := int(top[0])
				if hasTopLeft {
					leftRef = int(topLeft)
				}
				t[0] = (leftRef + 2*int(top[0]) + int(top[1]) + 2) >> 2
				for i := 1; i <= 6; i++ {
					t[i] = (int(top[i-1]) + 2*int(top[i]) + int(top[i+1]) + 2) >> 2
				}
				rightRef := int(top[7])
				if hasTopRight {
					rightRef = int(top[8])
				}
				t[7] = (rightRef + 2*int(top[7]) + int(top[6]) + 2) >> 2
				return t
			}
			filteredLeft := func() [8]int {
				var l [8]int
				topRef := int(left[0])
				if hasTopLeft {
					topRef = int(topLeft)
				}
				l[0] = (topRef + 2*int(left[0]) + int(left[1]) + 2) >> 2
				for i := 1; i <= 6; i++ {
					l[i] = (int(left[i-1]) + 2*int(left[i]) + int(left[i+1]) + 2) >> 2
				}
				l[7] = (int(left[6]) + 3*int(left[7]) + 2) >> 2
				return l
			}
			var dc uint8
			if topAvail && leftAvail {
				t, l := filteredTop(), filteredLeft()
				sum := 0
				for i := 0; i < 8; i++ {
					sum += t[i] + l[i]
				}
				dc = uint8((sum + 8) >> 4)
			} else if topAvail {
				t := filteredTop()
				sum := 0
				for i := 0; i < 8; i++ {
					sum += t[i]
				}
				dc = uint8((sum + 4) >> 3)
			} else if leftAvail {
				l := filteredLeft()
				sum := 0
				for i := 0; i < 8; i++ {
					sum += l[i]
				}
				dc = uint8((sum + 4) >> 3)
			} else {
				dc = 128
			}
			for i := range predicted {
				predicted[i] = dc
			}
		} else {
			pred.PredIntra8x8(predicted[:], mode, top[:], left[:], topLeft)
		}

		block := joinLuma8x8Residual(mb.Coeffs, b8)
		transform.Dequant8x8(block[:], qp)
		transform.IDCT8x8(block[:])

		traceRecon := os.Getenv("GO264_RECON_TRACE") != ""
		predSum, resSum, outSum := 0, 0, 0
		if traceRecon {
			for i := 0; i < 64; i++ {
				predSum += int(predicted[i])
				resSum += int(block[i])
			}
		}
		for py := 0; py < 8; py++ {
			for px := 0; px < 8; px++ {
				v := int(predicted[py*8+px]) + int(block[py*8+px])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				f.SetPixelY(x0+px, y0+py, uint8(v))
				if traceRecon {
					outSum += v
				}
			}
		}
		if traceRecon {
			fmt.Fprintf(os.Stderr, "GORECON part=i8x8 mb=%04d b8=%d x=%d y=%d mode=%d qp=%d predsum=%d ressum=%d outsum=%d first_pred=%d first_res=%d tc=%d\n", mbY*d.mbW+mbX, b8, mbX, mbY, mode, qp, predSum, resSum, outSum, predicted[0], block[0], mb.TotalCoeff[b8*4])
		}
	}
}

func (d *Decoder) reconstructChromaIntra(f *frame.Frame, mb *syntax.MBIntra, mbX, mbY, qp int) {
	if f == nil || mb == nil || f.StrideC <= 0 {
		return
	}
	dstBaseX := mbX * 8
	dstBaseY := mbY * 8
	if dstBaseX < 0 || dstBaseY < 0 || dstBaseX+8 > f.Width/2 || dstBaseY+8 > f.Height/2 || (dstBaseY+7)*f.StrideC+dstBaseX+8 > len(f.U) || (dstBaseY+7)*f.StrideC+dstBaseX+8 > len(f.V) {
		return
	}
	chromaQP := frame.ChromaQP(qp, d.chromaQPOffset)
	traceRecon := os.Getenv("GO264_RECON_TRACE") != ""
	for comp := 0; comp < 2; comp++ {
		predicted := d.predictChroma8x8(f, comp, mbX, mbY, int(mb.ChromaPredMode))
		predSum := 0
		if traceRecon {
			for i := 0; i < 64; i++ {
				predSum += int(predicted[i])
			}
		}
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
		resSum, outSum := 0, 0
		var blockOutSum [4]int
		var blockResSum [4]int
		if traceRecon {
			for blk := 0; blk < 4; blk++ {
				for i := 0; i < 16; i++ {
					v := int(residual[blk][i])
					resSum += v
					blockResSum[blk] += v
				}
			}
		}
		for blk := 0; blk < 4; blk++ {
			bx := (blk & 1) * 4
			by := (blk >> 1) * 4
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
					if traceRecon {
						outSum += v
						blockOutSum[blk] += v
					}
				}
			}
		}
		if traceRecon {
			fmt.Fprintf(os.Stderr, "GORECON part=chroma mb=%04d comp=%d x=%d y=%d mode=%d qp=%d predsum=%d ressum=%d outsum=%d first_pred=%d first_res=%d ctc=%v block_res=%v block_out=%v\n", mbY*d.mbW+mbX, comp, mbX, mbY, mb.ChromaPredMode, chromaQP, predSum, resSum, outSum, predicted[0], residual[0][0], mb.ChromaTotalCoeff[comp], blockResSum, blockOutSum)
		}
	}
}

func chromaPlanesCoverFrame(f *frame.Frame) bool {
	if f == nil || f.StrideC <= 0 || f.Width < 2 || f.Height < 2 {
		return false
	}
	last := (f.Height/2-1)*f.StrideC + (f.Width/2 - 1)
	return last >= 0 && last < len(f.U) && last < len(f.V)
}

func clip8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func (d *Decoder) predictChroma8x8(f *frame.Frame, comp int, mbX, mbY, mode int) [64]uint8 {
	var out [64]uint8
	if f == nil || f.StrideC <= 0 || f.Width < 2 || f.Height < 2 || f.Width/2 > f.StrideC || mbX < 0 || mbY < 0 || mbX*8+8 > f.Width/2 || mbY*8+8 > f.Height/2 || !chromaPlanesCoverFrame(f) {
		for i := range out {
			out[i] = 128
		}
		return out
	}
	get := func(x, y int) uint8 {
		if comp == 0 {
			return f.PixelU(x, y)
		}
		return f.PixelV(x, y)
	}
	var top [8]uint8
	var left [8]uint8
	topLeft := uint8(128)
	if mbY > 0 {
		for i := 0; i < 8; i++ {
			top[i] = get(mbX*8+i, mbY*8-1)
		}
	} else {
		for i := range top {
			top[i] = 128
		}
	}
	if mbX > 0 {
		for i := 0; i < 8; i++ {
			left[i] = get(mbX*8-1, mbY*8+i)
		}
	} else {
		for i := range left {
			left[i] = 128
		}
	}
	if mbX > 0 && mbY > 0 {
		topLeft = get(mbX*8-1, mbY*8-1)
	}
	switch mode {
	case 1: // horizontal
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				out[y*8+x] = left[y]
			}
		}
	case 2: // vertical
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				out[y*8+x] = top[x]
			}
		}
	case 3: // plane
		if mbX == 0 || mbY == 0 {
			for i := range out {
				out[i] = 128
			}
			return out
		}
		h := 0
		v := 0
		for i := 0; i < 4; i++ {
			w := i + 1
			leftRef := int(topLeft)
			topRef := int(topLeft)
			if i < 3 {
				leftRef = int(top[2-i])
				topRef = int(left[2-i])
			}
			h += w * (int(top[4+i]) - leftRef)
			v += w * (int(left[4+i]) - topRef)
		}
		a := 16 * (int(left[7]) + int(top[7]))
		b := (17*h + 16) >> 5
		c := (17*v + 16) >> 5
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				out[y*8+x] = clip8((a + b*(x-3) + c*(y-3) + 16) >> 5)
			}
		}
	default: // DC
		if mbX > 0 && mbY > 0 {
			leftTop, leftBottom, topTopLeft, topRight := 0, 0, 0, 0
			for i := 0; i < 4; i++ {
				leftTop += int(left[i])
				leftBottom += int(left[i+4])
				topTopLeft += int(top[i])
				topRight += int(top[i+4])
			}
			dc0 := uint8((leftTop + topTopLeft + 4) >> 3)
			dc1 := uint8((topRight + 2) >> 2)
			dc2 := uint8((leftBottom + 2) >> 2)
			dc3 := uint8((topRight + leftBottom + 4) >> 3)
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					switch {
					case y < 4 && x < 4:
						out[y*8+x] = dc0
					case y < 4:
						out[y*8+x] = dc1
					case x < 4:
						out[y*8+x] = dc2
					default:
						out[y*8+x] = dc3
					}
				}
			}
		} else if mbY > 0 {
			topLeft, topRight := 0, 0
			for i := 0; i < 4; i++ {
				topLeft += int(top[i])
				topRight += int(top[i+4])
			}
			dc0 := uint8((topLeft + 2) >> 2)
			dc1 := uint8((topRight + 2) >> 2)
			for y := 0; y < 8; y++ {
				for x := 0; x < 4; x++ {
					out[y*8+x] = dc0
					out[y*8+x+4] = dc1
				}
			}
		} else if mbX > 0 {
			leftTop, leftBottom := 0, 0
			for i := 0; i < 4; i++ {
				leftTop += int(left[i])
				leftBottom += int(left[i+4])
			}
			dc0 := uint8((leftTop + 2) >> 2)
			dc2 := uint8((leftBottom + 2) >> 2)
			for y := 0; y < 4; y++ {
				for x := 0; x < 8; x++ {
					out[y*8+x] = dc0
					out[(y+4)*8+x] = dc2
				}
			}
		} else {
			for i := range out {
				out[i] = 128
			}
		}
	}
	return out
}
