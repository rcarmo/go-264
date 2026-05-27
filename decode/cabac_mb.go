package decode

// decode/cabac_mb.go — CABAC macroblock decode for P-slice inter and I-slice
// intra macroblocks. Calls syntax.DecodeCABACCBP/DQP/Ref/MVD for pure syntax;
// residual coefficients are decoded via cabac.CABACDecoder.DecodeCABACResidual.

import (
	"fmt"
	"os"
	"strconv"

	cabac "github.com/rcarmo/go-264/entropy/cabac"
	"github.com/rcarmo/go-264/syntax"
)

// enableCABACI8x8Transform enables High-profile CABAC I_NxN 8x8 transform
// flag consumption. FFmpeg consumes this flag before intra prediction-mode bins;
// skipping it desynchronizes all following CABAC syntax on High-profile streams.
const enableCABACI8x8Transform = true

// cabacMinMacroblockContexts covers the highest macroblock-level context this
// file may index directly (transform_size_8x8_flag at ctxIdx 399+n). Residual
// decoders perform their own stricter table-size checks.
const cabacMinMacroblockContexts = 402

// decodeCABACPInterMB decodes one CABAC-coded P-slice macroblock.
// Returns (inter, nil, true) for P-skip, (nil, intra, false) for intra-in-P.
func decodeCABACPInterMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx, numRefFrames uint32, lastQScaleDiff int, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int, leftCBP, topCBP uint32, leftNonSkip, topNonSkip bool, refCtxs [4]int, ref4 []int8, mvd4 []syntax.MotionVector, stride4, mbX, mbY int, currentPOC int, transform8x8Mode bool, transform8x8Ctx int, leftMBType, topMBType uint32, leftChromaPred, topChromaPred int8, leftEdge8x8, topEdge8x8 [2]int8) (*syntax.MBInter, *syntax.MBIntra, bool) {
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
	tracePTypeLimit := 2
	if v := os.Getenv("GO264_P_TYPE_TRACE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tracePTypeLimit = n
		}
	}
	tracePType := os.Getenv("GO264_P_TYPE_TRACE") != "" && mbY*stride4/4+mbX < tracePTypeLimit
	pTypeTrace := ""
	decodePTypeBin := func(ctx int) uint32 {
		preLow, preRange, _ := dec.DebugState()
		preState := models[ctx].DebugPackedState()
		bin := dec.DecodeBin(&models[ctx])
		if tracePType {
			postLow, postRange, _ := dec.DebugState()
			pTypeTrace += fmt.Sprintf(" ctx=%d state=%d pre=%d/%d bin=%d post_state=%d post=%d/%d", ctx, preState, preLow, preRange, bin, models[ctx].DebugPackedState(), postLow, postRange)
		}
		return bin
	}
	if decodePTypeBin(14) == 0 {
		if decodePTypeBin(15) == 0 {
			mb.MBType = 3 * decodePTypeBin(16) // P16x16 or P8x8
		} else {
			mb.MBType = 2 - decodePTypeBin(17) // P8x16 or P16x8
		}
	} else {
		if tracePType {
			fmt.Fprintf(os.Stderr, "GOPTYPE mb=%04d poc=%d raw=intra%s\n", mbY*stride4/4+mbX, currentPOC, pTypeTrace)
		}
		// FFmpeg h264_cabac.c decodes intra-in-P via decode_cabac_intra_mb_type(ctx_base=17, intra_slice=0).
		if cabacUseFFmpegEdgeContexts() {
			leftCBP, topCBP = cabacUnavailableCBP(leftCBP, topCBP, mbX, mbY, true)
			leftNZ, topNZ = cabacTraceEdgeNZ(mbX, mbY, leftNZ, topNZ)
			leftChromaNZ, topChromaNZ = cabacTraceEdgeChromaNZ(mbX, mbY, leftChromaNZ, topChromaNZ)
		}
		intra := decodeCABACIntraMBWithParams(dec, models, lastQScaleDiff, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftMBType, topMBType, leftChromaPred, topChromaPred, transform8x8Mode, transform8x8Ctx, leftEdge8x8, topEdge8x8, 17, false)
		return nil, intra, false
	}
	if tracePType {
		fmt.Fprintf(os.Stderr, "GOPTYPE mb=%04d poc=%d raw=%d%s\n", mbY*stride4/4+mbX, currentPOC, mb.MBType, pTypeTrace)
	}
	if cabacUseFFmpegEdgeContexts() {
		leftCBP, topCBP = cabacUnavailableCBP(leftCBP, topCBP, mbX, mbY, false)
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
		tracePRef := os.Getenv("GO264_P_REF_TRACE") != "" && currentPOC == 28 && mbY*stride4/4+mbX < tracePTypeLimit
		for i := 0; i < parts; i++ {
			ctxSlot := i
			if mb.MBType == syntax.PMBTypeP16x8 && i == 1 {
				ctxSlot = 2 // second 16x8 partition starts at the bottom-left 8x8 origin
			}
			ctx := refCtxs[ctxSlot]
			bx, by, bw, bh := cabacPPartRefRect(mb.MBType, i, mbX*4, mbY*4)
			if len(ref4) > 0 {
				ctx = cabacRefIdxCtx(ref4, stride4, bx, by)
			}
			preLow, preRange, _ := dec.DebugState()
			ref := syntax.DecodeCABACRef(dec, models, ctx)
			postLow, postRange, _ := dec.DebugState()
			mb.RefIdx[i] = int8(ref)
			if len(ref4) > 0 {
				fillRef4(ref4, stride4, bx, by, bw, bh, mb.RefIdx[i])
			}
			if tracePRef {
				fmt.Fprintf(os.Stderr, "GOREF mb=%04d poc=%d part=%d ctx=%d ref=%d pre=%d/%d post=%d/%d\n", mbY*stride4/4+mbX, currentPOC, i, ctx, ref, preLow, preRange, postLow, postRange)
			}
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
					mb.SubMV[i*4+j], _ = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, baseX, baseY+j, 2, 1, i*4+j, 0, currentPOC)
				}
			case 2: // P_L0_4x8
				for j := 0; j < 2; j++ {
					mb.SubMV[i*4+j], _ = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, baseX+j, baseY, 1, 2, i*4+j, 0, currentPOC)
				}
			case 3: // P_L0_4x4
				for j := 0; j < 4; j++ {
					mb.SubMV[i*4+j], _ = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, baseX+(j&1), baseY+(j>>1), 1, 1, i*4+j, 0, currentPOC)
				}
			default: // P_L0_8x8
				mb.SubMV[i*4], _ = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, baseX, baseY, 2, 2, i, 0, currentPOC)
			}
		}
		mb.DecodedMVDX = mb.SubMV[0].X
		mb.DecodedMVDY = mb.SubMV[0].Y
	} else {
		for i := 0; i < parts; i++ {
			switch mb.MBType {
			case syntax.PMBTypeP16x8:
				mb.MV[i], _ = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, x4, y4+i*2, 4, 2, i, 0, currentPOC)
			case syntax.PMBTypeP8x16:
				mb.MV[i], _ = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, x4+i*2, y4, 2, 4, i, 0, currentPOC)
			default:
				mb.MV[i], _ = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, x4, y4, 4, 4, i, 0, currentPOC)
			}
		}
		mb.DecodedMVDX = mb.MV[0].X
		mb.DecodedMVDY = mb.MV[0].Y
	}
	tracePCABAC := os.Getenv("GO264_P_CABAC_TRACE") != "" && currentPOC == 28 && mbY*stride4/4+mbX < tracePTypeLimit
	if tracePCABAC {
		preLow, preRange, _ := dec.DebugState()
		fmt.Fprintf(os.Stderr, "GOP_PRE_CBP mb=%04d poc=%d type=%d left=%02x top=%02x low=%d range=%d\n", mbY*stride4/4+mbX, currentPOC, ffInterMBType(mb), leftCBP, topCBP, preLow, preRange)
	}
	if tracePCABAC {
		mb.CBP = syntax.DecodeCABACCBPWithTrace(dec, models, leftCBP, topCBP, fmt.Sprintf("mb=%04d poc=%d", mbY*stride4/4+mbX, currentPOC))
	} else {
		mb.CBP = syntax.DecodeCABACCBP(dec, models, leftCBP, topCBP)
	}
	if tracePCABAC {
		postLow, postRange, _ := dec.DebugState()
		fmt.Fprintf(os.Stderr, "GOP_POST_CBP mb=%04d poc=%d cbp=%02x low=%d range=%d\n", mbY*stride4/4+mbX, currentPOC, mb.CBP, postLow, postRange)
	}
	if mb.CBP != 0 {
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
		if tracePCABAC {
			preLow, preRange, _ := dec.DebugState()
			fmt.Fprintf(os.Stderr, "GOP_PRE_DQP mb=%04d poc=%d 8x8=%d low=%d range=%d\n", mbY*stride4/4+mbX, currentPOC, boolInt(mb.Use8x8Transform), preLow, preRange)
		}
		mb.QPDelta = int32(syntax.DecodeCABACDQP(dec, models, lastQScaleDiff))
		if tracePCABAC {
			postLow, postRange, _ := dec.DebugState()
			fmt.Fprintf(os.Stderr, "GOP_POST_DQP mb=%04d poc=%d qpd=%d low=%d range=%d\n", mbY*stride4/4+mbX, currentPOC, mb.QPDelta, postLow, postRange)
		}
		var nzMB [16]int
		if use8x8Residual {
			for group := 0; group < 4; group++ {
				if mb.CBP&(1<<uint(group)) != 0 {
					var buf [64]int16
					tc := dec.DecodeCABACResidual(models, 5, 64, buf[:], 0, 0)
					splitLuma8x8Residual(&mb.Coeffs, group, buf)
					for sub := 0; sub < 4; sub++ {
						blkIdx := luma4x4BlockFor8x8Group(group, sub)
						nzMB[blkIdx] = tc
						mb.TotalCoeff[blkIdx] = tc
					}
				}
			}
		} else {
			for group := 0; group < 4; group++ {
				if mb.CBP&(1<<uint(group)) != 0 {
					for sub := 0; sub < 4; sub++ {
						blkIdx := luma4x4BlockFor8x8Group(group, sub)
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
				nza, nzb := cabacChromaDCCtx(comp, leftCBP, topCBP)
				var dc [4]int16
				tc := dec.DecodeCABACResidual(models, 3, 4, dc[:], nza, nzb)
				if tc > 0 {
					mb.CBP |= 0x40 << uint(comp)
				}
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

func cabacPPartRefRect(mbType uint32, part, x4, y4 int) (int, int, int, int) {
	switch mbType {
	case syntax.PMBTypeP16x8:
		return x4, y4 + part*2, 4, 2
	case syntax.PMBTypeP8x16:
		return x4 + part*2, y4, 2, 4
	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		return x4 + (part&1)*2, y4 + (part>>1)*2, 2, 2
	default:
		return x4, y4, 4, 4
	}
}

func decodeCABACMVDPair(dec *cabac.CABACDecoder, models []cabac.CABACCtx, mvd4 []syntax.MotionVector, stride4, x4, y4, w4, h4 int) syntax.MotionVector {
	mvd, _ := decodeCABACMVDPairDiag(dec, models, mvd4, stride4, x4, y4, w4, h4, -1, -1, -1)
	return mvd
}

func decodeCABACMVDPairDiag(dec *cabac.CABACDecoder, models []cabac.CABACCtx, mvd4 []syntax.MotionVector, stride4, x4, y4, w4, h4, part, list, poc int) (syntax.MotionVector, syntax.MotionVector) {
	mbAddr := 0
	if stride4 > 0 {
		mbAddr = (y4/4)*(stride4/4) + x4/4
	}
	traceComp := (os.Getenv("GO264_B_MVD_COMP_TRACE") != "" || os.Getenv("GO264_P_MVD_COMP_TRACE") != "") && part >= 0 && list >= 0
	amvdX := cabacMVDAMVD(mvd4, stride4, x4, y4, 0)
	preLowX, preRangeX, _ := dec.DebugState()
	mdx := syntax.DecodeCABACMVD(dec, models, 40, amvdX)
	postLowX, postRangeX, _ := dec.DebugState()
	if traceComp {
		fmt.Fprintf(os.Stderr, "GOMVD_COMP mb=%04d poc=%d part=%d list=%d comp=x amvd=%d mvd=%d pre=%d/%d post=%d/%d\n", mbAddr, poc, part, list, amvdX, mdx, preLowX, preRangeX, postLowX, postRangeX)
	}
	amvdY := cabacMVDAMVD(mvd4, stride4, x4, y4, 1)
	preLowY, preRangeY, _ := dec.DebugState()
	mdy := syntax.DecodeCABACMVD(dec, models, 47, amvdY)
	postLowY, postRangeY, _ := dec.DebugState()
	if traceComp {
		fmt.Fprintf(os.Stderr, "GOMVD_COMP mb=%04d poc=%d part=%d list=%d comp=y amvd=%d mvd=%d pre=%d/%d post=%d/%d\n", mbAddr, poc, part, list, amvdY, mdy, preLowY, preRangeY, postLowY, postRangeY)
	}
	mvd := syntax.MotionVector{X: mdx, Y: mdy}
	// FFmpeg's decode_cabac_mb_mvd returns the full signed MVD for motion
	// reconstruction, but stores min(abs(mvd),70) in mvda for future CABAC
	// context selection. Keep those roles separate so large legal MVDs don't
	// poison neighbouring context bins.
	fillMVD4(mvd4, stride4, x4, y4, w4, h4, cabacMVDContextVector(mvd))
	return mvd, syntax.MotionVector{X: int16(amvdX), Y: int16(amvdY)}
}

func cabacMVDContextVector(mv syntax.MotionVector) syntax.MotionVector {
	return syntax.MotionVector{X: cabacMVDContextComponent(mv.X), Y: cabacMVDContextComponent(mv.Y)}
}

func cabacMVDContextComponent(v int16) int16 {
	if v < 0 {
		v = -v
	}
	if v > 70 {
		return 70
	}
	return v
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
	if dec == nil || idx < 0 || idx >= len(models) {
		return false
	}
	preLow, preRange, _ := dec.DebugState()
	preState := models[idx].DebugPackedState()
	bin := dec.DecodeBin(&models[idx])
	postLow, postRange, _ := dec.DebugState()
	if os.Getenv("GO264_CABAC_SYNTAX_TRACE") != "" {
		fmt.Fprintf(os.Stderr, "GOSYN part=transform_size_8x8_flag idx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d ctx=%d\n", idx, preState, preLow, preRange, bin, models[idx].DebugPackedState(), postLow, postRange, cabacTransform8x8Ctx(ctx))
	}
	return bin == 1
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

func decodeCABACIPCMSamples(dec *cabac.CABACDecoder, mb *syntax.MBIntra) {
	if dec == nil || mb == nil {
		return
	}
	dec.ByteAlign()
	for i := range mb.PCMY {
		mb.PCMY[i] = dec.ReadPCMByte()
	}
	for i := range mb.PCMCb {
		mb.PCMCb[i] = dec.ReadPCMByte()
	}
	for i := range mb.PCMCr {
		mb.PCMCr[i] = dec.ReadPCMByte()
	}
	dec.Reset()
}

func luma4x4BlockFor8x8Group(group, sub int) int {
	baseCol := (group & 1) * 2
	baseRow := (group >> 1) * 2
	return blkXYToIdx[baseRow+(sub>>1)][baseCol+(sub&1)]
}

func splitLuma8x8Residual(dst *[16][16]int16, group int, src [64]int16) {
	if dst == nil || group < 0 || group >= 4 {
		return
	}
	for sub := 0; sub < 4; sub++ {
		blkIdx := blkXYToIdx[(group>>1)*2+(sub>>1)][(group&1)*2+(sub&1)]
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
		blkIdx := blkXYToIdx[(group>>1)*2+(sub>>1)][(group&1)*2+(sub&1)]
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

func cabacLumaDCCtx(leftCBP, topCBP uint32) (int, int) {
	nza, nzb := 0, 0
	if leftCBP&0x100 != 0 {
		nza = 1
	}
	if topCBP&0x100 != 0 {
		nzb = 1
	}
	return nza, nzb
}

func cabacChromaDCCtx(comp int, leftCBP, topCBP uint32) (int, int) {
	if comp < 0 || comp >= 2 {
		return 0, 0
	}
	mask := uint32(0x40 << uint(comp))
	nza, nzb := 0, 0
	if leftCBP&mask != 0 {
		nza = 1
	}
	if topCBP&mask != 0 {
		nzb = 1
	}
	return nza, nzb
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
func cabacPredIntraMode(left, top int8) int8 {
	if left < 0 || top < 0 {
		return 2
	}
	if top < left {
		return top
	}
	return left
}

func decodeCABACIntraMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx, lastQScaleDiff int, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int, leftCBP, topCBP uint32, leftMBType, topMBType uint32, leftChromaPred, topChromaPred int8, transform8x8Mode bool, transform8x8Ctx int, leftEdge8x8, topEdge8x8 [2]int8) *syntax.MBIntra {
	return decodeCABACIntraMBWithParams(dec, models, lastQScaleDiff, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftMBType, topMBType, leftChromaPred, topChromaPred, transform8x8Mode, transform8x8Ctx, leftEdge8x8, topEdge8x8, 3, true)
}

func decodeCABACIntraMBWithParams(dec *cabac.CABACDecoder, models []cabac.CABACCtx, lastQScaleDiff int, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int, leftCBP, topCBP uint32, leftMBType, topMBType uint32, leftChromaPred, topChromaPred int8, transform8x8Mode bool, transform8x8Ctx int, leftEdge8x8, topEdge8x8 [2]int8, ctxBase int, intraSlice bool) *syntax.MBIntra {
	mb := &syntax.MBIntra{}
	if dec == nil || len(models) < 128 || ctxBase < 0 || ctxBase+5 >= len(models) {
		return mb
	}

	traceSyntax := os.Getenv("GO264_CABAC_SYNTAX_TRACE") != ""
	traceBin := func(label string, idx int) uint32 {
		preLow, preRange, _ := dec.DebugState()
		preState := models[idx].DebugPackedState()
		bin := dec.DecodeBin(&models[idx])
		postLow, postRange, _ := dec.DebugState()
		if traceSyntax {
			fmt.Fprintf(os.Stderr, "GOSYN part=%s idx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d\n", label, idx, preState, preLow, preRange, bin, models[idx].DebugPackedState(), postLow, postRange)
		}
		return bin
	}

	// mb_type: FFmpeg decode_cabac_intra_mb_type(ctx_base, intra_slice).
	stateOffset := ctxBase
	isI16 := false
	if intraSlice {
		// FFmpeg decode_cabac_intra_mb_type() uses the count of I16x16/PCM
		// neighbours, not a directional left+2*top code: left and top each add one.
		intraCtx := int(isCABACIntra16orPCM(leftMBType) + isCABACIntra16orPCM(topMBType))
		if traceBin("intra_mb_type0", ctxBase+intraCtx) == 0 {
			mb.MBType = 0 // I_NxN
		} else {
			stateOffset += 2
			isI16 = true
		}
	} else if traceBin("intra_mb_type0", ctxBase) == 0 {
		mb.MBType = 0 // I_NxN
	} else {
		stateOffset = ctxBase
		isI16 = true
	}
	if isI16 {
		if dec.DecodeTerminate() == 1 {
			mb.MBType = syntax.MBTypeIPCM
			decodeCABACIPCMSamples(dec, mb)
			return mb
		}
		// I_16x16: binarize cbp_luma / cbp_chroma / pred_mode.
		mbType := uint32(1)
		if traceBin("i16_cbp_luma", stateOffset+1) == 1 {
			mbType += 12
		}
		if traceBin("i16_cbp_chroma0", stateOffset+2) == 1 {
			chromaExtraCtx := stateOffset + 2
			if intraSlice {
				chromaExtraCtx++
			}
			mbType += 4 + 4*traceBin("i16_cbp_chroma1", chromaExtraCtx)
		}
		predCtx0 := stateOffset + 3
		predCtx1 := stateOffset + 3
		if intraSlice {
			predCtx0++
			predCtx1 += 2
		}
		mbType += 2 * traceBin("i16_pred0", predCtx0)
		mbType += 1 * traceBin("i16_pred1", predCtx1)
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
				predMode := cabacPredIntraMode(leftMode, topMode)
				if traceBin("i8x8_prev", 68) == 1 {
					mb.I8x8PredMode[i] = predMode
				} else {
					mode := int8(0)
					mode |= int8(traceBin("i8x8_rem0", 69))
					mode |= int8(traceBin("i8x8_rem1", 69)) << 1
					mode |= int8(traceBin("i8x8_rem2", 69)) << 2
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
				if traceBin("i4x4_prev", 68) == 1 {
					mb.IntraPredMode[i] = -1
				} else {
					mode := int8(0)
					mode |= int8(traceBin("i4x4_rem0", 69))
					mode |= int8(traceBin("i4x4_rem1", 69)) << 1
					mode |= int8(traceBin("i4x4_rem2", 69)) << 2
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
	if traceBin("chroma_pred0", 64+chromaPredCtx) == 0 {
		mb.ChromaPredMode = 0
	} else if traceBin("chroma_pred1", 67) == 0 {
		mb.ChromaPredMode = 1
	} else if traceBin("chroma_pred2", 67) == 0 {
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
		mb.QPDelta = int32(syntax.DecodeCABACDQP(dec, models, lastQScaleDiff))
	}

	// Residual coefficients
	var nzMB [16]int
	var nzMBChroma [2][4]int
	if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: luma DC (cat=0) then luma AC (cat=1) per block if cbp_luma
		nza, nzb := cabacLumaDCCtx(leftCBP, topCBP)
		var dcBuf [16]int16
		dcTC := dec.DecodeCABACResidual(models, 0, 16, dcBuf[:], nza, nzb)
		if dcTC > 0 {
			mb.CodedBlockPattern |= 0x100
		}
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
						blkIdx := luma4x4BlockFor8x8Group(group, sub)
						mb.TotalCoeff[blkIdx] = tc
						nzMB[blkIdx] = tc
					}
				}
			}
		} else {
			for group := 0; group < 4; group++ {
				if cbpLuma&(1<<uint(group)) != 0 {
					for sub := 0; sub < 4; sub++ {
						blkIdx := luma4x4BlockFor8x8Group(group, sub)
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
			nza, nzb := cabacChromaDCCtx(comp, leftCBP, topCBP)
			var dc [4]int16
			tc := dec.DecodeCABACResidual(models, 3, 4, dc[:], nza, nzb)
			if tc > 0 {
				mb.CodedBlockPattern |= 0x40 << uint(comp)
			}
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

// decodeCABACBidiMB decodes one CABAC-coded B-slice macroblock.
// Returns (bidi, nil, true) for B-skip/Direct, (nil, intra, false) for intra-in-B.
// Implements H.264 §9.3.2.1 / FFmpeg h264_cabac.c ff_h264_decode_mb_cabac B-slice path.
//
// B-slice differences from P-slice:
//   - Skip flag context is 24+ctx (B-slice: ctx adds 13 vs P base 11, but FFmpeg
//     uses decode_cabac_mb_skip which adds 13 for B → effectively base 24 with
//     neighbor corrections).
//   - MB type binarized at ctx 27+{0..5} with B-specific table.
//   - Sub-MB types for B_8x8 use ctx 36-39.
//   - Both L0 and L1 ref-idx / MVD fields present where applicable.
func decodeCABACBidiMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx,
	numRefL0, numRefL1 uint32, lastQScaleDiff int,
	leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int,
	leftCBP, topCBP uint32,
	leftNonSkip, topNonSkip bool,
	leftIsDirect, topIsDirect bool,
	refCtxs [4]int,
	mv4 []syntax.MotionVector, ref4 []int8, direct4 []bool, mv4L1 []syntax.MotionVector, ref4L1 []int8, mvd4 []syntax.MotionVector, mvd4L1 []syntax.MotionVector, stride4, mbX, mbY int,
	currentPOC int,
	transform8x8Mode bool, transform8x8Ctx int,
	leftMBType, topMBType uint32,
	leftChromaPred, topChromaPred int8,
	leftEdge8x8, topEdge8x8 [2]int8,
) (*syntax.MBBidi, *syntax.MBIntra, bool) {
	mb := &syntax.MBBidi{}
	if dec == nil || len(models) < cabacMinMacroblockContexts {
		// Safe fallback: treat as B_Direct_16x16 skip.
		return mb, nil, true
	}

	// B-slice skip flag: ctxIdx = 24 + availability of non-direct left/top MBs.
	// FFmpeg: decode_cabac_mb_skip adds 13 to the P base (11 + ctx) for B-slices.
	skipCtx := 24
	if leftNonSkip {
		skipCtx++
	}
	if topNonSkip {
		skipCtx++
	}
	if dec.DecodeBin(&models[skipCtx]) == 1 {
		// B_Direct_16x16 skip.
		mb.MBType = syntax.BMBTypeDirect16x16
		fillMVD4(mvd4, stride4, mbX*4, mbY*4, 4, 4, syntax.MotionVector{})
		if mvd4L1 != nil {
			fillMVD4(mvd4L1, stride4, mbX*4, mbY*4, 4, 4, syntax.MotionVector{})
		}
		return mb, nil, true
	}

	// B-slice MB type binarization: ctx base = 27 + ctxOffset.
	// ctxOffset: +1 if left MB is non-Direct, +1 if top MB is non-Direct.
	// Mirrors FFmpeg: if (!IS_DIRECT(left_type-1)) ctx++; if (!IS_DIRECT(top_type-1)) ctx++.
	// leftIsDirect=false means the left MB was non-Direct → increment ctx.
	typeCtxOffset := 0
	if !leftIsDirect {
		typeCtxOffset++
	}
	if !topIsDirect {
		typeCtxOffset++
	}
	traceBTypeLimit := 20
	if v := os.Getenv("GO264_B_TYPE_TRACE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			traceBTypeLimit = n
		}
	}
	traceBType := os.Getenv("GO264_B_TYPE_TRACE") != "" && currentPOC == 20 && mbY*stride4/4+mbX < traceBTypeLimit
	typeTrace := ""
	decodeTypeBin := func(ctx int) int {
		preLow, preRange, _ := dec.DebugState()
		preState := models[ctx].DebugPackedState()
		bin := dec.DecodeBin(&models[ctx])
		if traceBType {
			postLow, postRange, _ := dec.DebugState()
			typeTrace += fmt.Sprintf(" ctx=%d state=%d pre=%d/%d bin=%d post_state=%d post=%d/%d", ctx, preState, preLow, preRange, bin, models[ctx].DebugPackedState(), postLow, postRange)
		}
		return int(bin)
	}
	if decodeTypeBin(27+typeCtxOffset) == 0 {
		mb.MBType = syntax.BMBTypeDirect16x16
	} else if decodeTypeBin(27+3) == 0 {
		// B_L0_16x16 or B_L1_16x16.
		mb.MBType = uint32(1 + decodeTypeBin(27+5))
	} else {
		bits := decodeTypeBin(27+4) << 3
		bits |= decodeTypeBin(27+5) << 2
		bits |= decodeTypeBin(27+5) << 1
		bits |= decodeTypeBin(27 + 5)
		switch {
		case bits < 8:
			mb.MBType = uint32(3 + bits) // B_Bi_16x16 through B_L1_L0_16x8
		case bits == 13:
			// Intra-in-B.
			if cabacUseFFmpegEdgeContexts() {
				leftCBP, topCBP = cabacUnavailableCBP(leftCBP, topCBP, mbX, mbY, true)
				leftNZ, topNZ = cabacTraceEdgeNZ(mbX, mbY, leftNZ, topNZ)
				leftChromaNZ, topChromaNZ = cabacTraceEdgeChromaNZ(mbX, mbY, leftChromaNZ, topChromaNZ)
			}
			intra := decodeCABACIntraMBWithParams(dec, models, lastQScaleDiff,
				leftNZ, topNZ, leftChromaNZ, topChromaNZ,
				leftCBP, topCBP, leftMBType, topMBType,
				leftChromaPred, topChromaPred,
				transform8x8Mode, transform8x8Ctx,
				leftEdge8x8, topEdge8x8,
				32, false)
			return nil, intra, false
		case bits == 14:
			mb.MBType = 11 // B_L1_L0_8x16
		case bits == 15:
			mb.MBType = syntax.BMBTypeB8x8
		default:
			// bits ∈ {8..12}: read one more bin (ctx 27+5).
			// shifted_bits = (bits<<1)|extra ∈ {16..25} → mb_type = shifted_bits-4 ∈ {12..21}.
			// B_L0_Bi_16x8 through B_Bi_Bi_8x16. Mirrors FFmpeg: mb_type = bits - 4.
			bits = (bits << 1) | decodeTypeBin(27+5)
			mb.MBType = uint32(bits - 4)
		}
	}
	if traceBType {
		fmt.Fprintf(os.Stderr, "GOBTYPE mb=%04d poc=%d leftNonSkip=%t topNonSkip=%t leftDirect=%t topDirect=%t ctxOffset=%d raw=%d%s\n", mbY*stride4/4+mbX, currentPOC, leftNonSkip, topNonSkip, leftIsDirect, topIsDirect, typeCtxOffset, mb.MBType, typeTrace)
	}

	if cabacUseFFmpegEdgeContexts() {
		leftCBP, topCBP = cabacUnavailableCBP(leftCBP, topCBP, mbX, mbY, false)
	}

	bMBType := mb.MBType

	// B_Direct_16x16: no ref/MV to decode.
	if bMBType == syntax.BMBTypeDirect16x16 {
		fillMVD4(mvd4, stride4, mbX*4, mbY*4, 4, 4, syntax.MotionVector{})
		if mvd4L1 != nil {
			fillMVD4(mvd4L1, stride4, mbX*4, mbY*4, 4, 4, syntax.MotionVector{})
		}
		goto decodeCBP
	}

	// B_8x8: decode sub-MB types then ref/MV per sub-partition.
	if bMBType == syntax.BMBTypeB8x8 {
		for i := 0; i < 4; i++ {
			if os.Getenv("GO264_B_TYPE_TRACE") != "" && currentPOC == 20 {
				mb.SubMBType[i] = decodeCABACBSubMBTypeTrace(dec, models, mbY*stride4/4+mbX, currentPOC, i)
			} else {
				mb.SubMBType[i] = decodeCABACBSubMBType(dec, models)
			}
		}
		// Ref-idx syntax is decoded list-by-list before MVDs, matching FFmpeg's
		// B_8x8 CABAC order. FFmpeg runs direct prediction before ref_idx decode
		// when any sub-MB is Direct, so direct sub-blocks must seed the ref/direct
		// caches used by subsequent ref_idx context derivation.
		x4, y4 := mbX*4, mbY*4
		for i, t := range mb.SubMBType {
			if t == 0 {
				bx, by := x4+(i&1)*2, y4+(i>>1)*2
				fillRef4(ref4, stride4, bx, by, 2, 2, 0)
				fillRef4(ref4L1, stride4, bx, by, 2, 2, 0)
				if direct4 != nil {
					for dy := 0; dy < 2; dy++ {
						for dx := 0; dx < 2; dx++ {
							idx := (by+dy)*stride4 + bx + dx
							if idx >= 0 && idx < len(direct4) {
								direct4[idx] = true
							}
						}
					}
				}
			}
		}
		if numRefL0 > 1 {
			for i := 0; i < 4; i++ {
				t := mb.SubMBType[i]
				if syntax.BMBSubUsesL0(t) {
					bx, by := x4+(i&1)*2, y4+(i>>1)*2
					mb.RefIdxL0[i] = int8(syntax.DecodeCABACRef(dec, models, cabacBRefIdxCtx(ref4, direct4, stride4, bx, by)))
					fillRef4(ref4, stride4, bx, by, 2, 2, mb.RefIdxL0[i])
				}
			}
		}
		if numRefL1 > 1 {
			for i := 0; i < 4; i++ {
				t := mb.SubMBType[i]
				if syntax.BMBSubUsesL1(t) {
					bx, by := x4+(i&1)*2, y4+(i>>1)*2
					mb.RefIdxL1[i] = int8(syntax.DecodeCABACRef(dec, models, cabacBRefIdxCtx(ref4L1, direct4, stride4, bx, by)))
					fillRef4(ref4L1, stride4, bx, by, 2, 2, mb.RefIdxL1[i])
				}
			}
		}
		// B_8x8 sub-MBs fill the ENTIRE sub-partition area (not just 1×1) so that
		// subsequent sub-MB amvd computations read the correct magnitude context.
		if mvd4L1 == nil || len(mvd4L1) != len(mvd4) {
			mvd4L1 = make([]syntax.MotionVector, len(mvd4))
		}
		for i := 0; i < 4; i++ {
			t := mb.SubMBType[i]
			bx, by := x4+(i&1)*2, y4+(i>>1)*2
			if t == 0 {
				// B_Direct_8x8 writes resolved direct motion into FFmpeg's MV cache
				// before later explicit B_8x8 sub-partitions call pred_motion. Until
				// colocated temporal derivation is implemented, use the same spatial
				// MVP fallback as our direct reconstruction path so following sub-MB
				// MVPs see a populated direct neighbour instead of stale zeros.
				mv0 := predictMotion4x4(mv4, ref4, stride4, bx, by, 2, mb.RefIdxL0[i])
				mv1 := predictMotion4x4(mv4L1, ref4L1, stride4, bx, by, 2, mb.RefIdxL1[i])
				mb.SubMVL0[i*4] = mv0
				mb.SubMVL1[i*4] = mv1
				fillMV4(mv4, ref4, stride4, bx, by, 2, 2, mv0, mb.RefIdxL0[i])
				fillMV4(mv4L1, ref4L1, stride4, bx, by, 2, 2, mv1, mb.RefIdxL1[i])
				fillMVD4(mvd4, stride4, bx, by, 2, 2, syntax.MotionVector{})
				fillMVD4(mvd4L1, stride4, bx, by, 2, 2, syntax.MotionVector{})
				continue
			}
			sc := syntax.BMBSubPartCount(t)
			fillW4, fillH4 := syntax.BMBSubPartFillDims(t)
			for j := 0; j < sc; j++ {
				ox4, oy4 := bSubPartOffset4x4(t, j)
				sx, sy := bx+ox4, by+oy4
				idx := i*4 + j
				if !syntax.BMBSubUsesL0(t) {
					fillMVD4(mvd4, stride4, sx, sy, fillW4, fillH4, syntax.MotionVector{})
				}
				if syntax.BMBSubUsesL0(t) {
					traceMVD := os.Getenv("GO264_B_MVD_TRACE") != ""
					amvdX, amvdY := 0, 0
					var preLow, preRange, postLow, postRange uint32
					if traceMVD {
						amvdX = cabacMVDAMVD(mvd4, stride4, sx, sy, 0)
						amvdY = cabacMVDAMVD(mvd4, stride4, sx, sy, 1)
						preLow, preRange, _ = dec.DebugState()
					}
					mvd := decodeCABACMVDPair(dec, models, mvd4, stride4, sx, sy, fillW4, fillH4)
					if traceMVD {
						postLow, postRange, _ = dec.DebugState()
					}
					mvp := predictMotion4x4(mv4, ref4, stride4, sx, sy, fillW4, mb.RefIdxL0[i])
					mb.SubMVL0[idx] = syntax.MotionVector{X: mvd.X + mvp.X, Y: mvd.Y + mvp.Y}
					if traceMVD {
						fmt.Fprintf(os.Stderr, "GOB8MVD mb=%04d sub=%d j=%d list=0 amvd={%d,%d} mvd={%d,%d} mvp={%d,%d} final={%d,%d} pre=%d/%d post=%d/%d\n", mbY*stride4/4+mbX, i, j, amvdX, amvdY, mvd.X, mvd.Y, mvp.X, mvp.Y, mb.SubMVL0[idx].X, mb.SubMVL0[idx].Y, preLow, preRange, postLow, postRange)
					}
					fillMV4(mv4, ref4, stride4, sx, sy, fillW4, fillH4, mb.SubMVL0[idx], mb.RefIdxL0[i])
				}
				if !syntax.BMBSubUsesL1(t) {
					fillMVD4(mvd4L1, stride4, sx, sy, fillW4, fillH4, syntax.MotionVector{})
				}
				if syntax.BMBSubUsesL1(t) {
					mb.SubMVL1[idx] = decodeCABACMVDPair(dec, models, mvd4L1, stride4, sx, sy, fillW4, fillH4)
					mvp := predictMotion4x4(mv4L1, ref4L1, stride4, sx, sy, fillW4, mb.RefIdxL1[i])
					mb.SubMVL1[idx].X += mvp.X
					mb.SubMVL1[idx].Y += mvp.Y
					fillMV4(mv4L1, ref4L1, stride4, sx, sy, fillW4, fillH4, mb.SubMVL1[idx], mb.RefIdxL1[i])
				}
			}
		}
		goto decodeCBP
	}

	// 16x16 / 16x8 / 8x16 partitions: determine how many partitions and which lists.
	{
		parts := cabacBPartsForType(bMBType)
		usesL0, usesL1 := cabacBListsForType(bMBType)
		x4, y4 := mbX*4, mbY*4
		traceBRef := os.Getenv("GO264_B_REF_TRACE") != "" && currentPOC == 30 && mbY*stride4/4+mbX < traceBTypeLimit
		if numRefL0 > 1 && usesL0 {
			for i := 0; i < parts; i++ {
				if cabacBPartUsesL0(bMBType, i) {
					pw, ph := cabacBPartDims(bMBType, i)
					bx, by := x4+cabacBPartX(bMBType, i, parts), y4+cabacBPartY(bMBType, i, parts)
					ctx := refCtxs[i]
					if i > 0 {
						ctx = cabacBRefIdxCtx(ref4, direct4, stride4, bx, by)
					}
					preLow, preRange, _ := dec.DebugState()
					traceTag := ""
					if traceBRef {
						traceTag = fmt.Sprintf("mb=%04d poc=%d part=%d list=0", mbY*stride4/4+mbX, currentPOC, i)
					}
					ref := syntax.DecodeCABACRefWithTrace(dec, models, ctx, traceTag)
					postLow, postRange, _ := dec.DebugState()
					mb.RefIdxL0[i] = int8(ref)
					if traceBRef {
						fmt.Fprintf(os.Stderr, "GOBREF mb=%04d poc=%d part=%d list=0 ctx=%d ref=%d pre=%d/%d post=%d/%d\n", mbY*stride4/4+mbX, currentPOC, i, ctx, ref, preLow, preRange, postLow, postRange)
					}
					fillRef4(ref4, stride4, bx, by, pw, ph, mb.RefIdxL0[i])
				}
			}
		}
		if numRefL1 > 1 && usesL1 {
			for i := 0; i < parts; i++ {
				if cabacBPartUsesL1(bMBType, i) {
					ctx := refCtxs[i]
					preLow, preRange, _ := dec.DebugState()
					traceTag := ""
					if traceBRef {
						traceTag = fmt.Sprintf("mb=%04d poc=%d part=%d list=1", mbY*stride4/4+mbX, currentPOC, i)
					}
					ref := syntax.DecodeCABACRefWithTrace(dec, models, ctx, traceTag)
					postLow, postRange, _ := dec.DebugState()
					mb.RefIdxL1[i] = int8(ref)
					if traceBRef {
						fmt.Fprintf(os.Stderr, "GOBREF mb=%04d poc=%d part=%d list=1 ctx=%d ref=%d pre=%d/%d post=%d/%d\n", mbY*stride4/4+mbX, currentPOC, i, ctx, ref, preLow, preRange, postLow, postRange)
					}
				}
			}
		}
		// MVD for L0.
		if usesL0 {
			for i := 0; i < parts; i++ {
				if !cabacBPartUsesL0(bMBType, i) {
					continue
				}
				pw, ph := cabacBPartDims(bMBType, i)
				bx, by := x4+cabacBPartX(bMBType, i, parts), y4+cabacBPartY(bMBType, i, parts)
				preLow, preRange, postLow, postRange := uint32(0), uint32(0), uint32(0), uint32(0)
				traceMVD := os.Getenv("GO264_B_MVD_TRACE") != ""
				if traceMVD {
					preLow, preRange, _ = dec.DebugState()
				}
				mb.MVL0[i], mb.AMVDL0[i] = decodeCABACMVDPairDiag(dec, models, mvd4, stride4, bx, by, pw, ph, i, 0, currentPOC)
				if traceMVD {
					postLow, postRange, _ = dec.DebugState()
					fmt.Fprintf(os.Stderr, "GOBPART_MVD_RAW mb=%04d poc=%d part=%d list=0 amvd={%d,%d} mvd={%d,%d} pre=%d/%d post=%d/%d\n", mbY*stride4/4+mbX, currentPOC, i, mb.AMVDL0[i].X, mb.AMVDL0[i].Y, mb.MVL0[i].X, mb.MVL0[i].Y, preLow, preRange, postLow, postRange)
				}
			}
		}
		// MVD for L1 uses a separate persistent cache, matching FFmpeg's per-list
		// mvd_cache. Reusing L0 corrupts amvd, while resetting every MB loses left/top
		// L1 context for Bi/list1 B partitions.
		if usesL1 {
			if mvd4L1 == nil || len(mvd4L1) != len(mvd4) {
				mvd4L1 = make([]syntax.MotionVector, len(mvd4))
			}
			for i := 0; i < parts; i++ {
				if !cabacBPartUsesL1(bMBType, i) {
					continue
				}
				pw, ph := cabacBPartDims(bMBType, i)
				bx, by := x4+cabacBPartX(bMBType, i, parts), y4+cabacBPartY(bMBType, i, parts)
				preLow, preRange, postLow, postRange := uint32(0), uint32(0), uint32(0), uint32(0)
				traceMVD := os.Getenv("GO264_B_MVD_TRACE") != ""
				if traceMVD {
					preLow, preRange, _ = dec.DebugState()
				}
				mb.MVL1[i], mb.AMVDL1[i] = decodeCABACMVDPairDiag(dec, models, mvd4L1, stride4, bx, by, pw, ph, i, 1, currentPOC)
				if traceMVD {
					postLow, postRange, _ = dec.DebugState()
					fmt.Fprintf(os.Stderr, "GOBPART_MVD_RAW mb=%04d poc=%d part=%d list=1 amvd={%d,%d} mvd={%d,%d} pre=%d/%d post=%d/%d\n", mbY*stride4/4+mbX, currentPOC, i, mb.AMVDL1[i].X, mb.AMVDL1[i].Y, mb.MVL1[i].X, mb.MVL1[i].Y, preLow, preRange, postLow, postRange)
				}
			}
		}
		// Zero MVD cache for unused lists (matches FFmpeg: unused-direction
		// partitions get fill_rectangle(..., 0, 2) in 16x8/8x16 paths).
		if !usesL0 {
			fillMVD4(mvd4, stride4, x4, y4, 4, 4, syntax.MotionVector{})
		}
		if !usesL1 && mvd4L1 != nil {
			fillMVD4(mvd4L1, stride4, x4, y4, 4, 4, syntax.MotionVector{})
		}
	}

	// Apply motion-vector predictors: add MVP to each decoded MVD to get final MV.
	// CABAC MVD context uses mvd4 above, but MVP itself comes from the neighbouring
	// final-MV cache. Using the MVD cache here loses non-zero neighbour motion and
	// breaks later B_Direct spatial prediction.
	if bMBType != syntax.BMBTypeDirect16x16 && bMBType != syntax.BMBTypeB8x8 {
		parts := cabacBPartsForType(bMBType)
		usesL0B, usesL1B := cabacBListsForType(bMBType)
		x4, y4 := mbX*4, mbY*4
		if usesL0B {
			for i := 0; i < parts; i++ {
				if !cabacBPartUsesL0(bMBType, i) {
					continue
				}
				mvd := mb.MVL0[i]
				mvp := predictBPartMotion4x4(mv4, ref4, stride4, x4, y4, bMBType, i, mb.RefIdxL0[i])
				mb.MVDL0[i] = mvd
				mb.MVPL0[i] = mvp
				mb.MVL0[i].X += mvp.X
				mb.MVL0[i].Y += mvp.Y
				if os.Getenv("GO264_B_MVD_TRACE") != "" {
					fmt.Fprintf(os.Stderr, "GOBPART_MVD mb=%04d part=%d list=0 mvd={%d,%d} mvp={%d,%d} final={%d,%d}\n", mbY*stride4/4+mbX, i, mvd.X, mvd.Y, mvp.X, mvp.Y, mb.MVL0[i].X, mb.MVL0[i].Y)
				}
				bx := x4 + cabacBPartX(bMBType, i, parts)
				by := y4 + cabacBPartY(bMBType, i, parts)
				bw, bh := cabacBPartDims(bMBType, i)
				fillMV4(mv4, ref4, stride4, bx, by, bw, bh, mb.MVL0[i], mb.RefIdxL0[i])
			}
		}
		if usesL1B {
			for i := 0; i < parts; i++ {
				if !cabacBPartUsesL1(bMBType, i) {
					continue
				}
				mvd := mb.MVL1[i]
				mvp := predictBPartMotion4x4(mv4L1, ref4L1, stride4, x4, y4, bMBType, i, mb.RefIdxL1[i])
				mb.MVDL1[i] = mvd
				mb.MVPL1[i] = mvp
				mb.MVL1[i].X += mvp.X
				mb.MVL1[i].Y += mvp.Y
				if os.Getenv("GO264_B_MVD_TRACE") != "" {
					fmt.Fprintf(os.Stderr, "GOBPART_MVD mb=%04d part=%d list=1 mvd={%d,%d} mvp={%d,%d} final={%d,%d}\n", mbY*stride4/4+mbX, i, mvd.X, mvd.Y, mvp.X, mvp.Y, mb.MVL1[i].X, mb.MVL1[i].Y)
				}
				bx := x4 + cabacBPartX(bMBType, i, parts)
				by := y4 + cabacBPartY(bMBType, i, parts)
				bw, bh := cabacBPartDims(bMBType, i)
				fillMV4(mv4L1, ref4L1, stride4, bx, by, bw, bh, mb.MVL1[i], mb.RefIdxL1[i])
			}
		}
	}

decodeCBP:
	if os.Getenv("GO264_B_CABAC_TRACE") != "" && currentPOC == 20 && mbY*stride4/4+mbX < traceBTypeLimit {
		preLow, preRange, _ := dec.DebugState()
		fmt.Fprintf(os.Stderr, "GOBB_PRE_CBP mb=%04d type=%d left=%02x top=%02x low=%d range=%d\n", mbY*stride4/4+mbX, ffBidiMBType(mb), leftCBP, topCBP, preLow, preRange)
	}
	mb.CBP = syntax.DecodeCABACCBP(dec, models, leftCBP, topCBP)
	if os.Getenv("GO264_B_CABAC_TRACE") != "" && currentPOC == 20 && mbY*stride4/4+mbX < traceBTypeLimit {
		postLow, postRange, _ := dec.DebugState()
		fmt.Fprintf(os.Stderr, "GOBB_POST_CBP mb=%04d cbp=%02x low=%d range=%d\n", mbY*stride4/4+mbX, mb.CBP, postLow, postRange)
	}
	if mb.CBP != 0 {
		// FFmpeg decodes transform_size_8x8_flag immediately after CBP and before
		// mb_qp_delta. Reading DQP first swaps the two CABAC decisions whenever an
		// inter B macroblock has luma residual and 8x8 transform enabled. FFmpeg
		// applies this to Direct16x16 too; Direct is still non-intra here.
		var nzMB [16]int
		use8x8Residual := false
		if transform8x8Mode && mb.CBP&0xF != 0 {
			if decodeCABACTransform8x8Flag(dec, models, transform8x8Ctx) {
				use8x8Residual = true
				mb.Use8x8Transform = true
			}
		}
		mb.QPDelta = int32(syntax.DecodeCABACDQP(dec, models, lastQScaleDiff))
		if use8x8Residual {
			for group := 0; group < 4; group++ {
				if mb.CBP&(1<<uint(group)) != 0 {
					nzaBlk, nzbBlk := nzCBFCtxLuma(luma4x4BlockFor8x8Group(group, 0), &nzMB, leftNZ, topNZ)
					var buf [64]int16
					tc := dec.DecodeCABACResidual(models, 5, 64, buf[:], nzaBlk, nzbBlk)
					splitLuma8x8Residual(&mb.Coeffs, group, buf)
					for sub := 0; sub < 4; sub++ {
						mb.TotalCoeff[luma4x4BlockFor8x8Group(group, sub)] = tc
						nzMB[luma4x4BlockFor8x8Group(group, sub)] = tc
					}
				}
			}
		} else {
			for blkIdx := 0; blkIdx < 16; blkIdx++ {
				group := blkIdx / 4
				if mb.CBP&(1<<uint(group)) != 0 {
					nza, nzb := nzCBFCtxLuma(blkIdx, &nzMB, leftNZ, topNZ)
					var block [16]int16
					traceRes := os.Getenv("GO264_B_RESIDUAL_TRACE") != ""
					preLow, preRange := uint32(0), uint32(0)
					if traceRes {
						preLow, preRange, _ = dec.DebugState()
					}
					tc := dec.DecodeCABACResidual(models, 2, 16, block[:], nza, nzb)
					if traceRes {
						postLow, postRange, _ := dec.DebugState()
						fmt.Fprintf(os.Stderr, "GOBRES mb=%04d poc=%d blk=%02d group=%d nza=%d nzb=%d tc=%d pre=%d/%d post=%d/%d\n", mbY*stride4/4+mbX, currentPOC, blkIdx, group, nza, nzb, tc, preLow, preRange, postLow, postRange)
					}
					mb.Coeffs[blkIdx] = block
					mb.TotalCoeff[blkIdx] = tc
					nzMB[blkIdx] = tc
				}
			}
		}
		// Decode chroma residual — mirrors the P-slice path exactly.
		// Chroma DC is a single 4-coefficient block decoded with ONE residual call;
		// the bug of 5 calls was consuming ~4× too many CABAC bins and causing
		// accumulated state drift (premature end_of_slice_flag).
		chromaCBP := (mb.CBP >> 4) & 0x3
		var nzMBChroma [2][4]int
		if chromaCBP > 0 {
			for comp := 0; comp < 2; comp++ {
				nza, nzb := cabacChromaDCCtx(comp, leftCBP, topCBP)
				var dc [4]int16
				tc := dec.DecodeCABACResidual(models, 3, 4, dc[:], nza, nzb)
				if tc > 0 {
					mb.CBP |= 0x40 << uint(comp)
				}
				// Store chroma DC for reconstruction.
				for i := range dc {
					mb.CoeffsChroma[comp][i][0] = dc[i]
				}
			}
		}
		if chromaCBP > 1 {
			for comp := 0; comp < 2; comp++ {
				for blk := 0; blk < 4; blk++ {
					nzaCBF, nzbCBF := nzCBFCtxChroma(comp, blk, &nzMBChroma, leftChromaNZ, topChromaNZ)
					var ac [16]int16
					tc := dec.DecodeCABACResidual(models, 4, 15, ac[:], nzaCBF, nzbCBF)
					for j := 1; j < 16; j++ {
						mb.CoeffsChroma[comp][blk][j] = ac[j]
					}
					mb.ChromaTotalCoeff[comp][blk] = tc
					nzMBChroma[comp][blk] = tc
				}
			}
		}
	}

	return mb, nil, false
}

// decodeCABACBSubMBType decodes one CABAC B-slice sub-MB type.
// §9.3.2.5 / FFmpeg decode_cabac_b_mb_sub_type.
func decodeCABACBSubMBType(dec *cabac.CABACDecoder, models []cabac.CABACCtx) uint32 {
	if len(models) <= 39 {
		return 0
	}
	if dec.DecodeBin(&models[36]) == 0 {
		return 0 // B_Direct_8x8
	}
	if dec.DecodeBin(&models[37]) == 0 {
		return 1 + dec.DecodeBin(&models[39]) // B_L0_8x8 or B_L1_8x8
	}
	t := uint32(3)
	if dec.DecodeBin(&models[38]) == 1 {
		if dec.DecodeBin(&models[39]) == 1 {
			return 11 + dec.DecodeBin(&models[39]) // B_L1_4x4 or B_Bi_4x4
		}
		t += 4
	}
	t += 2 * dec.DecodeBin(&models[39])
	t += dec.DecodeBin(&models[39])
	return t
}

func decodeCABACBSubMBTypeTrace(dec *cabac.CABACDecoder, models []cabac.CABACCtx, mbAddr, currentPOC, subIdx int) uint32 {
	if len(models) <= 39 {
		return 0
	}
	trace := ""
	decode := func(ctx int) int {
		preLow, preRange, _ := dec.DebugState()
		preState := models[ctx].DebugPackedState()
		bin := dec.DecodeBin(&models[ctx])
		postLow, postRange, _ := dec.DebugState()
		trace += fmt.Sprintf(" ctx=%d state=%d pre=%d/%d bin=%d post_state=%d post=%d/%d", ctx, preState, preLow, preRange, bin, models[ctx].DebugPackedState(), postLow, postRange)
		return int(bin)
	}
	var typ uint32
	if decode(36) == 0 {
		typ = 0
	} else if decode(37) == 0 {
		typ = uint32(1 + decode(39))
	} else {
		t := 3
		if decode(38) == 1 {
			if decode(39) == 1 {
				typ = uint32(11 + decode(39))
				fmt.Fprintf(os.Stderr, "GOBSUBTYPE mb=%04d poc=%d sub=%d type=%d%s\n", mbAddr, currentPOC, subIdx, typ, trace)
				return typ
			}
			t += 4
		}
		t += 2 * decode(39)
		t += decode(39)
		typ = uint32(t)
	}
	fmt.Fprintf(os.Stderr, "GOBSUBTYPE mb=%04d poc=%d sub=%d type=%d%s\n", mbAddr, currentPOC, subIdx, typ, trace)
	return typ
}

// cabacBPartsForType returns the number of motion-vector partitions for a B MB type.
func cabacBPartsForType(t uint32) int {
	if t >= 4 && t <= 21 {
		return 2
	}
	return 1
}

// cabacBListsForType returns whether any partition of a B MB type uses L0/L1.
func cabacBListsForType(t uint32) (usesL0, usesL1 bool) {
	parts := cabacBPartsForType(t)
	for part := 0; part < parts; part++ {
		usesL0 = usesL0 || cabacBPartUsesL0(t, part)
		usesL1 = usesL1 || cabacBPartUsesL1(t, part)
	}
	return usesL0, usesL1
}

func cabacBPartDims(t uint32, part int) (w, h int) {
	if t >= 4 && t <= 21 {
		if cabacBIs8x16(t) {
			return 2, 4
		}
		return 4, 2
	}
	return 4, 4
}

func cabacBPartX(t uint32, part, nParts int) int {
	if nParts == 2 && cabacBIs8x16(t) {
		return part * 2
	}
	return 0
}

func cabacBPartY(t uint32, part, nParts int) int {
	if nParts == 2 && !cabacBIs8x16(t) {
		return part * 2
	}
	return 0
}

func cabacBIs8x16(t uint32) bool {
	return t == 5 || t == 7 || t == 9 || t == 11 || t == 13 || t == 15 || t == 17 || t == 19 || t == 21
}

func cabacBPartUsesL0(t uint32, part int) bool { return syntax.BPartUsesL0(t, part) }
func cabacBPartUsesL1(t uint32, part int) bool { return syntax.BPartUsesL1(t, part) }
