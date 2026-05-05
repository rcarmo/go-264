package slice

import (
	"github.com/rcarmo/go-264/entropy"
	"github.com/rcarmo/go-264/nal"
)

// Macroblock types for I-slices (ITU-T H.264 Table 7-11)
const (
	MBTypeINxN      = 0  // Intra_4x4 or Intra_8x8
	MBTypeI16x16_0  = 1  // Intra_16x16 (pred=0, CBP luma=0, CBP chroma=0)
	MBTypeI16x16_25 = 25 // last I_16x16 variant
	MBTypeIPCM      = 25 // I_PCM (raw samples)
)

// MBIntra describes a decoded intra macroblock.
type MBIntra struct {
	MBType             uint32
	IntraPredMode      [16]int8 // 4x4 prediction modes (if MBTypeINxN)
	Intra16x16PredMode int8
	CodedBlockPattern  uint32 // CBP
	ChromaPredMode     int8
	QPDelta            int32
	Coeffs             [16][16]int16   // 4x4 luma blocks in raster scan
	CoeffsChroma       [2][4][16]int16 // chroma blocks [U/V][4 blocks][16 coeffs]
	TotalCoeff         [16]int         // CAVLC totalCoeff per luma 4x4 block (for nC context)
	ChromaTotalCoeff   [2][4]int       // CAVLC totalCoeff per chroma 4x4 block [U/V][block]
}

// DecodeMBIntra decodes one intra macroblock from the bitstream.
// Returns the macroblock data needed for reconstruction.
func DecodeMBIntra(r *nal.Reader, sliceQP int32, ppsEntropy uint32, transform8x8 bool) *MBIntra {
	return DecodeMBIntraCtx(r, sliceQP, ppsEntropy, transform8x8, nil, nil)
}

// DecodeMBIntraCtx decodes an intra macroblock with optional left/top nC
// context from neighbouring macroblocks. leftNZ/topNZ are indexed by the H.264
// 4x4 block index within the neighbouring macroblock.
func DecodeMBIntraCtx(r *nal.Reader, sliceQP int32, ppsEntropy uint32, transform8x8 bool, leftNZ, topNZ *[16]int) *MBIntra {
	return DecodeMBIntraCtxFull(r, sliceQP, ppsEntropy, transform8x8, leftNZ, topNZ, nil, nil)
}

func DecodeMBIntraCtxFull(r *nal.Reader, sliceQP int32, ppsEntropy uint32, transform8x8 bool, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int) *MBIntra {
	mbType := r.ReadUE()
	return DecodeMBIntraCtxWithTypeFull(r, mbType, sliceQP, ppsEntropy, transform8x8, leftNZ, topNZ, leftChromaNZ, topChromaNZ)
}

// DecodeMBIntraCtxWithType decodes the intra macroblock payload after the
// caller has already consumed an enclosing slice-specific mb_type. P/B slices
// encode intra macroblock types as offsets from the inter type range.
func DecodeMBIntraCtxWithType(r *nal.Reader, mbType uint32, sliceQP int32, ppsEntropy uint32, transform8x8 bool, leftNZ, topNZ *[16]int) *MBIntra {
	return DecodeMBIntraCtxWithTypeFull(r, mbType, sliceQP, ppsEntropy, transform8x8, leftNZ, topNZ, nil, nil)
}

