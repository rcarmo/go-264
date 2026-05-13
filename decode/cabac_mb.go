package decode

// decode/cabac_mb.go — CABAC macroblock decode for P-slice inter and I-slice
// intra macroblocks. Calls syntax.DecodeCABACCBP/DQP/Ref/MVD for pure syntax;
// residual coefficients are decoded via cabac.CABACDecoder.DecodeCABACResidual.

import (
	cabac "github.com/rcarmo/go-264/entropy/cabac"
	"github.com/rcarmo/go-264/syntax"
)

// enableCABACI8x8Transform gates CABAC intra 8x8 transform decoding until the
// remaining neighbour-mode inference issue is fixed. Consuming the flag today
// makes the current CABAC fixtures worse, so the decoder deliberately keeps the
// legacy I4x4 reconstruction path rather than hiding a known correctness gap.
const enableCABACI8x8Transform = false

// cabacMinMacroblockContexts covers the highest macroblock-level context this
// file may index directly (transform_size_8x8_flag at ctxIdx 399+n). Residual
// decoders perform their own stricter table-size checks.
const cabacMinMacroblockContexts = 402

// decodeCABACPInterMB decodes one CABAC-coded P-slice macroblock.
// Returns (inter, nil, true) for P-skip, (nil, intra, false) for intra-in-P.
func decodeCABACPInterMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx, numRefFrames uint32, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int, leftCBP, topCBP uint32, leftNonSkip, topNonSkip bool, refCtxs [4]int, mvd4 []syntax.MotionVector, stride4, mbX, mbY int, transform8x8Mode bool, transform8x8Ctx int, leftMBType, topMBType uint32, leftChromaPred, topChromaPred int8, leftEdge8x8, topEdge8x8 [2]int8) (*syntax.MBInter, *syntax.MBIntra, bool) {
	mb := &syntax.MBInter{MBType: syntax.PMBTypeP16x16}
	if dec == nil || len(models) < cabacMinMacroblockContexts {
		return mb, nil, true
	}
	// P-slice mb_skip_flag uses ctxIdx 11 plus availability of non-skipped left/top neighbours.
	// Using ctx 11 unconditionally desynchronizes CABAC state after the first neighbour-dependent MB.
	skipCtx := 11
	if leftNonSkip {
		skipCtx++
	}
	if topNonSkip {
		skipCtx++
	}
	if dec.DecodeBin(&models[skipCtx]) == 1 {
		return mb, nil, true
	}
	// mb_type binarization (FFmpeg h264_cabac.c decode_cabac_mb_type P-slice path)
	if dec.DecodeBin(&models[14]) == 0 {
		if dec.DecodeBin(&models[15]) == 0 {
			mb.MBType = 3 * dec.DecodeBin(&models[16]) // P16x16 or P8x8
		} else {
			mb.MBType = 2 - dec.DecodeBin(&models[17]) // P8x16 or P16x8
		}
	} else {
		// FFmpeg h264_cabac.c decodes intra-in-P via decode_cabac_intra_mb_type(ctx_base=17, intra_slice=0).
		intra := decodeCABACIntraMBWithParams(dec, models, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftMBType, topMBType, leftChromaPred, topChromaPred, transform8x8Mode, transform8x8Ctx, leftEdge8x8, topEdge8x8, 17, false)
		return nil, intra, false
	}
	parts := 1
	switch mb.MBType {
	case syntax.PMBTypeP16x8, syntax.PMBTypeP8x16:
		parts = 2
	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		parts = 4
		for i := 0; i < 4; i++ {
			mb.SubMBType[i] = decodeCABACPSubMBType(dec, models)
		}
	}
	if numRefFrames > 1 && mb.MBType != syntax.PMBTypeP8x8ref0 {
		for i := 0; i < parts; i++ {
			ctxSlot := i
			if mb.MBType == syntax.PMBTypeP16x8 && i == 1 {
				ctxSlot = 2 // second 16x8 partition starts at the bottom-left 8x8 origin
			}
			mb.RefIdx[i] = int8(syntax.DecodeCABACRef(dec, models, refCtxs[ctxSlot]))
		}
	}
	x4, y4 := mbX*4, mbY*4
	if mb.MBType == syntax.PMBTypeP8x8 || mb.MBType == syntax.PMBTypeP8x8ref0 {
		for i := 0; i < 4; i++ {
			baseX := x4 + (i&1)*2
			baseY := y4 + (i>>1)*2
			switch mb.SubMBType[i] {
			case 1: // P_L0_8x4
				for j := 0; j < 2; j++ {
					mb.SubMV[i*4+j] = decodeCABACMVDPair(dec, models, mvd4, stride4, baseX, baseY+j, 2, 1)
				}
			case 2: // P_L0_4x8
				for j := 0; j < 2; j++ {
					mb.SubMV[i*4+j] = decodeCABACMVDPair(dec, models, mvd4, stride4, baseX+j, baseY, 1, 2)
				}
			case 3: // P_L0_4x4
				for j := 0; j < 4; j++ {
					mb.SubMV[i*4+j] = decodeCABACMVDPair(dec, models, mvd4, stride4, baseX+(j&1), baseY+(j>>1), 1, 1)
				}
			default: // P_L0_8x8
				mb.SubMV[i*4] = decodeCABACMVDPair(dec, models, mvd4, stride4, baseX, baseY, 2, 2)
			}
		}
		mb.DecodedMVDX = mb.SubMV[0].X
		mb.DecodedMVDY = mb.SubMV[0].Y
	} else {
		for i := 0; i < parts; i++ {
			switch mb.MBType {
			case syntax.PMBTypeP16x8:
				mb.MV[i] = decodeCABACMVDPair(dec, models, mvd4, stride4, x4, y4+i*2, 4, 2)
			case syntax.PMBTypeP8x16:
				mb.MV[i] = decodeCABACMVDPair(dec, models, mvd4, stride4, x4+i*2, y4, 2, 4)
			default:
				mb.MV[i] = decodeCABACMVDPair(dec, models, mvd4, stride4, x4, y4, 4, 4)
			}
		}
		mb.DecodedMVDX = mb.MV[0].X
		mb.DecodedMVDY = mb.MV[0].Y
	}
	mb.CBP = syntax.DecodeCABACCBP(dec, models, leftCBP, topCBP)
	if mb.CBP != 0 {
		mb.QPDelta = int32(syntax.DecodeCABACDQP(dec, models, 0))
		use8x8Residual := false
		if !cabacInter8x8TransformAllowed(mb) {
			transform8x8Mode = false
		}
		if transform8x8Mode && mb.CBP&0xF != 0 {
			if decodeCABACTransform8x8Flag(dec, models, transform8x8Ctx) {
				use8x8Residual = true
				mb.Use8x8Transform = true
			}
		}
		var nzMB [16]int
		if use8x8Residual {
			for group := 0; group < 4; group++ {
				if mb.CBP&(1<<uint(group)) != 0 {
					var buf [64]int16
					tc := dec.DecodeCABACResidual(models, 5, 64, buf[:], 0, 0)
					splitLuma8x8Residual(&mb.Coeffs, group, buf)
					for sub := 0; sub < 4; sub++ {
						blkIdx := group*4 + sub
						nzMB[blkIdx] = tc
						mb.TotalCoeff[blkIdx] = tc
					}
				}
			}
		} else {
			for group := 0; group < 4; group++ {
				if mb.CBP&(1<<uint(group)) != 0 {
					for sub := 0; sub < 4; sub++ {
						blkIdx := group*4 + sub
						nza, nzb := nzCBFCtxLuma(blkIdx, &nzMB, leftNZ, topNZ)
						var buf [16]int16
						tc := dec.DecodeCABACResidual(models, 2, 16, buf[:], nza, nzb)
						mb.TotalCoeff[blkIdx] = tc
						nzMB[blkIdx] = tc
						mb.Coeffs[blkIdx] = buf
					}
				}
			}
		}
		chromaCBP := (mb.CBP >> 4) & 0x3
		var nzMBChroma [2][4]int
		if chromaCBP > 0 {
			for comp := 0; comp < 2; comp++ {
				var dc [4]int16
				dec.DecodeCABACResidual(models, 3, 4, dc[:], 0, 0)
				storeCABACChromaDC(mb, comp, dc)
			}
		}
		if chromaCBP > 1 {
			for comp := 0; comp < 2; comp++ {
				for blk := 0; blk < 4; blk++ {
					nza, nzb := nzCBFCtxChroma(comp, blk, &nzMBChroma, leftChromaNZ, topChromaNZ)
					var ac [16]int16
					tc := dec.DecodeCABACResidual(models, 4, 15, ac[:], nza, nzb)
					mb.ChromaTotalCoeff[comp][blk] = tc
					nzMBChroma[comp][blk] = tc
					storeCABACChromaAC(mb, comp, blk, ac)
				}
			}
		}
	}
	return mb, nil, false
}

