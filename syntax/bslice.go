package syntax

// B-slice macroblock types and bidirectional prediction.
// ITU-T H.264 §7.3.5, Table 7-14

import "github.com/rcarmo/go-264/nal"

// B-slice macroblock types
const (
	BMBTypeDirect16x16 = 0
	BMBTypeL016x16     = 1
	BMBTypeL116x16     = 2
	BMBTypeBi16x16     = 3
	BMBTypeL016x8      = 4
	BMBTypeL016x8b     = 5 // actually B_L0_8x16 in Table 7-14; kept for API compatibility
	BMBTypeL116x8      = 6
	BMBTypeL116x8b     = 7 // actually B_L1_8x16
	BMBTypeBi16x8      = 8 // B_L0_L1_16x8
	BMBTypeBi16x8b     = 9 // B_L0_L1_8x16
	BMBTypeL08x16      = 10
	BMBTypeL18x16      = 11
	BMBTypeBi8x16      = 12
	BMBTypeB8x8        = 22
	BMBTypeIntra       = 23 // I_NxN in B-slice
)

// MBBidi describes a decoded B-slice macroblock.
type MBBidi struct {
	MBType           uint32
	RefIdxL0         [4]int8
	RefIdxL1         [4]int8
	MVL0             [4]MotionVector
	MVL1             [4]MotionVector
	SubMBType        [4]uint32
	CBP              uint32
	QPDelta          int32
	Coeffs           [16][16]int16
	CoeffsChroma     [2][4][16]int16
	TotalCoeff       [16]int
	ChromaTotalCoeff [2][4]int
	Intra            *MBIntra
}

// BidiDecodeOpts carries context for CAVLC B-slice macroblock decoding.
// Zero-value is safe: single-reference lists, no neighbour contexts, no 8x8 transform.
type BidiDecodeOpts struct {
	SliceQP      int32
	NumRefL0     uint32
	NumRefL1     uint32
	Transform8x8 bool
	LeftNZ       *[16]int
	TopNZ        *[16]int
	LeftChromaNZ *[2][4]int
	TopChromaNZ  *[2][4]int
}

// DecodeMBBidi decodes one macroblock from a B-slice.
func DecodeMBBidi(r *nal.Reader, sliceQP int32, numRefL0, numRefL1 uint32) *MBBidi {
	return DecodeMBBidiWithOpts(r, BidiDecodeOpts{SliceQP: sliceQP, NumRefL0: numRefL0, NumRefL1: numRefL1})
}

