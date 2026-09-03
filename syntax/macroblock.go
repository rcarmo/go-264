package syntax

import (
	cavlc "github.com/rcarmo/go-264/entropy/cavlc"
	"github.com/rcarmo/go-264/nal"
)

// Macroblock types for I-slices (ITU-T H.264 Table 7-11)
const (
	MBTypeINxN      = 0  // Intra_4x4 or Intra_8x8
	MBTypeI16x16_0  = 1  // Intra_16x16 (pred=0, CBP luma=0, CBP chroma=0)
	MBTypeI16x16_24 = 24 // last I_16x16 variant
	MBTypeIPCM      = 25 // I_PCM (raw samples)
)

// H.264 4x4 block scan-order index maps (§6.4.3).
// Blk4x4Col/Row: column/row index (0-3) within the 4x4 MB grid for each blkIdx.
// BlkXYToIdx: inverse map — BlkXYToIdx[row][col] → blkIdx.
// Matches FFmpeg's blk4x4ToX/Y and xyToBlk4x4 conventions.
var Blk4x4Col = [16]int{0, 1, 0, 1, 2, 3, 2, 3, 0, 1, 0, 1, 2, 3, 2, 3}
var Blk4x4Row = [16]int{0, 0, 1, 1, 0, 0, 1, 1, 2, 2, 3, 3, 2, 2, 3, 3}
var BlkXYToIdx = [4][4]int{
	{0, 1, 4, 5},
	{2, 3, 6, 7},
	{8, 9, 12, 13},
	{10, 11, 14, 15},
}

// MBIntra describes a decoded intra macroblock.
type MBIntra struct {
	MBType             uint32
	IntraPredMode      [16]int8 // 4x4 prediction modes (if MBTypeINxN, I4x4)
	I8x8PredMode       [4]int8  // 8x8 prediction modes (if MBTypeINxN + Use8x8Transform)
	Use8x8Transform    bool     // true if I_NxN block uses 8x8 DCT (High profile)
	Intra16x16PredMode int8
	CodedBlockPattern  uint32 // CBP
	ChromaPredMode     int8
	QPDelta            int32
	Coeffs             [16][16]int16   // 4x4 luma blocks in raster scan
	CoeffsChroma       [2][4][16]int16 // chroma blocks [U/V][4 blocks][16 coeffs]
	TotalCoeff         [16]int         // CAVLC totalCoeff per luma 4x4 block (for nC context)
	ChromaTotalCoeff   [2][4]int       // CAVLC totalCoeff per chroma 4x4 block [U/V][block]
	LumaDCTotalCoeff   int             // totalCoeff of luma DC block (for I16x16 dcNC context)
	PCMY               [256]uint8      // I_PCM luma samples, raster order
	PCMCb              [64]uint8       // I_PCM Cb samples for 4:2:0 streams
	PCMCr              [64]uint8       // I_PCM Cr samples for 4:2:0 streams
}

// IntraDecodeOpts carries context for CAVLC intra macroblock decoding.
// Zero-value is safe (no neighbour context, QP=0, no 8x8 transform).
type IntraDecodeOpts struct {
	SliceQP      int32
	Transform8x8 bool
	LeftNZ       *[16]int
	TopNZ        *[16]int
	LeftChromaNZ *[2][4]int
	TopChromaNZ  *[2][4]int
}

// DecodeMBIntra reads mb_type from the bitstream and decodes one CAVLC intra
// macroblock. Use DecodeMBIntraWithType when mb_type was already consumed by
// the caller (e.g. intra MBs embedded in a P-slice).
func DecodeMBIntra(r *nal.Reader, opts IntraDecodeOpts) *MBIntra {
	if r == nil {
		return &MBIntra{}
	}
	mbType := r.ReadUE()
	return DecodeMBIntraWithType(r, mbType, opts)
}

