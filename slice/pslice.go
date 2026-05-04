package slice

// P-slice macroblock types and motion vector decoding.
// ITU-T H.264 §7.3.5, §7.4.5

import (
	"github.com/rcarmo/go-264/entropy"
	"github.com/rcarmo/go-264/nal"
)

// P-slice macroblock types (Table 7-13)
const (
	PMBTypeP16x16   = 0
	PMBTypeP16x8    = 1
	PMBTypeP8x16    = 2
	PMBTypeP8x8     = 3
	PMBTypeP8x8ref0 = 4
	PMBTypeIntra    = 5 // I_NxN in P-slice (mb_type >= 5)
)

// MotionVector in quarter-pixel units.
type MotionVector struct {
	X, Y int16
}

// MBInter describes a decoded inter macroblock.
type MBInter struct {
	MBType    uint32
	RefIdx    [4]int8      // reference frame indices per partition
	MV        [4]MotionVector // motion vectors per partition
	SubMBType [4]uint32    // sub-macroblock types for P8x8
	SubMV     [16]MotionVector // sub-partition MVs for P8x8
	CBP       uint32
	QPDelta   int32
	Coeffs    [16][16]int16
	TotalCoeff [16]int
}

// DecodeMBInter decodes one inter macroblock from a P-slice.
func DecodeMBInter(r *nal.Reader, sliceQP int32, numRefFrames uint32) *MBInter {
	return DecodeMBInterCtx(r, sliceQP, numRefFrames, nil, nil)
}

// DecodeMBInterCtx decodes one inter macroblock with optional left/top CAVLC
// nC context from neighbouring macroblocks.
func DecodeMBInterCtx(r *nal.Reader, sliceQP int32, numRefFrames uint32, leftNZ, topNZ *[16]int) *MBInter {
	mb := &MBInter{}
	mb.MBType = r.ReadUE()

	// Check if this is actually an intra MB in a P-slice
	if mb.MBType >= 5 {
		// Intra macroblock — delegate to intra decoder
		return mb
	}

	switch mb.MBType {
	case PMBTypeP16x16:
		// One partition, one MV
		if numRefFrames > 1 {
			mb.RefIdx[0] = int8(r.ReadUE()) // ref_idx_l0
		}
		mb.MV[0] = decodeMVD(r) // mvd_l0

	case PMBTypeP16x8:
		// Two 16x8 partitions
		for i := 0; i < 2; i++ {
			if numRefFrames > 1 {
				mb.RefIdx[i] = int8(r.ReadUE())
			}
		}
		for i := 0; i < 2; i++ {
			mb.MV[i] = decodeMVD(r)
		}

	case PMBTypeP8x16:
		// Two 8x16 partitions
		for i := 0; i < 2; i++ {
			if numRefFrames > 1 {
				mb.RefIdx[i] = int8(r.ReadUE())
			}
		}
		for i := 0; i < 2; i++ {
			mb.MV[i] = decodeMVD(r)
		}

	case PMBTypeP8x8, PMBTypeP8x8ref0:
		// Four 8x8 sub-partitions
		for i := 0; i < 4; i++ {
			mb.SubMBType[i] = r.ReadUE()
		}
		for i := 0; i < 4; i++ {
			if numRefFrames > 1 && mb.MBType != PMBTypeP8x8ref0 {
				mb.RefIdx[i] = int8(r.ReadUE())
			}
		}
		for i := 0; i < 4; i++ {
			numSubParts := subMBPartCount(mb.SubMBType[i])
			for j := 0; j < numSubParts; j++ {
				mb.SubMV[i*4+j] = decodeMVD(r)
			}
		}
	}

	// Coded block pattern
	mb.CBP = decodeCBPInter(r)

	if mb.CBP > 0 {
		mb.QPDelta = r.ReadSE()
	}

	// Residual data (CAVLC). Approximate nC using already-decoded blocks within
	// the current macroblock; cross-MB nC is handled later when neighbour state is
	// threaded through the slice decoder.
	if mb.CBP > 0 {
		cbpLuma := mb.CBP & 0xF
		var nzCoeffs [16]int
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
		// TODO: decode chroma residual when CBP chroma is set.
	}
	return mb
}

// decodeMVD reads a motion vector difference (mvd_l0).
func decodeMVD(r *nal.Reader) MotionVector {
	return MotionVector{
		X: int16(r.ReadSE()),
		Y: int16(r.ReadSE()),
	}
}

// subMBPartCount returns the number of sub-partitions for a sub-MB type.
func subMBPartCount(subType uint32) int {
	switch subType {
	case 0: return 1 // 8x8
	case 1: return 2 // 8x4
	case 2: return 2 // 4x8
	case 3: return 4 // 4x4
	}
	return 1
}

// decodeCBPInter decodes coded_block_pattern for inter macroblocks.
func decodeCBPInter(r *nal.Reader) uint32 {
	codeNum := r.ReadUE()
	// Table 9-4: Inter CBP mapping
	cbpInterTable := [48]uint32{
		0, 16, 1, 2, 4, 8, 32, 3, 5, 10, 12, 15, 47, 7, 11, 13,
		14, 6, 9, 31, 35, 37, 42, 44, 33, 34, 36, 40, 39, 43, 45, 46,
		17, 18, 20, 24, 19, 21, 26, 28, 23, 27, 29, 30, 22, 25, 38, 41,
	}
	if codeNum < 48 {
		return cbpInterTable[codeNum]
	}
	return 0
}

// PredictMV computes the predicted motion vector using median prediction.
// ITU-T H.264 §8.4.1.3
// A = left, B = top, C = top-right (or top-left if C unavailable)
func PredictMV(a, b, c MotionVector, availA, availB, availC bool) MotionVector {
	if !availA && !availB && !availC {
		return MotionVector{0, 0}
	}
	if availA && !availB && !availC {
		return a
	}
	if !availA && availB && !availC {
		return b
	}

	// Median of three
	return MotionVector{
		X: median3(a.X, b.X, c.X),
		Y: median3(a.Y, b.Y, c.Y),
	}
}

func median3(a, b, c int16) int16 {
	if a > b {
		a, b = b, a
	}
	if b > c {
		b, c = c, b
	}
	if a > b {
		a, b = b, a
	}
	return b
}
