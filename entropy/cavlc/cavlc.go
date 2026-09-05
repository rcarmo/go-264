package cavlc

import (
	"math/bits"

	"github.com/rcarmo/go-264/nal"
)

type Block4x4 [16]int16
type Block8x8 [64]int16

// H.264 4x4 zig-zag scan order (Table 6-1 / inverse scan mapping).
// CAVLC places levels in scan order; the transform/dequant stages consume
// raster-order coefficient positions, so decoded coefficients must be mapped
// through this table before being stored in Block4x4.
var zigZag4x4 = [16]int{
	0, 1, 4, 8,
	5, 2, 3, 6,
	9, 12, 13, 10,
	7, 11, 14, 15,
}

// H.264 8x8 CAVLC scan order split into four 16-coefficient chunks.
// FFmpeg uses zigzag_scan8x8_cavlc for MB_TYPE_8x8DCT residuals instead of the
// plain 8x8 zig-zag order: each CAVLC block decodes every fourth 8x8 scan entry.
var zigZag8x8CAVLC = [64]int{
	0, 9, 17, 18, 12, 40, 27, 7,
	35, 57, 29, 30, 58, 38, 53, 47,
	1, 2, 24, 11, 19, 48, 20, 14,
	42, 50, 22, 37, 59, 31, 60, 55,
	8, 3, 32, 4, 26, 41, 13, 21,
	49, 43, 15, 44, 52, 39, 61, 62,
	16, 10, 25, 5, 33, 34, 6, 28,
	56, 36, 23, 51, 45, 46, 54, 63,
}

