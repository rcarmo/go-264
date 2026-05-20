package decode

import "github.com/rcarmo/go-264/syntax"

const (
	ffMBTypeIntra4x4   uint32 = 1 << 0
	ffMBTypeIntra16x16 uint32 = 1 << 1
	ffMBTypeIntraPCM   uint32 = 1 << 2
	ffMBType16x16      uint32 = 1 << 3
	ffMBType16x8       uint32 = 1 << 4
	ffMBType8x16       uint32 = 1 << 5
	ffMBType8x8        uint32 = 1 << 6
	ffMBTypeDirect2    uint32 = 1 << 8
	ffMBTypeP0L0       uint32 = 1 << 12
	ffMBTypeP1L0       uint32 = 1 << 13
	ffMBTypeP0L1       uint32 = 1 << 14
	ffMBTypeP1L1       uint32 = 1 << 15
)

func ffInterMBType(mb *syntax.MBInter) uint32 {
	if mb == nil {
		return 0
	}
	if mb.MBType >= syntax.PMBTypeIntra {
		return ffMBTypeIntra4x4
	}
	switch mb.MBType {
	case syntax.PMBTypeP16x16:
		return ffMBType16x16 | ffMBTypeP0L0 | ffMBTypeP1L0
	case syntax.PMBTypeP16x8:
		return ffMBType16x8 | ffMBTypeP0L0 | ffMBTypeP1L0
	case syntax.PMBTypeP8x16:
		return ffMBType8x16 | ffMBTypeP0L0 | ffMBTypeP1L0
	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		return ffMBType8x8 | ffMBTypeP0L0 | ffMBTypeP1L0
	default:
		return ffMBType16x16 | ffMBTypeP0L0 | ffMBTypeP1L0
	}
}

func ffBidiMBType(mb *syntax.MBBidi) uint32 {
	if mb == nil {
		return 0
	}
	if mb.MBType >= syntax.BMBTypeIntra {
		if mb.Intra != nil && mb.Intra.MBType == syntax.MBTypeIPCM {
			return ffMBTypeIntraPCM
		}
		if mb.Intra != nil && mb.Intra.MBType != syntax.MBTypeINxN {
			return ffMBTypeIntra16x16
		}
		return ffMBTypeIntra4x4
	}
	switch mb.MBType {
	case syntax.BMBTypeDirect16x16:
		return ffMBType16x16 | ffMBTypeDirect2 | ffMBTypeP0L0 | ffMBTypeP1L0 | ffMBTypeP0L1 | ffMBTypeP1L1
	case syntax.BMBTypeL016x16:
		return ffMBType16x16 | ffMBTypeP0L0 | ffMBTypeP1L0
	case syntax.BMBTypeL116x16:
		return ffMBType16x16 | ffMBTypeP0L1 | ffMBTypeP1L1
	case syntax.BMBTypeBi16x16:
		return ffMBType16x16 | ffMBTypeP0L0 | ffMBTypeP1L0 | ffMBTypeP0L1 | ffMBTypeP1L1
	case syntax.BMBTypeL016x8, syntax.BMBTypeL116x8, syntax.BMBTypeBi16x8:
		return ffMBType16x8 | ffBidiPartUseFlags(mb, 0) | ffBidiPartUseFlags(mb, 1)
	case syntax.BMBTypeL016x8b, syntax.BMBTypeL116x8b, syntax.BMBTypeBi16x8b, syntax.BMBTypeL08x16, syntax.BMBTypeL18x16, syntax.BMBTypeBi8x16:
		return ffMBType8x16 | ffBidiPartUseFlags(mb, 0) | ffBidiPartUseFlags(mb, 1)
	case syntax.BMBTypeB8x8:
		flags := ffMBType8x8
		for part, t := range mb.SubMBType {
			if t == 0 {
				flags |= ffMBTypeDirect2 | ffMBTypeP0L0 | ffMBTypeP1L0 | ffMBTypeP0L1 | ffMBTypeP1L1
				continue
			}
			if syntax.BMBSubUsesL0(t) {
				if part&1 == 0 {
					flags |= ffMBTypeP0L0
				} else {
					flags |= ffMBTypeP1L0
				}
			}
			if syntax.BMBSubUsesL1(t) {
				if part&1 == 0 {
					flags |= ffMBTypeP0L1
				} else {
					flags |= ffMBTypeP1L1
				}
			}
		}
		return flags
	default:
		return ffMBType16x16 | ffBidiPartUseFlags(mb, 0)
	}
}

func ffBidiPartUseFlags(mb *syntax.MBBidi, part int) uint32 {
	var flags uint32
	if syntax.BPartUsesL0(mb.MBType, part) {
		if part == 0 {
			flags |= ffMBTypeP0L0
		} else {
			flags |= ffMBTypeP1L0
		}
	}
	if syntax.BPartUsesL1(mb.MBType, part) {
		if part == 0 {
			flags |= ffMBTypeP0L1
		} else {
			flags |= ffMBTypeP1L1
		}
	}
	return flags
}