func decodeCABACMVDPair(dec *cabac.CABACDecoder, models []cabac.CABACCtx, mvd4 []syntax.MotionVector, stride4, x4, y4, w4, h4 int) syntax.MotionVector {
	amvdX := cabacMVDAMVD(mvd4, stride4, x4, y4, 0)
	mdx := syntax.DecodeCABACMVD(dec, models, 40, amvdX)
	amvdY := cabacMVDAMVD(mvd4, stride4, x4, y4, 1)
	mdy := syntax.DecodeCABACMVD(dec, models, 47, amvdY)
	mvd := syntax.MotionVector{X: mdx, Y: mdy}
	fillMVD4(mvd4, stride4, x4, y4, w4, h4, mvd)
	return mvd
}

func decodeCABACPSubMBType(dec *cabac.CABACDecoder, models []cabac.CABACCtx) uint32 {
	if dec == nil || len(models) <= 23 {
		return 0
	}
	if dec.DecodeBin(&models[21]) == 1 {
		return 0 // P_L0_8x8
	}
	if dec.DecodeBin(&models[22]) == 0 {
		return 1 // P_L0_8x4
	}
	if dec.DecodeBin(&models[23]) == 1 {
		return 2 // P_L0_4x8
	}
	return 3 // P_L0_4x4
}