func DecodeCAVLCBlock(r *nal.Reader, nC int) (Block4x4, int) {
	var block Block4x4
	if r == nil {
		return block, 0
	}
	totalCoeff, trailingOnes := DecodeCoeffToken(r, nC)
	if totalCoeff == 0 {
		return block, 0
	}
	if totalCoeff > 16 {
		totalCoeff = 16
	}
	var levels [16]int16
	idx := totalCoeff - 1
	signs := r.ReadBits(trailingOnes)
	for i := trailingOnes - 1; i >= 0; i-- {
		levels[idx] = 1 - 2*int16((signs>>uint(i))&1)
		idx--
	}
	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 {
		suffixLength = 1
	}
	for i := trailingOnes; i < totalCoeff; i++ {
		levelCode := decodeLevelPrefix(r, suffixLength)
		if i == trailingOnes && trailingOnes < 3 {
			levelCode += 2
		}
		// levelCode is nonnegative: even codes are positive, odd negative.
		levels[idx] = int16((levelCode >> 1) + 1)
		if levelCode&1 != 0 {
			levels[idx] = -levels[idx]
		}
		absLevel := levels[idx]
		if absLevel < 0 {
			absLevel = -absLevel
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		if int(absLevel) > (3<<uint(suffixLength-1)) && suffixLength < 6 {
			suffixLength++
		}
		idx--
	}
	totalZeros := 0
	if totalCoeff < 16 {
		totalZeros = DecodeTotalZeros(r, totalCoeff)
	}
	// levels[] is stored in increasing scan-order index by this decoder: index 0
	// is the lowest-frequency non-zero coefficient and index totalCoeff-1 is the
	// last/highest-frequency non-zero coefficient. run_before is decoded from the
	// high-frequency end, so place from scanPos downward.
	zerosLeft := totalZeros
	coeffIdx := totalCoeff - 1
	scanPos := totalCoeff + totalZeros - 1
	for coeffIdx >= 0 {
		if zerosLeft > 0 && coeffIdx > 0 {
			run := DecodeRunBefore(r, zerosLeft)
			if scanPos >= 0 && scanPos < 16 {
				block[zigZag4x4[scanPos]] = levels[coeffIdx]
			}
			scanPos -= run + 1
			zerosLeft -= run
		} else {
			if scanPos >= 0 && scanPos < 16 {
				block[zigZag4x4[scanPos]] = levels[coeffIdx]
			}
			scanPos--
		}
		coeffIdx--
	}
	return block, totalCoeff
}

// DecodeCAVLCBlockAC decodes a 15-coefficient AC residual block whose scan
// starts after the DC coefficient. Returned coefficients are placed in
// raster-order positions 1..15; position 0 is left zero for caller-supplied DC.
func DecodeCAVLCBlockAC(r *nal.Reader, nC int) (Block4x4, int) {
	if r == nil {
		return Block4x4{}, 0
	}
	return decodeCAVLCBlockWithScan(r, nC, 15, zigZag4x4[1:])
}

func DecodeCAVLCBlock8x8Part(r *nal.Reader, nC int, part int) (Block8x8, int) {
	if r == nil || part < 0 || part > 3 {
		return Block8x8{}, 0
	}
	return decodeCAVLCBlock8x8WithScan(r, nC, zigZag8x8CAVLC[part*16:part*16+16])
}

func decodeCAVLCBlockWithScan(r *nal.Reader, nC int, maxCoeff int, scan []int) (Block4x4, int) {
	var block Block4x4
	totalCoeff, trailingOnes := DecodeCoeffToken(r, nC)
	if totalCoeff == 0 {
		return block, 0
	}
	if totalCoeff > maxCoeff {
		totalCoeff = maxCoeff
	}
	var levels [16]int16
	idx := totalCoeff - 1
	signs := r.ReadBits(trailingOnes)
	for i := trailingOnes - 1; i >= 0; i-- {
		levels[idx] = 1 - 2*int16((signs>>uint(i))&1)
		idx--
	}
	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 {
		suffixLength = 1
	}
	for i := trailingOnes; i < totalCoeff; i++ {
		levelCode := decodeLevelPrefix(r, suffixLength)
		if i == trailingOnes && trailingOnes < 3 {
			levelCode += 2
		}
		// levelCode is nonnegative: even codes are positive, odd negative.
		levels[idx] = int16((levelCode >> 1) + 1)
		if levelCode&1 != 0 {
			levels[idx] = -levels[idx]
		}
		absLevel := levels[idx]
		if absLevel < 0 {
			absLevel = -absLevel
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		if int(absLevel) > (3<<uint(suffixLength-1)) && suffixLength < 6 {
			suffixLength++
		}
		idx--
	}
	totalZeros := 0
	if totalCoeff < maxCoeff {
		totalZeros = DecodeTotalZeros(r, totalCoeff)
	}
	zerosLeft := totalZeros
	coeffIdx := totalCoeff - 1
	scanPos := totalCoeff + totalZeros - 1
	for coeffIdx >= 0 {
		if zerosLeft > 0 && coeffIdx > 0 {
			run := DecodeRunBefore(r, zerosLeft)
			if scanPos >= 0 && scanPos < len(scan) {
				block[scan[scanPos]] = levels[coeffIdx]
			}
			scanPos -= run + 1
			zerosLeft -= run
		} else {
			if scanPos >= 0 && scanPos < len(scan) {
				block[scan[scanPos]] = levels[coeffIdx]
			}
			scanPos--
		}
		coeffIdx--
	}
	return block, totalCoeff
}

func decodeCAVLCBlock8x8WithScan(r *nal.Reader, nC int, scan []int) (Block8x8, int) {
	var block Block8x8
	totalCoeff, trailingOnes := DecodeCoeffToken(r, nC)
	if totalCoeff == 0 {
		return block, 0
	}
	if totalCoeff > 16 {
		totalCoeff = 16
	}
	var levels [16]int16
	idx := totalCoeff - 1
	signs := r.ReadBits(trailingOnes)
	for i := trailingOnes - 1; i >= 0; i-- {
		levels[idx] = 1 - 2*int16((signs>>uint(i))&1)
		idx--
	}
	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 {
		suffixLength = 1
	}
	for i := trailingOnes; i < totalCoeff; i++ {
		levelCode := decodeLevelPrefix(r, suffixLength)
		if i == trailingOnes && trailingOnes < 3 {
			levelCode += 2
		}
		// levelCode is nonnegative: even codes are positive, odd negative.
		levels[idx] = int16((levelCode >> 1) + 1)
		if levelCode&1 != 0 {
			levels[idx] = -levels[idx]
		}
		absLevel := levels[idx]
		if absLevel < 0 {
			absLevel = -absLevel
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		if int(absLevel) > (3<<uint(suffixLength-1)) && suffixLength < 6 {
			suffixLength++
		}
		idx--
	}
	totalZeros := 0
	if totalCoeff < 16 {
		totalZeros = DecodeTotalZeros(r, totalCoeff)
	}
	zerosLeft := totalZeros
	coeffIdx := totalCoeff - 1
	scanPos := totalCoeff + totalZeros - 1
	for coeffIdx >= 0 {
		if zerosLeft > 0 && coeffIdx > 0 {
			run := DecodeRunBefore(r, zerosLeft)
			if scanPos >= 0 && scanPos < len(scan) {
				block[scan[scanPos]] = levels[coeffIdx]
			}
			scanPos -= run + 1
			zerosLeft -= run
		} else {
			if scanPos >= 0 && scanPos < len(scan) {
				block[scan[scanPos]] = levels[coeffIdx]
			}
			scanPos--
		}
		coeffIdx--
	}
	return block, totalCoeff
}

func DecodeCoeffToken(r *nal.Reader, nC int) (int, int) {
	return decodeCoeffTokenFromTable(r, nC)
}

func decodeLevelPrefix(r *nal.Reader, suffixLength int) int {
	// A short level contains both its unary prefix and suffix in this one
	// lookahead. Consume the pair once instead of rereading the suffix after
	// skipping the prefix. Escape levels retain their separate syntax below.
	if r.BitsLeft() >= 16 {
		word := r.PeekBits(16)
		prefix := bits.LeadingZeros16(uint16(word))
		n := prefix + 1 + suffixLength
		if prefix < 14 && n <= 16 {
			r.SkipBits(n)
			return prefix<<uint(suffixLength) + int(word>>uint(16-n)&((1<<uint(suffixLength))-1))
		}
	}
	prefix := decodeLevelPrefixBits(r)
	var levelSuffixSize int
	if prefix == 14 && suffixLength == 0 {
		levelSuffixSize = 4
	} else if prefix >= 15 {
		levelSuffixSize = prefix - 3
	} else {
		levelSuffixSize = suffixLength
	}
	levelCodePrefix := prefix
	if prefix >= 15 {
		// For escape-coded levels the prefix contribution is saturated to 15
		// before applying suffixLength (§9.2.2). Using prefix<<suffixLength here
		// makes large levels too large and can perturb suffix-length adaptation for
		// following coefficients.
		levelCodePrefix = 15
	}
	levelCode := levelCodePrefix << uint(suffixLength)
	if levelSuffixSize > 0 {
		levelCode += int(r.ReadBits(levelSuffixSize))
	}
	if prefix >= 15 && suffixLength == 0 {
		levelCode += 15
	}
	if prefix >= 16 {
		levelCode += (1 << uint(prefix-3)) - 4096
	}
	return levelCode
}

func decodeLevelPrefixBits(r *nal.Reader) int {
	if r.BitsLeft() >= 16 {
		v := uint16(r.PeekBits(16))
		if v != 0 {
			prefix := bits.LeadingZeros16(v)
			if prefix < 20 {
				r.SkipBits(prefix + 1)
				return prefix
			}
		}
	}
	prefix := 0
	for !r.EOF() && prefix < 20 {
		if r.ReadBit() == 1 {
			break
		}
		prefix++
	}
	return prefix
}

// decodeCoeffTokenChromaDC decodes coeff_token for chroma DC (4:2:0, max 4 coeffs).
// ITU-T H.264 Table 9-5(e)
func decodeCoeffTokenChromaDC(r *nal.Reader) (int, int) {
	return decodeCoeffTokenChromaDCTable(r)
}

// DecodeCAVLCChromaDC decodes a chroma DC 2×2 block (4:2:0, max 4 coefficients).
func DecodeCAVLCChromaDC(r *nal.Reader) [4]int16 {
	var block [4]int16
	if r == nil {
		return block
	}
	totalCoeff, trailingOnes := decodeCoeffTokenChromaDC(r)
	if totalCoeff == 0 {
		return block
	}
	if totalCoeff > 4 {
		totalCoeff = 4
	}
	if trailingOnes > totalCoeff {
		trailingOnes = totalCoeff
	}

	var levels [4]int16
	idx := totalCoeff - 1
	signs := r.ReadBits(trailingOnes)
	for i := trailingOnes - 1; i >= 0; i-- {
		levels[idx] = 1 - 2*int16((signs>>uint(i))&1)
		idx--
	}

	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 {
		suffixLength = 1
	}
	for i := trailingOnes; i < totalCoeff; i++ {
		levelCode := decodeLevelPrefix(r, suffixLength)
		if i == trailingOnes && trailingOnes < 3 {
			levelCode += 2
		}
		// levelCode is nonnegative: even codes are positive, odd negative.
		levels[idx] = int16((levelCode >> 1) + 1)
		if levelCode&1 != 0 {
			levels[idx] = -levels[idx]
		}
		absLevel := levels[idx]
		if absLevel < 0 {
			absLevel = -absLevel
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		if int(absLevel) > (3<<uint(suffixLength-1)) && suffixLength < 6 {
			suffixLength++
		}
		idx--
	}

	// Chroma DC total_zeros (from FFmpeg chroma_dc_total_zeros tables)
	totalZeros := 0
	if totalCoeff < 4 {
		totalZeros = decodeChromaDCTotalZeros(r, totalCoeff)
	}

	zerosLeft := totalZeros
	coeffIdx := totalCoeff - 1
	scanPos := totalCoeff + totalZeros - 1
	for coeffIdx >= 0 {
		if zerosLeft > 0 && coeffIdx > 0 {
			run := DecodeRunBefore(r, zerosLeft)
			if scanPos >= 0 && scanPos < 4 {
				block[scanPos] = levels[coeffIdx]
			}
			scanPos -= run + 1
			zerosLeft -= run
		} else {
			if scanPos >= 0 && scanPos < 4 {
				block[scanPos] = levels[coeffIdx]
			}
			scanPos--
		}
		coeffIdx--
	}
	return block
}

// decodeChromaDCTotalZeros: from FFmpeg chroma_dc_total_zeros tables
func decodeChromaDCTotalZeros(r *nal.Reader, totalCoeff int) int {
	return decodeChromaDCTotalZerosTable(r, totalCoeff)
}
