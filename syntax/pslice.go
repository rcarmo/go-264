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
	Transform8x8 bool
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
	if r == nil {
		return mb
	}
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
	if interTransform8x8FlagPresent(opts.Transform8x8, mb.CBP, mb.MBType, mb.SubMBType, true) {
		mb.Use8x8Transform = r.ReadBool()
	}
	if mb.CBP > 0 {
		mb.QPDelta = r.ReadSE()
	}

	decodeInterResidualCAVLC(r, mb.CBP, &mb.Coeffs, &mb.CoeffsChroma, &mb.TotalCoeff, &mb.ChromaTotalCoeff, leftNZ, topNZ, leftChromaNZ, topChromaNZ)
	return mb
}

func interTransform8x8FlagPresent(enabled bool, cbp uint32, mbType uint32, subTypes [4]uint32, direct8x8Inference bool) bool {
	if !enabled || cbp&0xF == 0 {
		return false
	}
	if mbType == PMBTypeP8x8 || mbType == PMBTypeP8x8ref0 {
		for _, subType := range subTypes {
			if subType != 0 { // only P_L0_8x8 has no smaller sub-partitions
				return false
			}
		}
	}
	if mbType == BMBTypeB8x8 {
		for _, subType := range subTypes {
			switch {
			case subType == 0 && !direct8x8Inference:
				return false
			case subType >= 4:
				return false
			}
		}
	}
	return true
}

func decodeInterResidualCAVLC(r *nal.Reader, cbp uint32, coeffs *[16][16]int16, coeffsChroma *[2][4][16]int16, totalCoeff *[16]int, chromaTotalCoeff *[2][4]int, leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int) {
	if r == nil || cbp == 0 || coeffs == nil || coeffsChroma == nil || totalCoeff == nil || chromaTotalCoeff == nil {
		return
	}
	cbpLuma := cbp & 0xF
	for blk := 0; blk < 16; blk++ {
		group := blk / 4
		if cbpLuma&(1<<uint(group)) == 0 {
			continue
		}
		nC := computeNC4x4Ctx(blk, totalCoeff[:], leftNZ, topNZ)
		block, tc := cavlc.DecodeCAVLCBlock(r, nC)
		coeffs[blk] = [16]int16(block)
		totalCoeff[blk] = tc
	}
	cbpChroma := cbp >> 4
	if cbpChroma == 0 {
		return
	}
	for comp := 0; comp < 2; comp++ {
		dcBlock4 := cavlc.DecodeCAVLCChromaDC(r)
		for i := 0; i < 4; i++ {
			coeffsChroma[comp][i][0] = dcBlock4[i]
		}
	}
	if cbpChroma != 2 {
		return
	}
	for comp := 0; comp < 2; comp++ {
		for blk := 0; blk < 4; blk++ {
			nC := computeNCChroma4x4Ctx(blk, chromaTotalCoeff[comp][:], leftChromaNZ, topChromaNZ, comp)
			acBlock, tc := cavlc.DecodeCAVLCBlockAC(r, nC)
			for j := 1; j < 16; j++ {
				coeffsChroma[comp][blk][j] = acBlock[j]
			}
			chromaTotalCoeff[comp][blk] = tc
		}
	}
}

func readTE(r *nal.Reader, maxVal int) uint32 {
	if r == nil || maxVal <= 0 {
		return 0
	}
	if maxVal == 1 {
		return 1 - r.ReadBit()
	}
	v := r.ReadUE()
	if v > uint32(maxVal) {
		return uint32(maxVal)
	}
	return v
}

func decodeMVD(r *nal.Reader) MotionVector {
	if r == nil {
		return MotionVector{}
	}
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