// DecodeMBBidiWithOpts decodes one macroblock from a B-slice with neighbour
// state for residual nC and transform-size syntax decisions.
func DecodeMBBidiWithOpts(r *nal.Reader, opts BidiDecodeOpts) *MBBidi {
	mb := &MBBidi{}
	if r == nil {
		return mb
	}
	numRefL0, numRefL1 := opts.NumRefL0, opts.NumRefL1
	mb.MBType = r.ReadUE()

	if mb.MBType >= BMBTypeIntra {
		mb.Intra = DecodeMBIntraWithType(r, mb.MBType-BMBTypeIntra, IntraDecodeOpts{
			SliceQP: opts.SliceQP, Transform8x8: opts.Transform8x8,
			LeftNZ: opts.LeftNZ, TopNZ: opts.TopNZ, LeftChromaNZ: opts.LeftChromaNZ, TopChromaNZ: opts.TopChromaNZ,
		})
		return mb
	}

	// Direct mode derives refs/MVs from colocated state, but non-skip
	// B_Direct_16x16 still carries coded_block_pattern/residual syntax below.
	if mb.MBType == BMBTypeDirect16x16 {
		decodeBResidual(r, mb, opts)
		return mb
	}

	// Determine list usage from mb_type
	numParts := 1
	if mb.MBType >= 4 && mb.MBType <= 21 {
		numParts = 2
	}
	if mb.MBType == BMBTypeB8x8 {
		numParts = 4
		for i := 0; i < 4; i++ {
			mb.SubMBType[i] = r.ReadUE()
		}
	}

	usesL0Part := func(part int) bool {
		if mb.MBType == BMBTypeB8x8 {
			return usesBSubL0(mb.SubMBType[part])
		}
		return usesL0(mb.MBType, part)
	}
	usesL1Part := func(part int) bool {
		if mb.MBType == BMBTypeB8x8 {
			return usesBSubL1(mb.SubMBType[part])
		}
		return usesL1(mb.MBType, part)
	}

	// Reference indices
	for i := 0; i < numParts; i++ {
		if usesL0Part(i) && numRefL0 > 1 {
			mb.RefIdxL0[i] = int8(readTE(r, int(numRefL0-1)))
		}
	}
	for i := 0; i < numParts; i++ {
		if usesL1Part(i) && numRefL1 > 1 {
			mb.RefIdxL1[i] = int8(readTE(r, int(numRefL1-1)))
		}
	}

	// Motion vectors
	for i := 0; i < numParts; i++ {
		if usesL0Part(i) {
			for subPart := 0; subPart < bSubMBPartCountForType(mb.SubMBType[i]); subPart++ {
				mvd := decodeMVD(r)
				if subPart == 0 {
					mb.MVL0[i] = mvd
				}
			}
		}
	}
	for i := 0; i < numParts; i++ {
		if usesL1Part(i) {
			for subPart := 0; subPart < bSubMBPartCountForType(mb.SubMBType[i]); subPart++ {
				mvd := decodeMVD(r)
				if subPart == 0 {
					mb.MVL1[i] = mvd
				}
			}
		}
	}

	decodeBResidual(r, mb, opts)

	return mb
}

var bMBUsesL0 = [23][2]bool{
	1:  {true, false}, // B_L0_16x16
	3:  {true, false}, // B_Bi_16x16
	4:  {true, true},  // B_L0_L0_16x8
	5:  {true, true},  // B_L0_L0_8x16
	8:  {true, false}, // B_L0_L1_16x8
	9:  {true, false}, // B_L0_L1_8x16
	10: {false, true}, // B_L1_L0_16x8
	11: {false, true}, // B_L1_L0_8x16
	12: {true, true},  // B_L0_Bi_16x8
	13: {true, true},  // B_L0_Bi_8x16
	14: {false, true}, // B_L1_Bi_16x8
	15: {false, true}, // B_L1_Bi_8x16
	16: {true, true},  // B_Bi_L0_16x8
	17: {true, true},  // B_Bi_L0_8x16
	18: {true, false}, // B_Bi_L1_16x8
	19: {true, false}, // B_Bi_L1_8x16
	20: {true, true},  // B_Bi_Bi_16x8
	21: {true, true},  // B_Bi_Bi_8x16
	22: {true, true},  // B_8x8: actual use is sub_mb_type-driven; legacy decoder uses this gate only for coarse syntax
}

var bSubMBUsesL0 = [13]bool{
	1: true, 3: true, 4: true, 5: true, 8: true, 9: true, 10: true, 12: true,
}

var bSubMBUsesL1 = [13]bool{
	2: true, 3: true, 6: true, 7: true, 8: true, 9: true, 11: true, 12: true,
}

var bSubMBPartCount = [13]int{
	0: 1, 1: 1, 2: 1, 3: 1,
	4: 2, 5: 2, 6: 2, 7: 2, 8: 2, 9: 2,
	10: 4, 11: 4, 12: 4,
}