// DecodeMBIntraWithType decodes the intra macroblock payload after the caller
// has already consumed an enclosing slice-specific mb_type. P/B slices encode
// intra macroblock types as offsets from the inter type range.
func DecodeMBIntraWithType(r *nal.Reader, mbType uint32, opts IntraDecodeOpts) *MBIntra {
	mb := &MBIntra{MBType: mbType}
	if r == nil {
		return mb
	}
	if mbType > 25 {
		r.Fail(nal.ErrInvalidSyntax)
		return mb
	}
	leftNZ, topNZ := opts.LeftNZ, opts.TopNZ
	leftChromaNZ, topChromaNZ := opts.LeftChromaNZ, opts.TopChromaNZ

	if mb.MBType == MBTypeIPCM {
		decodeIPCMSamples(r, mb)
		return mb
	}

	if mb.MBType == 0 {
		// I_NxN: 16 prediction modes (4x4 block-level modes)
		for i := 0; i < 16; i++ {
			if r.ReadBool() {
				mb.IntraPredMode[i] = -1 // prev_intra_pred_mode_flag: use predicted
			} else {
				mb.IntraPredMode[i] = int8(r.ReadBits(3)) // rem_intra_pred_mode
			}
		}
	} else if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: prediction mode and CBP coded in mb_type
		mb.Intra16x16PredMode = int8((mb.MBType - 1) % 4)
		cbpChroma := (mb.MBType - 1) / 4 % 3
		cbpLuma := uint32(0)
		if (mb.MBType-1)/12 > 0 {
			cbpLuma = 15
		}
		mb.CodedBlockPattern = cbpLuma | (cbpChroma << 4)
	}

	// Chroma intra pred mode
	if mb.MBType != MBTypeIPCM {
		mb.ChromaPredMode = int8(r.ReadUEBounded(3))
	}

	// Coded block pattern (only for I_NxN, I_16x16 has it in mb_type)
	if mb.MBType == 0 {
		mb.CodedBlockPattern = decodeCBPIntra(r)
	}

	use8x8 := false
	if opts.Transform8x8 && mb.MBType == 0 && (mb.CodedBlockPattern&0xF) != 0 {
		use8x8 = r.ReadBool()
	}
	mb.Use8x8Transform = use8x8

	// QP delta
	if mb.CodedBlockPattern > 0 || (mb.MBType >= 1 && mb.MBType <= 24) {
		mb.QPDelta = r.ReadSEBounded(-26, 25)
	}

	// CAVLC residual decode
	if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: decode the 16 luma DC coefficients as a separate CAVLC block.
		dcNC := computeNCLumaDC(leftNZ, topNZ)
		dcBlock, dcTC := cavlc.DecodeCAVLCBlock(r, dcNC)
		mb.LumaDCTotalCoeff = dcTC
		for pos := 0; pos < 16; pos++ {
			blk := BlkXYToIdx[pos/4][pos%4]
			mb.Coeffs[blk][0] = dcBlock[pos]
		}
		cbpLuma := mb.CodedBlockPattern & 0xF
		if cbpLuma != 0 {
			var nzCoeffs [16]int
			for blk := 0; blk < 16; blk++ {
				nC := computeNC4x4Ctx(blk, nzCoeffs[:], leftNZ, topNZ)
				acBlock, tc := cavlc.DecodeCAVLCBlockAC(r, nC)
				for j := 1; j < 16; j++ {
					mb.Coeffs[blk][j] = acBlock[j]
				}
				nzCoeffs[blk] = tc
				mb.TotalCoeff[blk] = tc
			}
		}
	} else if mb.MBType == 0 && mb.CodedBlockPattern > 0 {
		cbpLuma := mb.CodedBlockPattern & 0xF
		var nzCoeffs [16]int
		if use8x8 {
			for blk8 := 0; blk8 < 4; blk8++ {
				if cbpLuma&(1<<uint(blk8)) != 0 {
					for sub := 0; sub < 4; sub++ {
						blk4 := blk8*4 + sub
						nC := computeNC4x4Ctx(blk4, nzCoeffs[:], leftNZ, topNZ)
						block, tc := cavlc.DecodeCAVLCBlock(r, nC)
						mb.Coeffs[blk4] = [16]int16(block)
						nzCoeffs[blk4] = tc
						mb.TotalCoeff[blk4] = tc
					}
				}
			}
		} else {
			for blk := 0; blk < 16; blk++ {
				group := blk / 4
				if cbpLuma&(1<<uint(group)) != 0 {
					nC := computeNC4x4Ctx(blk, nzCoeffs[:], leftNZ, topNZ)
					block, tc := cavlc.DecodeCAVLCBlock(r, nC)
					mb.Coeffs[blk] = [16]int16(block)
					nzCoeffs[blk] = tc
					mb.TotalCoeff[blk] = tc
				}
			}
		}
	}

	// Chroma residual
	cbpChroma := mb.CodedBlockPattern >> 4
	if cbpChroma > 0 {
		for comp := 0; comp < 2; comp++ {
			dcBlock4 := cavlc.DecodeCAVLCChromaDC(r)
			for i := 0; i < 4; i++ {
				mb.CoeffsChroma[comp][i][0] = dcBlock4[i]
			}
		}
		if cbpChroma == 2 {
			for comp := 0; comp < 2; comp++ {
				var nzChroma [4]int
				for blk := 0; blk < 4; blk++ {
					nC := computeNCChroma4x4Ctx(blk, nzChroma[:], leftChromaNZ, topChromaNZ, comp)
					acBlock, tc := cavlc.DecodeCAVLCBlockAC(r, nC)
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

func decodeIPCMSamples(r *nal.Reader, mb *MBIntra) {
	if r == nil || mb == nil {
		return
	}
	r.ByteAlign()
	for i := range mb.PCMY {
		mb.PCMY[i] = uint8(r.ReadBits(8))
	}
	for i := range mb.PCMCb {
		mb.PCMCb[i] = uint8(r.ReadBits(8))
	}
	for i := range mb.PCMCr {
		mb.PCMCr[i] = uint8(r.ReadBits(8))
	}
	// H.264 9.2.1: an I_PCM neighbour contributes nN = 16 to CAVLC,
	// even though its samples have no transform coefficients.
	for i := range mb.TotalCoeff {
		mb.TotalCoeff[i] = 16
	}
	for comp := range mb.ChromaTotalCoeff {
		for i := range mb.ChromaTotalCoeff[comp] {
			mb.ChromaTotalCoeff[comp][i] = 16
		}
	}
}

// decodeCBPIntra decodes coded_block_pattern for intra macroblocks (Table 9-4).
func decodeCBPIntra(r *nal.Reader) uint32 {
	codeNum := r.ReadUEBounded(47)
	if r.Err() != nil {
		return 0
	}
	cbpIntraTable := [48]uint32{
		47, 31, 15, 0, 23, 27, 29, 30, 7, 11, 13, 14, 39, 43, 45, 46,
		16, 3, 5, 10, 12, 19, 21, 26, 28, 35, 37, 42, 44, 1, 2, 4,
		8, 17, 18, 20, 24, 6, 9, 22, 25, 32, 33, 34, 36, 40, 38, 41,
	}
	return cbpIntraTable[codeNum]
}

// Block-index to column/row lookup helpers used by nC context computations.
// Block layout within MB (H.264 raster scan §6.4.3):
//
//	0  1  4  5
//	2  3  6  7
//	8  9 12 13
//
// 10 11 14 15
func computeNC4x4(blkIdx int, nz []int) int {
	return computeNC4x4Ctx(blkIdx, nz, nil, nil)
}

// computeNCLumaDC computes nC for the I16x16 luma-DC CAVLC block.
// Per H.264 §9.2.1, N_A should be the DC-block totalCoeff of the left MB if
// that MB is I16x16, or -1 otherwise. In practice x264 (and therefore most
// Baseline/Main streams) uses the AC totalCoeff of the neighbour's block-5 as
// dcNC, so we match that behaviour for compatibility.
func computeNCLumaDC(leftNZ, topNZ *[16]int) int {
	return combineNC(neighbourNC(leftNZ, BlkXYToIdx[0][3]), neighbourNC(topNZ, BlkXYToIdx[3][0]))
}

func computeNC4x4Ctx(blkIdx int, nz []int, leftNZ, topNZ *[16]int) int {
	x := Blk4x4Col[blkIdx]
	y := Blk4x4Row[blkIdx]
	nA, nB := -1, -1
	if x > 0 {
		nA = nz[BlkXYToIdx[y][x-1]]
	} else if leftNZ != nil {
		nA = leftNZ[BlkXYToIdx[y][3]]
	}
	if y > 0 {
		nB = nz[BlkXYToIdx[y-1][x]]
	} else if topNZ != nil {
		nB = topNZ[BlkXYToIdx[3][x]]
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

func neighbourNC(nz *[16]int, idx int) int {
	if nz == nil {
		return -1
	}
	return nz[idx]
}

func combineNC(nA, nB int) int {
	if nA < 0 && nB < 0 {
		return 0
	}
	if nA < 0 {
		return nB
	}
	if nB < 0 {
		return nA
	}
	return (nA + nB + 1) >> 1
}
