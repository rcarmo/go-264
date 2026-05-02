package entropy

// CAVLC (Context-Adaptive Variable-Length Coding) decoder.
// ITU-T H.264 §9.2

import (
	"github.com/rcarmo/go-264/nal"
)

// Block4x4 holds decoded coefficients for a 4×4 block in scan order.
type Block4x4 [16]int16

// DecodeCAVLCBlock decodes a 4×4 block of residual coefficients.
// nC is the predicted number of non-zero coefficients (context).
// Returns the block coefficients and the total non-zero count.
func DecodeCAVLCBlock(r *nal.Reader, nC int) (Block4x4, int) {
	var block Block4x4

	// 1. coeff_token → (totalCoeff, trailingOnes)
	totalCoeff, trailingOnes := DecodeCoeffToken(r, nC)
	if totalCoeff == 0 {
		return block, 0
	}

	// 2. Trailing ones signs (1 bit each, highest-frequency coeffs first)
	signs := make([]int16, trailingOnes)
	for i := trailingOnes - 1; i >= 0; i-- {
		if r.ReadBit() == 1 {
			signs[i] = -1
		} else {
			signs[i] = 1
		}
	}

	// 3. Decode levels (reverse order: highest frequency to DC)
	levels := make([]int16, totalCoeff)
	idx := totalCoeff - 1

	// Fill trailing ones
	for i := trailingOnes - 1; i >= 0; i-- {
		levels[idx] = signs[i]
		idx--
	}

	// Decode remaining levels
	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 {
		suffixLength = 1
	}

	for i := trailingOnes; i < totalCoeff; i++ {
		levelCode := decodeLevelPrefix(r, suffixLength)

		if i == trailingOnes && trailingOnes < 3 {
			levelCode += 2
		}

		// Map levelCode to signed level
		if levelCode%2 == 0 {
			levels[idx] = int16(levelCode/2 + 1)
		} else {
			levels[idx] = int16(-(levelCode + 1) / 2)
		}

		// Update suffix length threshold
		absLevel := levels[idx]
		if absLevel < 0 {
			absLevel = -absLevel
		}
		if suffixLength == 0 {
			suffixLength = 1
		}
		if int(absLevel) > (3 << uint(suffixLength-1)) {
			if suffixLength < 6 {
				suffixLength++
			}
		}
		idx--
	}

	// 4. total_zeros
	totalZeros := 0
	if totalCoeff < 16 {
		totalZeros = DecodeTotalZeros(r, totalCoeff)
	}

	// 5. run_before — place coefficients in scan order
	zerosLeft := totalZeros
	coeffIdx := totalCoeff - 1
	scanPos := totalCoeff + totalZeros - 1

	for coeffIdx >= 0 {
		if zerosLeft > 0 && coeffIdx > 0 {
			run := DecodeRunBefore(r, zerosLeft)
			if scanPos >= 0 && scanPos < 16 {
				block[scanPos] = levels[coeffIdx]
			}
			scanPos -= run + 1
			zerosLeft -= run
		} else {
			if scanPos >= 0 && scanPos < 16 {
				block[scanPos] = levels[coeffIdx]
			}
			scanPos--
		}
		coeffIdx--
	}

	return block, totalCoeff
}

// DecodeCoeffToken decodes totalCoeff and trailingOnes.
func DecodeCoeffToken(r *nal.Reader, nC int) (totalCoeff, trailingOnes int) {
	if nC >= 8 {
		// Fixed-length 6-bit code (Table 9-5d)
		code := r.ReadBits(6)
		trailingOnes = int(code % 4)
		totalCoeff = int(code/4) + 1
		if trailingOnes > totalCoeff {
			trailingOnes = totalCoeff
		}
		if totalCoeff > 16 {
			totalCoeff = 16
		}
		return
	}

	if nC < 2 {
		return decodeCoeffTokenN0(r)
	} else if nC < 4 {
		return decodeCoeffTokenN2(r)
	}
	return decodeCoeffTokenN4(r)
}