func cabacChromaPredModeCtx(leftChromaPred, topChromaPred int8) int {
	ctx := 0
	if leftChromaPred != 0 {
		ctx++
	}
	if topChromaPred != 0 {
		ctx++
	}
	return ctx
}

func cabacInter8x8TransformAllowed(mb *syntax.MBInter) bool {
	if mb == nil {
		return false
	}
	if mb.MBType != syntax.PMBTypeP8x8 && mb.MBType != syntax.PMBTypeP8x8ref0 {
		return true
	}
	// FFmpeg get_dct8x8_allowed() keeps transform_size_8x8_flag present for
	// P_8x8 only when every sub_mb_type is the full 8x8 partition. Any 8x4,
	// 4x8, or 4x4 sub-partition disables the flag; consuming it would desync CABAC.
	for _, subType := range mb.SubMBType {
		if subType != 0 {
			return false
		}
	}
	return true
}

func decodeCABACTransform8x8Flag(dec *cabac.CABACDecoder, models []cabac.CABACCtx, ctx int) bool {
	idx := 399 + cabacTransform8x8Ctx(ctx)
	return dec != nil && idx >= 0 && idx < len(models) && dec.DecodeBin(&models[idx]) == 1
}

func cabacTransform8x8Ctx(ctx int) int {
	if ctx < 0 {
		return 0
	}
	if ctx > 2 {
		return 2
	}
	return ctx
}

func splitLuma8x8Residual(dst *[16][16]int16, group int, src [64]int16) {
	if dst == nil || group < 0 || group >= 4 {
		return
	}
	for sub := 0; sub < 4; sub++ {
		blkIdx := group*4 + sub
		baseX := (sub & 1) * 4
		baseY := (sub >> 1) * 4
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				dst[blkIdx][y*4+x] = src[(baseY+y)*8+baseX+x]
			}
		}
	}
}

