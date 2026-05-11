package syntax

// P-slice macroblock types and motion vector decoding.
// ITU-T H.264 §7.3.5, §7.4.5

import (
	cavlc "github.com/rcarmo/go-264/entropy/cavlc"
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
	MBType           uint32
	RefIdx           [4]int8          // reference frame indices per partition
	MV               [4]MotionVector  // motion vectors per partition
	SubMBType        [4]uint32        // sub-macroblock types for P8x8
	SubMV            [16]MotionVector // sub-partition MVs for P8x8
	CBP              uint32
	Use8x8Transform  bool  // true if inter MB uses 8x8 DCT (High profile)
	DecodedMVDX      int16 // representative decoded X MVD for amvd context of future MBs
	DecodedMVDY      int16 // representative decoded Y MVD
	QPDelta          int32
	Coeffs           [16][16]int16
	CoeffsChroma     [2][4][16]int16
	TotalCoeff       [16]int
	ChromaTotalCoeff [2][4]int
}

// InterDecodeOpts carries context for CAVLC inter macroblock decoding.
// Zero-value is safe (no neighbour context, QP=0, single reference frame).
type InterDecodeOpts struct {
	SliceQP      int32
	NumRefFrames uint32
	LeftNZ       *[16]int
	TopNZ        *[16]int
	LeftChromaNZ *[2][4]int
	TopChromaNZ  *[2][4]int
}

// DecodeMBInter decodes one CAVLC inter macroblock from a P-slice.
// If the decoded mb_type indicates an intra MB (>= PMBTypeIntra), the returned
// MBInter.MBType is set but no intra payload is consumed; the caller must then
// call DecodeMBIntraWithType(r, mb.MBType-PMBTypeIntra, opts) to consume the
// remainder.
func DecodeMBInter(r *nal.Reader, opts InterDecodeOpts) MBInter {
	var mb MBInter
	mb.MBType = r.ReadUE()
	if mb.MBType >= PMBTypeIntra {
		return mb // caller handles intra payload
	}

	numRefFrames := opts.NumRefFrames
	leftNZ, topNZ := opts.LeftNZ, opts.TopNZ
	leftChromaNZ, topChromaNZ := opts.LeftChromaNZ, opts.TopChromaNZ

	switch mb.MBType {
	case PMBTypeP16x16:
		if numRefFrames > 1 {
			mb.RefIdx[0] = int8(readTE(r, int(numRefFrames-1)))
		}
		mb.MV[0] = decodeMVD(r)

	case PMBTypeP16x8:
		for i := 0; i < 2; i++ {
			if numRefFrames > 1 {
				mb.RefIdx[i] = int8(readTE(r, int(numRefFrames-1)))
			}
		}
		for i := 0; i < 2; i++ {
			mb.MV[i] = decodeMVD(r)
		}

	case PMBTypeP8x16:
		for i := 0; i < 2; i++ {
			if numRefFrames > 1 {
				mb.RefIdx[i] = int8(readTE(r, int(numRefFrames-1)))
			}
		}
		for i := 0; i < 2; i++ {
			mb.MV[i] = decodeMVD(r)
		}

	case PMBTypeP8x8, PMBTypeP8x8ref0:
		for i := 0; i < 4; i++ {
			mb.SubMBType[i] = r.ReadUE()
		}
		for i := 0; i < 4; i++ {
			if numRefFrames > 1 && mb.MBType != PMBTypeP8x8ref0 {
				mb.RefIdx[i] = int8(readTE(r, int(numRefFrames-1)))
			}
		}
		for i := 0; i < 4; i++ {
			numSubParts := subMBPartCount(mb.SubMBType[i])
			for j := 0; j < numSubParts; j++ {
				mb.SubMV[i*4+j] = decodeMVD(r)
			}
		}
	}

	mb.CBP = decodeCBPInter(r)
	if mb.CBP > 0 {
		mb.QPDelta = r.ReadSE()
	}

	if mb.CBP > 0 {
		cbpLuma := mb.CBP & 0xF
		var nzCoeffs [16]int
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
		cbpChroma := mb.CBP >> 4
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
	}
	return mb
}

func readTE(r *nal.Reader, maxVal int) uint32 {
	if maxVal <= 0 {
		return 0
	}
	if maxVal == 1 {
		return 1 - r.ReadBit()
	}
	return r.ReadUE()
}

func decodeMVD(r *nal.Reader) MotionVector {
	return MotionVector{
		X: int16(r.ReadSE()),
		Y: int16(r.ReadSE()),
	}
}

func subMBPartCount(subType uint32) int {
	switch subType {
	case 0:
		return 1 // 8x8
	case 1:
		return 2 // 8x4
	case 2:
		return 2 // 4x8
	case 3:
		return 4 // 4x4
	}
	return 1
}

func decodeCBPInter(r *nal.Reader) uint32 {
	codeNum := r.ReadUE()
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
// ITU-T H.264 §8.4.1.3.
// A = left, B = top, C = top-right (or top-left if C unavailable).
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