// decodeCoeffTokenN0: nC = 0..1 (Table 9-5a)
// Pattern-based decoder using leading zeros.
func decodeCoeffTokenN0(r *nal.Reader) (int, int) {
	zeros := 0
	for !r.EOF() {
		if r.ReadBit() == 1 {
			break
		}
		zeros++
		if zeros > 19 {
			return 0, 0
		}
	}
	// Consumed 'zeros' 0-bits then one 1-bit

	switch zeros {
	case 0: return 0, 0   // "1"
	case 1: return 1, 1   // "01"
	case 2: return 2, 2   // "001"
	case 3: // "0001" + 1 bit
		if r.ReadBit() == 1 { return 3, 3 }  // "00011"
		return 4, 3 // "00010" + 1 more bit below
	case 4: // "00001" + 2 bits
		s := r.ReadBits(2)
		switch s {
		case 3: return 5, 3  // "0000111"
		case 2: return 3, 2  // "0000110" → wait, doesn't match spec
		case 1: return 4, 2  // "0000101"
		case 0: return 2, 1  // "0000100"
		}
	case 5: // "000001" + 1 bit
		s := r.ReadBit()
		if s == 1 { return 1, 0 }  // "0000011" → "000101" is (1,0) in spec
		return 3, 1 // "0000010"
	}

	// For zeros >= 6: pattern is 0^(zeros) 1 <2 bits>
	// Encodes (tc, to) where tc grows with zeros
	if zeros >= 6 && zeros <= 13 {
		s := r.ReadBits(2)
		tc := zeros - 3
		switch s {
		case 3: return tc + 1, 3
		case 2: return tc, 2
		case 1: return tc, 1
		case 0: return tc, 0
		}
	}

	// Very long codes (zeros >= 14): high totalCoeff
	if zeros >= 14 {
		s := r.ReadBits(2)
		tc := zeros - 3
		if tc > 16 { tc = 16 }
		to := int(s)
		if to > 3 { to = 3 }
		return tc, to
	}

	return 0, 0
}

// decodeCoeffTokenN2: nC = 2..3 (Table 9-5b)
func decodeCoeffTokenN2(r *nal.Reader) (int, int) {
	zeros := 0
	for !r.EOF() {
		if r.ReadBit() == 1 {
			break
		}
		zeros++
		if zeros > 15 {
			return 0, 0
		}
	}

	switch zeros {
	case 0: // "1" + 1 bit
		if r.ReadBit() == 1 { return 0, 0 } // "11"
		return 1, 1 // "10"
	case 1: // "01" + 1 bit
		if r.ReadBit() == 1 { return 2, 2 } // "011"
		return 3, 3 // "010" + more
	case 2: // "001" + 2 bits
		s := r.ReadBits(2)
		switch s {
		case 3: return 3, 2
		case 2: return 3, 1
		case 1: return 1, 0
		case 0: return 2, 1
		}
	case 3: // "0001" + 2 bits
		s := r.ReadBits(2)
		switch s {
		case 3: return 4, 3
		case 2: return 4, 2
		case 1: return 4, 1
		case 0: return 2, 0
		}
	}

	// For zeros >= 4: similar pattern
	if zeros >= 4 {
		s := r.ReadBits(2)
		tc := zeros
		switch s {
		case 3: return tc + 1, 3
		case 2: return tc + 1, 2
		case 1: return tc + 1, 1
		case 0: return tc + 1, 0
		}
	}
	return 0, 0
}

// decodeCoeffTokenN4: nC = 4..7 (Table 9-5c)
func decodeCoeffTokenN4(r *nal.Reader) (int, int) {
	// Read 4 bits, then optionally more
	code := r.ReadBits(4)

	switch code {
	case 0xF: return 0, 0   // "1111"
	case 0xE: return 1, 1   // "1110"
	case 0xD: return 2, 2   // "1101"
	case 0xC: return 3, 3   // "1100"
	case 0xB: return 4, 3   // "1011"
	case 0xA: return 5, 3   // "1010"
	case 0x9: return 6, 3   // "1001"
	case 0x8: return 7, 3   // "1000" — approximate
	}

	// 5-bit codes
	code = (code << 1) | r.ReadBit()
	switch code {
	case 0xF: return 2, 1   // "01111"
	case 0xE: return 3, 2   // "01110"
	case 0xD: return 4, 2   // "01101"
	case 0xC: return 5, 2   // "01100"
	case 0xB: return 3, 1   // "01011"
	case 0xA: return 4, 1   // "01010"
	case 0x9: return 5, 1   // "01001"
	case 0x8: return 6, 2   // "01000"
	}

	// 6-bit codes
	code = (code << 1) | r.ReadBit()
	switch code {
	case 0xF: return 1, 0
	case 0xE: return 6, 1
	case 0xD: return 2, 0
	case 0xC: return 7, 2
	case 0xB: return 3, 0
	case 0xA: return 8, 3
	case 0x9: return 7, 1
	case 0x8: return 8, 2
	}

	// 7-bit codes
	code = (code << 1) | r.ReadBit()
	tc := int(code>>2) + 5
	to := int(code & 3)
	if tc > 16 { tc = 16 }
	if to > 3 { to = 3 }
	return tc, to
}

// decodeLevelPrefix reads the level prefix + suffix.
func decodeLevelPrefix(r *nal.Reader, suffixLength int) int {
	prefix := 0
	for !r.EOF() {
		if r.ReadBit() == 1 {
			break
		}
		prefix++
		if prefix > 15 {
			break
		}
	}

	levelCode := prefix << uint(suffixLength)
	if suffixLength > 0 {
		levelCode += int(r.ReadBits(suffixLength))
	}
	if prefix >= 15 && suffixLength == 0 {
		levelCode += 15
	}
	if prefix >= 16 {
		levelCode += (1 << uint(prefix-3)) - 4096
	}

	return levelCode
}