func DecodeMBIntraCtxWithTypeFull(r *nal.Reader, mbType uint32, sliceQP int32, ppsEntropy uint32, transform8x8 bool, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int) *MBIntra {
	mb := &MBIntra{MBType: mbType}

	if mb.MBType == 0 {
		// I_NxN: always 16 prediction modes (4x4 block-level modes)
		numModes := 16
		for i := 0; i < numModes; i++ {
			if r.ReadBool() { // prev_intra_pred_mode_flag
				mb.IntraPredMode[i] = -1 // use predicted mode
			} else {
				mb.IntraPredMode[i] = int8(r.ReadBits(3)) // rem_intra_pred_mode
			}
		}
	} else if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: prediction mode, CBP coded in mb_type
		mb.Intra16x16PredMode = int8((mb.MBType - 1) % 4)
		cbpChroma := (mb.MBType - 1) / 4 % 3
		cbpLuma := uint32(0)
		if (mb.MBType-1)/12 > 0 {
			cbpLuma = 15
		}
		mb.CodedBlockPattern = cbpLuma | (cbpChroma << 4)
	}

	// Chroma intra pred mode (for NxN and 16x16)
	if mb.MBType != MBTypeIPCM {
		mb.ChromaPredMode = int8(r.ReadUE()) // intra_chroma_pred_mode
	}

	// Coded block pattern (only for I_NxN, I_16x16 has it in mb_type)
	if mb.MBType == 0 {
		mb.CodedBlockPattern = decodeCBPIntra(r)
	}

	use8x8 := false
	if transform8x8 && mb.MBType == 0 && (mb.CodedBlockPattern&0xF) != 0 {
		use8x8 = r.ReadBool()
	}

	// QP delta
	if mb.CodedBlockPattern > 0 || (mb.MBType >= 1 && mb.MBType <= 24) {
		mb.QPDelta = r.ReadSE()
	}

	if ppsEntropy == 0 {
		if mb.MBType >= 1 && mb.MBType <= 24 {
			// I_16x16: decode DC block (16 DC coefficients) via CAVLC
			// nC = -1 signals DC block (uses special ChromaDC-like table)
			// For simplicity, use nC=0
			dcBlock, _ := entropy.DecodeCAVLCBlock(r, 0)
			for i := 0; i < 16; i++ {
				mb.Coeffs[i][0] = dcBlock[i]
			}
			// Decode AC coefficients for each 4x4 block if CBP indicates
			cbpLuma := mb.CodedBlockPattern & 0xF
			if cbpLuma != 0 {
				for blk := 0; blk < 16; blk++ {
					acBlock, tc := entropy.DecodeCAVLCBlockAC(r, 0)
					// AC coefficients are already positioned at raster slots 1..15.
					for j := 1; j < 16; j++ {
						mb.Coeffs[blk][j] = acBlock[j]
					}
					mb.TotalCoeff[blk] = tc
				}
			}
		} else if mb.MBType == 0 && mb.CodedBlockPattern > 0 {
			cbpLuma := mb.CodedBlockPattern & 0xF
			var nzCoeffs [16]int
			if use8x8 {
				// I_8x8: each 8x8 block decoded as 4 sub-blocks
				for blk8 := 0; blk8 < 4; blk8++ {
					if cbpLuma&(1<<uint(blk8)) != 0 {
						// 4 sub-blocks per 8x8 block
						for sub := 0; sub < 4; sub++ {
							blk4 := blk8*4 + sub
							nC := computeNC4x4Ctx(blk4, nzCoeffs[:], leftNZ, topNZ)
							block, tc := entropy.DecodeCAVLCBlock(r, nC)
							mb.Coeffs[blk4] = [16]int16(block)
							nzCoeffs[blk4] = tc
							mb.TotalCoeff[blk4] = tc
						}
					}
				}
			} else {
				// I_4x4: decode each 4x4 block independently
				for blk := 0; blk < 16; blk++ {
					group := blk / 4
					if cbpLuma&(1<<uint(group)) != 0 {
						nC := computeNC4x4Ctx(blk, nzCoeffs[:], leftNZ, topNZ)
						block, tc := entropy.DecodeCAVLCBlock(r, nC)
						mb.Coeffs[blk] = [16]int16(block)
						nzCoeffs[blk] = tc
						mb.TotalCoeff[blk] = tc
					}
				}
			}
		}
	}

	// Decode chroma residual if CBP indicates
	cbpChroma := mb.CodedBlockPattern >> 4
	if ppsEntropy == 0 && cbpChroma > 0 {
		// Chroma DC: 2×2 block for each Cb and Cr
		for comp := 0; comp < 2; comp++ {
			dcBlock4 := entropy.DecodeCAVLCChromaDC(r)
			// Store DC values (only first 4 from 4x4 block)
			for i := 0; i < 4; i++ {
				mb.CoeffsChroma[comp][i][0] = dcBlock4[i]
			}
		}
		// Chroma AC (if cbpChroma == 2). Chroma has its own 2x2
		// neighbouring nC context per component.
		if cbpChroma == 2 {
			for comp := 0; comp < 2; comp++ {
				var nzChroma [4]int
				for blk := 0; blk < 4; blk++ {
					nC := computeNCChroma4x4Ctx(blk, nzChroma[:], leftChromaNZ, topChromaNZ, comp)
					acBlock, tc := entropy.DecodeCAVLCBlockAC(r, nC)
					for j := 1; j < 16; j++ {
						mb.CoeffsChroma[comp][blk][j] = acBlock[j]
					}
					nzChroma[blk] = tc
					mb.ChromaTotalCoeff[comp][blk] = tc
				}
			}
		}
	}

	return mb
}