var bMBUsesL1 = [23][2]bool{
	2:  {true, false}, // B_L1_16x16
	3:  {true, false}, // B_Bi_16x16
	6:  {true, true},  // B_L1_L1_16x8
	7:  {true, true},  // B_L1_L1_8x16
	8:  {false, true}, // B_L0_L1_16x8
	9:  {false, true}, // B_L0_L1_8x16
	10: {true, false}, // B_L1_L0_16x8
	11: {true, false}, // B_L1_L0_8x16
	12: {false, true}, // B_L0_Bi_16x8
	13: {false, true}, // B_L0_Bi_8x16
	14: {true, true},  // B_L1_Bi_16x8
	15: {true, true},  // B_L1_Bi_8x16
	16: {true, false}, // B_Bi_L0_16x8
	17: {true, false}, // B_Bi_L0_8x16
	18: {true, true},  // B_Bi_L1_16x8
	19: {true, true},  // B_Bi_L1_8x16
	20: {true, true},  // B_Bi_Bi_16x8
	21: {true, true},  // B_Bi_Bi_8x16
	22: {true, true},  // B_8x8: actual use is sub_mb_type-driven; legacy decoder uses this gate only for coarse syntax
}

// BSubUsesL0 reports whether a B_8x8 sub-macroblock type uses list 0.
func BSubUsesL0(subType uint32) bool { return usesBSubL0(subType) }

// BSubUsesL1 reports whether a B_8x8 sub-macroblock type uses list 1.
func BSubUsesL1(subType uint32) bool { return usesBSubL1(subType) }

func usesBSubL0(subType uint32) bool {
	return subType < uint32(len(bSubMBUsesL0)) && bSubMBUsesL0[subType]
}

func usesBSubL1(subType uint32) bool {
	return subType < uint32(len(bSubMBUsesL1)) && bSubMBUsesL1[subType]
}

func bSubMBPartCountForType(subType uint32) int {
	if subType < uint32(len(bSubMBPartCount)) {
		return bSubMBPartCount[subType]
	}
	return 0
}

func decodeBResidual(r *nal.Reader, mb *MBBidi, opts BidiDecodeOpts) {
	if r == nil || mb == nil {
		return
	}
	mb.CBP = decodeCBPInter(r)
	if mb.CBP > 0 {
		mb.QPDelta = r.ReadSE()
	}
	decodeInterResidualCAVLC(r, mb.CBP, &mb.Coeffs, &mb.CoeffsChroma, &mb.TotalCoeff, &mb.ChromaTotalCoeff, opts.LeftNZ, opts.TopNZ, opts.LeftChromaNZ, opts.TopChromaNZ)
}

// BPartUsesL0 reports whether a non-B_8x8 B macroblock partition uses list 0.
func BPartUsesL0(mbType uint32, partIdx int) bool { return usesL0(mbType, partIdx) }

// BPartUsesL1 reports whether a non-B_8x8 B macroblock partition uses list 1.
func BPartUsesL1(mbType uint32, partIdx int) bool { return usesL1(mbType, partIdx) }

// usesL0 returns true if the partition uses list 0 (forward) prediction.
func usesL0(mbType uint32, partIdx int) bool {
	if mbType >= uint32(len(bMBUsesL0)) || partIdx < 0 || partIdx > 1 {
		return false
	}
	return bMBUsesL0[mbType][partIdx]
}

// usesL1 returns true if the partition uses list 1 (backward) prediction.
func usesL1(mbType uint32, partIdx int) bool {
	if mbType >= uint32(len(bMBUsesL1)) || partIdx < 0 || partIdx > 1 {
		return false
	}
	return bMBUsesL1[mbType][partIdx]
}

// BiPredBlend blends L0 and L1 predictions for bidirectional prediction.
// The helper is used by tests/tools as well as reconstruction experiments, so
// malformed lengths are clipped at the boundary instead of panicking before the
// caller can report the bad stream or fixture.
func BiPredBlend(out, predL0, predL1 []uint8, n int) {
	if n <= 0 || len(out) == 0 || len(predL0) == 0 || len(predL1) == 0 {
		return
	}
	if n > len(out) {
		n = len(out)
	}
	if n > len(predL0) {
		n = len(predL0)
	}
	if n > len(predL1) {
		n = len(predL1)
	}
	for i := 0; i < n; i++ {
		out[i] = uint8((uint16(predL0[i]) + uint16(predL1[i]) + 1) >> 1)
	}
}