func joinLuma8x8Residual(src [16][16]int16, group int) [64]int16 {
	var out [64]int16
	if group < 0 || group >= 4 {
		return out
	}
	for sub := 0; sub < 4; sub++ {
		blkIdx := group*4 + sub
		baseX := (sub & 1) * 4
		baseY := (sub >> 1) * 4
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				out[(baseY+y)*8+baseX+x] = src[blkIdx][y*4+x]
			}
		}
	}
	return out
}

func storeCABACChromaDC(mb *syntax.MBInter, comp int, dc [4]int16) {
	if mb == nil || comp < 0 || comp >= 2 {
		return
	}
	for blk := 0; blk < 4; blk++ {
		mb.CoeffsChroma[comp][blk][0] = dc[blk]
	}
}

func storeCABACChromaAC(mb *syntax.MBInter, comp, blk int, ac [16]int16) {
	if mb == nil || comp < 0 || comp >= 2 || blk < 0 || blk >= 4 {
		return
	}
	// CABAC chroma AC residuals are decoded with the scan starting after DC.
	// Preserve slot 0, which was populated from the separate chroma DC block.
	for j := 1; j < 16; j++ {
		mb.CoeffsChroma[comp][blk][j] = ac[j]
	}
}

func storeCABACIntraChromaDC(mb *syntax.MBIntra, comp int, dc [4]int16) {
	if mb == nil || comp < 0 || comp >= 2 {
		return
	}
	for blk := 0; blk < 4; blk++ {
		mb.CoeffsChroma[comp][blk][0] = dc[blk]
	}
}

func storeCABACIntraChromaAC(mb *syntax.MBIntra, comp, blk int, ac [16]int16) {
	if mb == nil || comp < 0 || comp >= 2 || blk < 0 || blk >= 4 {
		return
	}
	// Same CABAC chroma split as inter: AC residuals occupy coeff slots 1..15,
	// while slot 0 is supplied by the separately decoded chroma DC block.
	for j := 1; j < 16; j++ {
		mb.CoeffsChroma[comp][blk][j] = ac[j]
	}
}

// decodeCABACIntraMB decodes one CABAC-coded I-slice intra macroblock.
// Models the FFmpeg decode_cabac_intra_mb_type / decode_cabac_mb_intra4x4_pred_mode
// / decode_cabac_mb_chroma_pre_mode flow from h264_cabac.c.
func decodeCABACIntraMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int, leftCBP, topCBP uint32, leftMBType, topMBType uint32, leftChromaPred, topChromaPred int8, transform8x8Mode bool, transform8x8Ctx int, leftEdge8x8, topEdge8x8 [2]int8) *syntax.MBIntra {
	return decodeCABACIntraMBWithParams(dec, models, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftMBType, topMBType, leftChromaPred, topChromaPred, transform8x8Mode, transform8x8Ctx, leftEdge8x8, topEdge8x8, 3, true)
}

