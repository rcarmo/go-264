package cavlc

import (
	"math/bits"

	"github.com/rcarmo/go-264/nal"
)

// DecodeCAVLCBlock decodes all sixteen positions of a 4x4 residual block.
func DecodeCAVLCBlock(r *nal.Reader, nC int) (Block4x4, int) {
	if block, tc, ok := decodeCAVLCBlockWord(r, nC); ok {
		return block, tc
	}
	return decodeCAVLCBlockFallback(r, nC)
}

// decodeCAVLCBlockWord decodes short residual blocks from one register-sized window. Nothing is
// consumed until the complete block succeeds. Long/escape codes, EPB windows
// and malformed prefixes restart in the original checked decoder.
func decodeCAVLCBlockWord(r *nal.Reader, nC int) (block Block4x4, totalCoeff int, ok bool) {
	word, left := r.PeekRawWord()
	if left == 0 {
		return block, 0, false
	}
	originalLeft := left
	ti := coeffTokenTableIndex(nC)
	entry := coeffTokenShortLookup[ti][word>>56]
	if entry == 0 {
		entry = coeffTokenLookup[ti][word>>48]
	}
	if entry == 0 {
		return block, 0, false
	}
	used := int(entry >> 8)
	totalCoeff = int((entry >> 2) & 63)
	trailingOnes := int(entry & 3)
	word <<= uint(used)
	left -= used
	if totalCoeff == 0 {
		r.SkipBits(used)
		return block, 0, true
	}
	var levels [16]int16
	signs := word >> uint(64-trailingOnes)
	word <<= uint(trailingOnes)
	left -= trailingOnes
	idx := totalCoeff - 1
	for i := trailingOnes - 1; i >= 0; i-- {
		levels[idx] = 1 - 2*int16((signs>>uint(i))&1)
		idx--
	}
	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 {
		suffixLength = 1
	}
	for i := trailingOnes; i < totalCoeff; i++ {
		prefix := bits.LeadingZeros64(word)
		// Escapes retain the original level-prefix and suffix semantics.
		if prefix >= 14 {
			return block, 0, false
		}
		n := prefix + 1 + suffixLength
		if n > left {
			return block, 0, false
		}
		levelCode := prefix<<uint(suffixLength) + int(word>>uint(64-n)&((1<<uint(suffixLength))-1))
		word <<= uint(n)
		left -= n
		if i == trailingOnes && trailingOnes < 3 {
			levelCode += 2
		}
		level := int16((levelCode >> 1) + 1)
		if levelCode&1 != 0 {
			level = -level
		}
		levels[idx] = level
		idx--
		abs := level
		if abs < 0 {
			abs = -abs
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		if int(abs) > (3<<uint(suffixLength-1)) && suffixLength < 6 {
			suffixLength++
		}
	}
	zerosLeft := 0
	if totalCoeff < 16 {
		entry = totalZerosLookup[totalCoeff-1][word>>55]
		used = int(entry >> 8)
		if entry == 0 || used > left {
			return block, 0, false
		}
		zerosLeft = int(entry & 255)
		word <<= uint(used)
		left -= used
	}
	scanPos := totalCoeff + zerosLeft - 1
	for coeffIdx := totalCoeff - 1; coeffIdx >= 0; coeffIdx-- {
		run := 0
		if zerosLeft > 0 && coeffIdx > 0 {
			entry = runBeforeLookup[min(zerosLeft-1, 6)][word>>53]
			used = int(entry >> 8)
			run = int(entry & 255)
			if entry == 0 || used > left || run > zerosLeft {
				return block, 0, false
			}
			word <<= uint(used)
			left -= used
			zerosLeft -= run
		}
		// Valid total_zeros and run_before keep scanPos within [0,15].
		block[zigZag4x4[scanPos]] = levels[coeffIdx]
		scanPos -= run + 1
	}
	// SkipBits accepts up to 32 bits per call. The lookahead proved that the
	// whole committed span is raw; only its final boundary can precede an EPB.
	consumed := originalLeft - left
	if consumed > 32 {
		r.SkipBits(32)
		consumed -= 32
	}
	r.SkipBits(consumed)
	return block, totalCoeff, true
}

// DecodeCAVLCBlockAC decodes the fifteen AC positions; Intra16/chroma DC
// coefficients are transmitted and reconstructed separately.
func DecodeCAVLCBlockAC(r *nal.Reader, nC int) (Block4x4, int) {
	if b, n, ok := decodeCAVLCBlockACWord(r, nC); ok {
		return b, n
	}
	return decodeCAVLCBlockACFallback(r, nC)
}

// decodeCAVLCBlockACWord keeps the AC limit and scan offset constant in the
// hot path. Like the full-block path, it commits only a complete valid block.
func decodeCAVLCBlockACWord(r *nal.Reader, nC int) (block Block4x4, totalCoeff int, ok bool) {
	word, left := r.PeekRawWord()
	if left == 0 {
		return block, 0, false
	}
	originalLeft := left
	ti := coeffTokenTableIndex(nC)
	entry := coeffTokenShortLookup[ti][word>>56]
	if entry == 0 {
		entry = coeffTokenLookup[ti][word>>48]
	}
	if entry == 0 {
		return block, 0, false
	}
	used := int(entry >> 8)
	totalCoeff = int((entry >> 2) & 63)
	if totalCoeff > 15 {
		return block, 0, false
	}
	trailingOnes := int(entry & 3)
	word <<= uint(used)
	left -= used
	if totalCoeff == 0 {
		r.SkipBits(used)
		return block, 0, true
	}
	var levels [16]int16
	signs := word >> uint(64-trailingOnes)
	word <<= uint(trailingOnes)
	left -= trailingOnes
	idx := totalCoeff - 1
	for i := trailingOnes - 1; i >= 0; i-- {
		levels[idx] = 1 - 2*int16((signs>>uint(i))&1)
		idx--
	}
	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 {
		suffixLength = 1
	}
	for i := trailingOnes; i < totalCoeff; i++ {
		prefix := bits.LeadingZeros64(word)
		// Escapes retain the original level-prefix and suffix semantics.
		if prefix >= 14 {
			return block, 0, false
		}
		n := prefix + 1 + suffixLength
		if n > left {
			return block, 0, false
		}
		levelCode := prefix<<uint(suffixLength) + int(word>>uint(64-n)&((1<<uint(suffixLength))-1))
		word <<= uint(n)
		left -= n
		if i == trailingOnes && trailingOnes < 3 {
			levelCode += 2
		}
		level := int16((levelCode >> 1) + 1)
		if levelCode&1 != 0 {
			level = -level
		}
		levels[idx] = level
		idx--
		abs := level
		if abs < 0 {
			abs = -abs
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		if int(abs) > (3<<uint(suffixLength-1)) && suffixLength < 6 {
			suffixLength++
		}
	}
	zerosLeft := 0
	if totalCoeff < 15 {
		entry = totalZerosLookup[totalCoeff-1][word>>55]
		used = int(entry >> 8)
		if entry == 0 || used > left {
			return block, 0, false
		}
		zerosLeft = int(entry & 255)
		if zerosLeft > 15-totalCoeff {
			return block, 0, false
		}
		word <<= uint(used)
		left -= used
	}
	scanPos := totalCoeff + zerosLeft - 1
	for coeffIdx := totalCoeff - 1; coeffIdx >= 0; coeffIdx-- {
		run := 0
		if zerosLeft > 0 && coeffIdx > 0 {
			entry = runBeforeLookup[min(zerosLeft-1, 6)][word>>53]
			used = int(entry >> 8)
			run = int(entry & 255)
			if entry == 0 || used > left || run > zerosLeft {
				return block, 0, false
			}
			word <<= uint(used)
			left -= used
			zerosLeft -= run
		}
		// Valid total_zeros and run_before keep scanPos within [0,15].
		block[zigZag4x4[scanPos+1]] = levels[coeffIdx]
		scanPos -= run + 1
	}
	// SkipBits accepts up to 32 bits per call. The lookahead proved that the
	// whole committed span is raw; only its final boundary can precede an EPB.
	consumed := originalLeft - left
	if consumed > 32 {
		r.SkipBits(32)
		consumed -= 32
	}
	r.SkipBits(consumed)
	return block, totalCoeff, true
}
