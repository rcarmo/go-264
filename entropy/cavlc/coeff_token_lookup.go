package cavlc

import "github.com/rcarmo/go-264/nal"

// coeffTokenLookup packs len,totalCoeff,trailingOnes as:
//
//	bits 15..8: code length
//	bits 7..2: totalCoeff (values 0..16)
//	bits 1..0: trailingOnes (values 0..3)
//
// A zero entry means no valid coeff_token prefix.
var coeffTokenLookup [4][1 << 16]uint16

func init() {
	buildCoeffTokenLookup(0, &ctLen0, &ctBits0)
	buildCoeffTokenLookup(1, &ctLen1, &ctBits1)
	buildCoeffTokenLookup(2, &ctLen2, &ctBits2)
	buildCoeffTokenLookup(3, &ctLen3, &ctBits3)
}

func buildCoeffTokenLookup(table int, lens, bits *[68]uint8) {
	for tc := 0; tc <= 16; tc++ {
		maxTO := 3
		if tc < maxTO {
			maxTO = tc
		}
		for to := 0; to <= maxTO; to++ {
			idx := tc*4 + to
			l := int(lens[idx])
			if l == 0 || l > 16 {
				continue
			}
			prefix := int(bits[idx]) << uint(16-l)
			span := 1 << uint(16-l)
			packed := uint16(l<<8 | tc<<2 | to)
			for suffix := 0; suffix < span; suffix++ {
				coeffTokenLookup[table][prefix|suffix] = packed
			}
		}
	}
}

func coeffTokenTableIndex(nC int) int {
	switch {
	case nC < 2:
		return 0
	case nC < 4:
		return 1
	case nC < 8:
		return 2
	default:
		return 3
	}
}

func decodeCoeffTokenLookup(r *nal.Reader, nC int) (totalCoeff, trailingOnes int, ok bool) {
	if r.BitsLeft() < 16 {
		return 0, 0, false
	}
	entry := coeffTokenLookup[coeffTokenTableIndex(nC)][r.PeekBits(16)]
	if entry == 0 {
		return 0, 0, false
	}
	l := int(entry >> 8)
	r.SkipBits(l)
	return int((entry >> 2) & 0x3F), int(entry & 3), true
}