func decodeCABACIntraMBWithParams(dec *cabac.CABACDecoder, models []cabac.CABACCtx, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int, leftCBP, topCBP uint32, leftMBType, topMBType uint32, leftChromaPred, topChromaPred int8, transform8x8Mode bool, transform8x8Ctx int, leftEdge8x8, topEdge8x8 [2]int8, ctxBase int, intraSlice bool) *syntax.MBIntra {
	mb := &syntax.MBIntra{}
	if dec == nil || len(models) < 128 || ctxBase < 0 || ctxBase+5 >= len(models) {
		return mb
	}

	// mb_type: FFmpeg decode_cabac_intra_mb_type(ctx_base, intra_slice).
	stateOffset := ctxBase
	isI16 := false
	if intraSlice {
		intraCtx := int(isCABACIntra16orPCM(leftMBType) + 2*isCABACIntra16orPCM(topMBType))
		if dec.DecodeBin(&models[ctxBase+intraCtx]) == 0 {
			mb.MBType = 0 // I_NxN
		} else {
			stateOffset += 2
			isI16 = true
		}
	} else if dec.DecodeBin(&models[ctxBase]) == 0 {
		mb.MBType = 0 // I_NxN
	} else {
		stateOffset = ctxBase
		isI16 = true
	}
	if isI16 {
		if dec.DecodeTerminate() == 1 {
			mb.MBType = 25 // I_PCM
			return mb
		}
		// I_16x16: binarize cbp_luma / cbp_chroma / pred_mode.
		mbType := uint32(1)
		if dec.DecodeBin(&models[stateOffset+1]) == 1 {
			mbType += 12
		}
		if dec.DecodeBin(&models[stateOffset+2]) == 1 {
			chromaExtraCtx := stateOffset + 2
			if intraSlice {
				chromaExtraCtx++
			}
			mbType += 4 + 4*dec.DecodeBin(&models[chromaExtraCtx])
		}
		predCtx0 := stateOffset + 3
		predCtx1 := stateOffset + 3
		if intraSlice {
			predCtx0++
			predCtx1 += 2
		}
		mbType += 2 * dec.DecodeBin(&models[predCtx0])
		mbType += 1 * dec.DecodeBin(&models[predCtx1])
		mb.MBType = mbType
	}

	// Intra 4x4 / 8x8 prediction modes (I_NxN only)
	if mb.MBType == 0 {
		if enableCABACI8x8Transform && transform8x8Mode && decodeCABACTransform8x8Flag(dec, models, transform8x8Ctx) {
			mb.Use8x8Transform = true
			var localModes [4]int8
			for i := 0; i < 4; i++ {
				bc := i % 2
				br := i / 2
				var leftMode int8
				if bc == 0 {
					leftMode = leftEdge8x8[br]
				} else {
					leftMode = localModes[i-1]
				}
				var topMode int8
				if br == 0 {
					topMode = topEdge8x8[bc]
				} else {
					topMode = localModes[i-2]
				}
				if leftMode < 0 {
					leftMode = 2
				}
				if topMode < 0 {
					topMode = 2
				}
				predMode := leftMode
				if topMode < predMode {
					predMode = topMode
				}
				if dec.DecodeBin(&models[68]) == 1 {
					mb.I8x8PredMode[i] = predMode
				} else {
					mode := int8(0)
					mode |= int8(dec.DecodeBin(&models[69]))
					mode |= int8(dec.DecodeBin(&models[69])) << 1
					mode |= int8(dec.DecodeBin(&models[69])) << 2
					if mode >= predMode {
						mode++
					}
					mb.I8x8PredMode[i] = mode
				}
				localModes[i] = mb.I8x8PredMode[i]
			}
		} else {
			// I4x4: one pred mode per 4x4 block (16 total)
			for i := 0; i < 16; i++ {
				if dec.DecodeBin(&models[68]) == 1 {
					mb.IntraPredMode[i] = -1
				} else {
					mode := int8(0)
					mode |= int8(dec.DecodeBin(&models[69]))
					mode |= int8(dec.DecodeBin(&models[69])) << 1
					mode |= int8(dec.DecodeBin(&models[69])) << 2
					mb.IntraPredMode[i] = mode
				}
			}
		}
	} else if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: prediction mode and CBP from mb_type
		mb.Intra16x16PredMode = int8((mb.MBType - 1) % 4)
		cbpChroma := (mb.MBType - 1) / 4 % 3
		cbpLuma := uint32(0)
		if (mb.MBType-1)/12 > 0 {
			cbpLuma = 15
		}
		mb.CodedBlockPattern = cbpLuma | (cbpChroma << 4)
	}

	// Chroma prediction mode (ctx 64-67)
	chromaPredCtx := cabacChromaPredModeCtx(leftChromaPred, topChromaPred)
	if dec.DecodeBin(&models[64+chromaPredCtx]) == 0 {
		mb.ChromaPredMode = 0
	} else if dec.DecodeBin(&models[67]) == 0 {
		mb.ChromaPredMode = 1
	} else if dec.DecodeBin(&models[67]) == 0 {
		mb.ChromaPredMode = 2
	} else {
		mb.ChromaPredMode = 3
	}

	// CBP for I_NxN (I_16x16 CBP is in mb_type already)
	if mb.MBType == 0 {
		mb.CodedBlockPattern = syntax.DecodeCABACCBP(dec, models, leftCBP, topCBP)
	}

	// QP delta
	if mb.CodedBlockPattern > 0 || (mb.MBType >= 1 && mb.MBType <= 24) {
		mb.QPDelta = int32(syntax.DecodeCABACDQP(dec, models, 0))
	}

	// Residual coefficients
	var nzMB [16]int
	var nzMBChroma [2][4]int
	if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: luma DC (cat=0) then luma AC (cat=1) per block if cbp_luma
		var dcBuf [16]int16
		dec.DecodeCABACResidual(models, 0, 16, dcBuf[:], 0, 0)
		for pos := 0; pos < 16; pos++ {
			blk := blkXYToIdx[pos/4][pos%4]
			mb.Coeffs[blk][0] = dcBuf[pos]
		}
		cbpLuma := mb.CodedBlockPattern & 0xF
		if cbpLuma != 0 {
			for blk := 0; blk < 16; blk++ {
				nza, nzb := nzCBFCtxLuma(blk, &nzMB, leftNZ, topNZ)
				var acBuf [16]int16
				tc := dec.DecodeCABACResidual(models, 1, 15, acBuf[:], nza, nzb)
				for j := 1; j < 16; j++ {
					mb.Coeffs[blk][j] = acBuf[j]
				}
				mb.TotalCoeff[blk] = tc
				nzMB[blk] = tc
			}
		}
	} else if mb.MBType == 0 {
		cbpLuma := mb.CodedBlockPattern & 0xF
		if mb.Use8x8Transform {
			for group := 0; group < 4; group++ {
				if cbpLuma&(1<<uint(group)) != 0 {
					var buf [64]int16
					tc := dec.DecodeCABACResidual(models, 5, 64, buf[:], 0, 0)
					splitLuma8x8Residual(&mb.Coeffs, group, buf)
					for sub := 0; sub < 4; sub++ {
						blkIdx := group*4 + sub
						mb.TotalCoeff[blkIdx] = tc
						nzMB[blkIdx] = tc
					}
				}
			}
		} else {
			for group := 0; group < 4; group++ {
				if cbpLuma&(1<<uint(group)) != 0 {
					for sub := 0; sub < 4; sub++ {
						blkIdx := group*4 + sub
						nza, nzb := nzCBFCtxLuma(blkIdx, &nzMB, leftNZ, topNZ)
						var buf [16]int16
						tc := dec.DecodeCABACResidual(models, 2, 16, buf[:], nza, nzb)
						mb.Coeffs[blkIdx] = [16]int16(buf)
						mb.TotalCoeff[blkIdx] = tc
						nzMB[blkIdx] = tc
					}
				}
			}
		}
	}

	// Chroma residuals
	chromaCBP := (mb.CodedBlockPattern >> 4) & 0x3
	if chromaCBP > 0 {
		for comp := 0; comp < 2; comp++ {
			var dc [4]int16
			dec.DecodeCABACResidual(models, 3, 4, dc[:], 0, 0)
			storeCABACIntraChromaDC(mb, comp, dc)
		}
	}
	if chromaCBP > 1 {
		for comp := 0; comp < 2; comp++ {
			for blk := 0; blk < 4; blk++ {
				nza, nzb := nzCBFCtxChroma(comp, blk, &nzMBChroma, leftChromaNZ, topChromaNZ)
				var ac [16]int16
				tc := dec.DecodeCABACResidual(models, 4, 15, ac[:], nza, nzb)
				storeCABACIntraChromaAC(mb, comp, blk, ac)
				mb.ChromaTotalCoeff[comp][blk] = tc
				nzMBChroma[comp][blk] = tc
			}
		}
	}

	return mb
}
