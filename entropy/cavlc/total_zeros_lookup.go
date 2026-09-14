package cavlc

import "github.com/rcarmo/go-264/nal"

// totalZerosLookup packs len,total_zeros as:
//
//	bits 15..8: code length
//	bits  7..0: total_zeros
//
// Table index is totalCoeff-1. Prefix index is the next 9 bits, matching the
// maximum total_zeros code length in H.264 Table 9-7.
var totalZerosLookup [15][512]uint16

func init() {
	for tableIdx := 0; tableIdx < len(totalZerosLookup); tableIdx++ {
		maxVal := totalZerosMaxVal[tableIdx]
		for val := 0; val <= maxVal; val++ {
			l := int(totalZerosLen[tableIdx][val])
			if l == 0 {
				continue
			}
			bits := uint16(totalZerosBits[tableIdx][val])
			fill := 9 - l
			start := int(bits) << fill
			end := start + (1 << fill)
			packed := uint16(l<<8) | uint16(val)
			for idx := start; idx < end; idx++ {
				totalZerosLookup[tableIdx][idx] = packed
			}
		}
	}
}

func decodeTotalZerosLookup(r *nal.Reader, totalCoeff int) (int, bool) {
	if totalCoeff <= 0 || totalCoeff >= 16 || r.BitsLeft() < 9 {
		return 0, false
	}
	entry := totalZerosLookup[totalCoeff-1][r.PeekBits(9)]
	if entry == 0 {
		return 0, false
	}
	l := int(entry >> 8)
	val := int(entry & 0xff)
	if val > totalZerosMaxVal[totalCoeff-1] {
		return 0, false
	}
	r.SkipBits(l)
	return val, true
}
