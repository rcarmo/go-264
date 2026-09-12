package cabac

import (
	"fmt"
	"os"
)

// CABAC residual coefficient decoding.
// Implements decode_cabac_residual_internal from FFmpeg h264_cabac.c.
// ITU-T H.264 §9.3.3.1 (significant-coeff-flag and coeff_abs_level binarization).

// H.264 coefficient scan tables (scan position → matrix row-major index).
// Coefficients are written at out[scan[pos]] so output is in the same
// matrix-order format as the CAVLC decoder (ready for IDCT without reordering).
var cabacScan4x4 = [16]int{
	0, 1, 4, 8,
	5, 2, 3, 6,
	9, 12, 13, 10,
	7, 11, 14, 15,
}

var cabacScan8x8 = [64]int{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

var cabacScanChromaDC = [4]int{0, 1, 2, 3}

// Context base offsets for significant coeff flags per category (non-field mode).
// Source: FFmpeg libavcodec/h264_cabac.c significant_coeff_flag_offset[0][cat]
var cabacSigCoeffFlagOffset = [14]int{
	105, 120, 134, 149, 152, 402, 484, 499, 513, 660, 528, 543, 557, 718,
}

// Context base offsets for last-significant coeff flags per category.
// Source: FFmpeg last_coeff_flag_offset[0][cat]
var cabacLastCoeffFlagOffset = [14]int{
	166, 181, 195, 210, 213, 417, 572, 587, 601, 690, 616, 631, 645, 748,
}

// Context base offsets for coeff absolute level minus 1 per category.
// Source: FFmpeg coeff_abs_level_m1_offset[cat]
var cabacCoeffAbsLevelOffset = [14]int{
	227, 237, 247, 257, 266, 426, 952, 962, 972, 708, 982, 992, 1002, 766,
}

// coeff_abs_level1_ctx: ctx offset for abs == 1 bin (relative to level base).
// Source: FFmpeg coeff_abs_level1_ctx[8]
var cabacLevel1Ctx = [8]uint8{1, 2, 3, 4, 0, 0, 0, 0}

// coeff_abs_levelgt1_ctx: ctx offset for abs > 1 (non-DC-422).
// Source: FFmpeg coeff_abs_levelgt1_ctx[0][8]
var cabacLevelGT1Ctx = [8]uint8{5, 5, 5, 5, 6, 7, 8, 9}

// coeff_abs_level_transition: node context update tables.
// [0] after abs==1, [1] after abs>1.
// Source: FFmpeg coeff_abs_level_transition[2][8]
var cabacLevelTransition = [2][8]uint8{
	{1, 2, 3, 3, 4, 5, 6, 7},
	{4, 4, 4, 4, 5, 6, 7, 7},
}

// significant_coeff_flag_offset_8x8 for cat5 (luma 8x8, non-field).
// Source: FFmpeg significant_coeff_flag_offset_8x8[0][63]
var cabacSigCoeff8x8 = [63]uint8{
	0, 1, 2, 3, 4, 5, 5, 4, 4, 3, 3, 4, 4, 4, 5, 5,
	4, 4, 4, 4, 3, 3, 6, 7, 7, 7, 8, 9, 10, 9, 8, 7,
	7, 6, 11, 12, 13, 11, 6, 7, 8, 9, 14, 10, 9, 8, 6, 11,
	12, 13, 11, 6, 9, 14, 10, 9, 11, 12, 13, 11, 14, 10, 12,
}

// h264LastCoeffFlagOffset8x8: last coeff flag offsets for 8x8 blocks.
// Source: FFmpeg ff_h264_last_coeff_flag_offset_8x8[63]
var cabacLastCoeff8x8 = [63]uint8{
	0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	3, 3, 3, 3, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4,
	5, 5, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7, 8, 8, 8,
}

func validResidualCoeffCount(cat, maxCoeff int) bool {
	switch cat {
	case 1, 4: // AC-only 4x4 blocks skip DC
		return maxCoeff == 15
	case 3: // chroma DC for 4:2:0
		return maxCoeff == 4
	case 5: // luma 8x8 transform
		return maxCoeff == 64
	default:
		return maxCoeff == 16
	}
}

// DecodeCABACResidual decodes one residual block using CABAC context models.
//
//   - cat: residual category (0=luma DC, 1=luma AC/I16, 2=luma 4x4, 3=chroma DC, 4=chroma AC, 5=luma 8x8)
//   - maxCoeff: number of coefficients to decode (16 for 4x4, 4 for chroma DC, 15 for AC, 64 for 8x8)
//   - out: slice of length >= maxCoeff; coefficients are written in MATRIX (row-major) order matching CAVLC.
//   - nza, nzb: left/top neighbour nonzero flags for coded_block_flag context (0 or 1 each)
//
// Returns number of nonzero coefficients (totalCoeff).
// Decodes coded_block_flag first; if 0, returns 0 without reading more bins.
func (d *CABACDecoder) DecodeCABACResidual(models []CABACCtx, cat, maxCoeff int, out []int16, nza, nzb int) int {
	if d == nil || maxCoeff <= 0 || maxCoeff > 64 || len(models) < 1024 || len(out) < maxCoeff {
		return 0
	}
	if cat < 0 || cat >= 14 || !validResidualCoeffCount(cat, maxCoeff) {
		return 0
	}
	if nza != 0 {
		nza = 1
	}
	if nzb != 0 {
		nzb = 1
	}

	sigBase := cabacSigCoeffFlagOffset[cat]
	lastBase := cabacLastCoeffFlagOffset[cat]
	levelBase := cabacCoeffAbsLevelOffset[cat]

	is8x8 := cat == 5

	// ---- Step 0: coded_block_flag ----
	// CBF context: ctx = (nza>0) + 2*(nzb>0), base from cabacCBFBase[cat].
	// For cat 5 (8x8 DCT), CBF is not separately decoded per block.
	// Source: FFmpeg decode_cabac_residual_dc/nondc → get_cabac_cbf_ctx.
	traceResidual := os.Getenv("GO264_CABAC_RESIDUAL_TRACE") != ""
	if !is8x8 {
		cbfBase := 0
		switch cat {
		case 0:
			cbfBase = 85 // luma DC
		case 1:
			cbfBase = 89 // luma AC I16x16
		case 2:
			cbfBase = 93 // luma 4x4
		case 3:
			cbfBase = 97 // chroma DC
		case 4:
			cbfBase = 101 // chroma AC
		default:
			cbfBase = 93
		}
		cbfCtx := cbfBase + nza + 2*nzb
		preLow, preRange, _ := d.DebugState()
		preState := models[cbfCtx].DebugPackedState()
		cbfBin := d.DecodeBin(&models[cbfCtx])
		postLow, postRange, _ := d.DebugState()
		if traceResidual {
			fmt.Fprintf(os.Stderr, "GORES event=cbf cat=%d max=%d nza=%d nzb=%d ctx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d\n", cat, maxCoeff, nza, nzb, cbfCtx, preState, preLow, preRange, cbfBin, models[cbfCtx].DebugPackedState(), postLow, postRange)
		}
		if cbfBin == 0 {
			return 0 // coded_block_flag = 0
		}
	}
	var index [64]int
	coeffCount := 0

	if is8x8 {
		for last := 0; last < 63; last++ {
			sigCtxIdx := sigBase + int(cabacSigCoeff8x8[last])
			if d.DecodeBin(&models[sigCtxIdx]) == 1 {
				index[coeffCount] = last
				coeffCount++
				lastCtxIdx := lastBase + int(cabacLastCoeff8x8[last])
				if d.DecodeBin(&models[lastCtxIdx]) == 1 {
					goto decode_levels
				}
			}
		}
		// Position 63 is added unconditionally (only safe after 8x8 CBF=1).
		index[coeffCount] = 63
		coeffCount++
	} else {
		// Scan positions 0..maxCoeff-2 via sig_ctx / last_ctx.
		for last := 0; last < maxCoeff-1; last++ {
			sigCtxIdx := sigBase + last
			preLow, preRange, _ := d.DebugState()
			preState := models[sigCtxIdx].DebugPackedState()
			sigBin := d.DecodeBin(&models[sigCtxIdx])
			postLow, postRange, _ := d.DebugState()
			if traceResidual {
				fmt.Fprintf(os.Stderr, "GORES event=sigbin cat=%d max=%d pos=%d ctx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d\n", cat, maxCoeff, last, sigCtxIdx, preState, preLow, preRange, sigBin, models[sigCtxIdx].DebugPackedState(), postLow, postRange)
			}
			if sigBin == 1 {
				index[coeffCount] = last
				coeffCount++
				lastCtxIdx := lastBase + last
				preLow, preRange, _ = d.DebugState()
				preState = models[lastCtxIdx].DebugPackedState()
				lastBin := d.DecodeBin(&models[lastCtxIdx])
				postLow, postRange, _ = d.DebugState()
				if traceResidual {
					fmt.Fprintf(os.Stderr, "GORES event=lastbin cat=%d max=%d pos=%d ctx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d\n", cat, maxCoeff, last, lastCtxIdx, preState, preLow, preRange, lastBin, models[lastCtxIdx].DebugPackedState(), postLow, postRange)
				}
				if lastBin == 1 {
					goto decode_levels
				}
			}
		}
		// H.264 spec §9.3.3.1.3: position maxCoeff-1 is implicitly significant when
		// the scan loop exhausts without an early last_significant_coeff_flag break.
		// No sig_flag bin is emitted for this position; add it unconditionally.
		// Safe because CBF=1 (decoded above) guarantees at least one nonzero coeff.
		index[coeffCount] = maxCoeff - 1
		coeffCount++
	}

decode_levels:
	if traceResidual {
		traceResidualIndices(cat, maxCoeff, index[:coeffCount])
	}
	if coeffCount == 0 {
		return 0
	}

	// Choose the scan table to convert scan position → matrix row-major index.
	// cat=0,2: 4x4 scan from position 0 (includes DC).
	// cat=1,4: 4x4 scan from position 1 (AC-only, skips DC slot 0); matches FFmpeg `scan+1`.
	// cat=3: chroma DC, identity (4 positions, raster).
	// cat=5: 8x8 scan from position 0.
	var scanTable []int
	switch cat {
	case 0, 2: // luma DC and luma 4x4: full 4x4 scan
		scanTable = cabacScan4x4[:maxCoeff]
	case 1, 4: // luma AC and chroma AC: skip DC slot, start from scan pos 1
		scanTable = cabacScan4x4[1 : 1+maxCoeff]
	case 3: // chroma DC: raster (identity)
		scanTable = cabacScanChromaDC[:]
	case 5: // luma 8x8: full 8x8 scan
		scanTable = cabacScan8x8[:]
	default:
		scanTable = cabacScan4x4[:maxCoeff]
	}

	// ---- Step 2: coefficient levels in reverse scan order ----
	nodeCtx := 0
	var decodedLevels *[64]int16
	var decodedScanPos *[64]int
	var decodedMatrixPos *[64]int
	decodedCount := 0
	if traceResidual {
		decodedLevels = &[64]int16{}
		decodedScanPos = &[64]int{}
		decodedMatrixPos = &[64]int{}
	}
	for i := coeffCount - 1; i >= 0; i-- {
		scanPos := index[i]
		matrixPos := scanTable[scanPos] // convert to matrix order

		level1CtxIdx := levelBase + int(cabacLevel1Ctx[nodeCtx])
		if d.DecodeBin(&models[level1CtxIdx]) == 0 {
			// abs level == 1
			nodeCtx = int(cabacLevelTransition[0][nodeCtx])
			if d.DecodeBypass() == 1 {
				out[matrixPos] = -1
			} else {
				out[matrixPos] = 1
			}
		} else {
			// abs level >= 2
			coeffAbs := 2
			gtCtxIdx := levelBase + int(cabacLevelGT1Ctx[nodeCtx])
			nodeCtx = int(cabacLevelTransition[1][nodeCtx])
			for coeffAbs < 15 && d.DecodeBin(&models[gtCtxIdx]) == 1 {
				coeffAbs++
			}
			if coeffAbs >= 15 {
				j := 0
				for d.DecodeBypass() == 1 && j < 23 {
					j++
				}
				coeffAbs = 1
				for k := j - 1; k >= 0; k-- {
					coeffAbs = (coeffAbs << 1) | int(d.DecodeBypass())
				}
				coeffAbs += 14
			}
			if d.DecodeBypass() == 1 {
				out[matrixPos] = int16(-coeffAbs)
			} else {
				out[matrixPos] = int16(coeffAbs)
			}
		}
		if traceResidual {
			decodedScanPos[decodedCount] = scanPos
			decodedMatrixPos[decodedCount] = matrixPos
			decodedLevels[decodedCount] = out[matrixPos]
			decodedCount++
		}
	}
	if traceResidual {
		fmt.Fprintf(os.Stderr, "GORES event=levelseq cat=%d max=%d count=%d seq=[", cat, maxCoeff, decodedCount)
		for i := 0; i < decodedCount; i++ {
			if i > 0 {
				fmt.Fprint(os.Stderr, " ")
			}
			fmt.Fprintf(os.Stderr, "%d:%d", decodedScanPos[i], decodedLevels[i])
		}
		fmt.Fprintln(os.Stderr, "]")
		fmt.Fprintf(os.Stderr, "GORES event=matrixseq cat=%d max=%d count=%d seq=[", cat, maxCoeff, decodedCount)
		for i := 0; i < decodedCount; i++ {
			if i > 0 {
				fmt.Fprint(os.Stderr, " ")
			}
			fmt.Fprintf(os.Stderr, "%d:%d", decodedScanPos[i], decodedMatrixPos[i])
		}
		fmt.Fprintln(os.Stderr, "]")
		traceResidualLevels(cat, maxCoeff, coeffCount, out[:maxCoeff])
	}
	return coeffCount
}

// Copy only when tracing is enabled. Passing the caller's scratch directly to
// fmt makes it escape even in ordinary trace-disabled decoding.
func traceResidualIndices(cat, maxCoeff int, indices []int) {
	copyIndices := append([]int(nil), indices...)
	fmt.Fprintf(os.Stderr, "GORES event=sig cat=%d max=%d count=%d idx=%v\n", cat, maxCoeff, len(indices), copyIndices)
}

func traceResidualLevels(cat, maxCoeff, coeffCount int, levels []int16) {
	copyLevels := append([]int16(nil), levels...)
	fmt.Fprintf(os.Stderr, "GORES event=levels cat=%d max=%d count=%d out=%v\n", cat, maxCoeff, coeffCount, copyLevels)
}