// decodeCBPIntra decodes coded_block_pattern for intra macroblocks.
// Uses Table 9-4 mapping from codeNum to CBP.
func decodeCBPIntra(r *nal.Reader) uint32 {
	codeNum := r.ReadUE()
	// Table 9-4: Intra CBP mapping (subset)
	cbpIntraTable := [48]uint32{
		47, 31, 15, 0, 23, 27, 29, 30, 7, 11, 13, 14, 39, 43, 45, 46,
		16, 3, 5, 10, 12, 19, 21, 26, 28, 35, 37, 42, 44, 1, 2, 4,
		8, 17, 18, 20, 24, 6, 9, 22, 25, 32, 33, 34, 36, 40, 38, 41,
	}
	if codeNum < 48 {
		return cbpIntraTable[codeNum]
	}
	return 0
}

// computeNC4x4 computes the nC context for a 4x4 block within a macroblock.
// Uses the totalCoeff of the left and top neighboring 4x4 blocks.
// Block layout within MB (H.264 raster scan §6.4.3):
//
//	0  1  4  5
//	2  3  6  7
//	8  9 12 13
//
// 10 11 14 15
var blk4x4ToX = [16]int{0, 1, 0, 1, 2, 3, 2, 3, 0, 1, 0, 1, 2, 3, 2, 3}
var blk4x4ToY = [16]int{0, 0, 1, 1, 0, 0, 1, 1, 2, 2, 3, 3, 2, 2, 3, 3}
var xyToBlk4x4 = [4][4]int{
	{0, 1, 4, 5},
	{2, 3, 6, 7},
	{8, 9, 12, 13},
	{10, 11, 14, 15},
}

func computeNC4x4(blkIdx int, nz []int) int {
	return computeNC4x4Ctx(blkIdx, nz, nil, nil)
}

func computeNC4x4Ctx(blkIdx int, nz []int, leftNZ, topNZ *[16]int) int {
	x := blk4x4ToX[blkIdx]
	y := blk4x4ToY[blkIdx]
	nA, nB := -1, -1
	if x > 0 {
		nA = nz[xyToBlk4x4[y][x-1]]
	} else if leftNZ != nil {
		nA = leftNZ[xyToBlk4x4[y][3]]
	}
	if y > 0 {
		nB = nz[xyToBlk4x4[y-1][x]]
	} else if topNZ != nil {
		nB = topNZ[xyToBlk4x4[3][x]]
	}
	return combineNC(nA, nB)
}

func computeNCChroma4x4Ctx(blkIdx int, nz []int, leftNZ, topNZ *[2][4]int, comp int) int {
	x := blkIdx & 1
	y := blkIdx >> 1
	nA, nB := -1, -1
	if x > 0 {
		nA = nz[blkIdx-1]
	} else if leftNZ != nil {
		nA = leftNZ[comp][y*2+1]
	}
	if y > 0 {
		nB = nz[blkIdx-2]
	} else if topNZ != nil {
		nB = topNZ[comp][2+x]
	}
	return combineNC(nA, nB)
}

func combineNC(nA, nB int) int {
	if nA >= 0 && nB >= 0 {
		return (nA + nB + 1) >> 1
	}
	if nA >= 0 {
		return nA
	}
	if nB >= 0 {
		return nB
	}
	return 0
}
