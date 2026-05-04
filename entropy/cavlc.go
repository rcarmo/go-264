package entropy

import "github.com/rcarmo/go-264/nal"

type Block4x4 [16]int16

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

func DecodeCAVLCBlock(r *nal.Reader, nC int) (Block4x4, int) {
	var block Block4x4
	totalCoeff, trailingOnes := DecodeCoeffToken(r, nC)
	if totalCoeff == 0 { return block, 0 }
	signs := make([]int16, trailingOnes)
	for i := trailingOnes - 1; i >= 0; i-- {
		if r.ReadBit() == 1 { signs[i] = -1 } else { signs[i] = 1 }
	}
	levels := make([]int16, totalCoeff)
	idx := totalCoeff - 1
	for i := trailingOnes - 1; i >= 0; i-- { levels[idx] = signs[i]; idx-- }
	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 { suffixLength = 1 }
	for i := trailingOnes; i < totalCoeff; i++ {
		levelCode := decodeLevelPrefix(r, suffixLength)
		if i == trailingOnes && trailingOnes < 3 { levelCode += 2 }
		if levelCode%2 == 0 { levels[idx] = int16(levelCode/2 + 1) } else { levels[idx] = int16(-(levelCode + 1) / 2) }
		absLevel := levels[idx]; if absLevel < 0 { absLevel = -absLevel }
		if suffixLength == 0 { suffixLength = 1 }
		if int(absLevel) > (3 << uint(suffixLength-1)) && suffixLength < 6 { suffixLength++ }
		idx--
	}
	totalZeros := 0
	if totalCoeff < 16 { totalZeros = DecodeTotalZeros(r, totalCoeff) }
	zerosLeft := totalZeros
	coeffIdx := totalCoeff - 1
	scanPos := totalCoeff + totalZeros - 1
	for coeffIdx >= 0 {
		if zerosLeft > 0 && coeffIdx > 0 {
			run := DecodeRunBefore(r, zerosLeft)
			if scanPos >= 0 && scanPos < 16 { block[zigZag4x4[scanPos]] = levels[coeffIdx] }
			scanPos -= run + 1; zerosLeft -= run
		} else {
			if scanPos >= 0 && scanPos < 16 { block[zigZag4x4[scanPos]] = levels[coeffIdx] }
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
	prefix := 0
	for !r.EOF() && prefix < 20 {
		if r.ReadBit() == 1 { break }; prefix++
	}
	var levelSuffixSize int
	if prefix == 14 && suffixLength == 0 {
		levelSuffixSize = 4
	} else if prefix >= 15 {
		levelSuffixSize = prefix - 3
	} else {
		levelSuffixSize = suffixLength
	}
	levelCode := prefix << uint(suffixLength)
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

func decodeCoeffTokenN0(r *nal.Reader) (int, int) {
	var code uint32
	code = (code << 1) | r.ReadBits(1)
	if code == 1 { return 0, 0 }
	code = (code << 1) | r.ReadBits(1)
	if code == 1 { return 1, 1 }
	code = (code << 1) | r.ReadBits(1)
	if code == 1 { return 2, 2 }
	code = (code << 2) | r.ReadBits(2)
	if code == 3 { return 3, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 5 { return 1, 0 }
	if code == 4 { return 2, 1 }
	if code == 3 { return 4, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 5 { return 3, 2 }
	if code == 3 { return 5, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 2, 0 }
	if code == 6 { return 3, 1 }
	if code == 5 { return 4, 2 }
	if code == 3 { return 6, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 3, 0 }
	if code == 6 { return 4, 1 }
	if code == 5 { return 5, 2 }
	if code == 3 { return 7, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 4, 0 }
	if code == 6 { return 5, 1 }
	if code == 5 { return 6, 2 }
	if code == 3 { return 8, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 5, 0 }
	if code == 6 { return 6, 1 }
	if code == 5 { return 7, 2 }
	if code == 3 { return 9, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 6, 0 }
	if code == 6 { return 7, 1 }
	if code == 5 { return 8, 2 }
	if code == 3 { return 10, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 7, 0 }
	if code == 6 { return 8, 1 }
	if code == 5 { return 9, 2 }
	if code == 3 { return 11, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 8, 0 }
	if code == 6 { return 9, 1 }
	if code == 5 { return 10, 2 }
	if code == 3 { return 12, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 9, 0 }
	if code == 6 { return 10, 1 }
	if code == 5 { return 11, 2 }
	if code == 3 { return 13, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 10, 0 }
	if code == 6 { return 11, 1 }
	if code == 5 { return 12, 2 }
	if code == 3 { return 14, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 11, 0 }
	if code == 6 { return 12, 1 }
	if code == 5 { return 13, 2 }
	if code == 3 { return 15, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 12, 0 }
	if code == 6 { return 13, 1 }
	if code == 5 { return 14, 2 }
	if code == 1 { return 15, 0 }
	if code == 2 { return 16, 1 }
	if code == 3 { return 16, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 13, 0 }
	if code == 6 { return 14, 1 }
	if code == 5 { return 15, 2 }
	if code == 3 { return 16, 0 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 14, 0 }
	if code == 6 { return 15, 1 }
	if code == 5 { return 16, 2 }
	return 0, 0
}

func decodeCoeffTokenN2(r *nal.Reader) (int, int) {
	var code uint32
	code = (code << 2) | r.ReadBits(2)
	if code == 3 { return 0, 0 }
	if code == 2 { return 1, 1 }
	code = (code << 1) | r.ReadBits(1)
	if code == 3 { return 2, 2 }
	code = (code << 2) | r.ReadBits(2)
	if code == 7 { return 2, 1 }
	if code == 5 { return 3, 3 }
	if code == 4 { return 4, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 11 { return 1, 0 }
	if code == 7 { return 2, 0 }
	if code == 10 { return 3, 1 }
	if code == 9 { return 3, 2 }
	if code == 6 { return 4, 1 }
	if code == 5 { return 4, 2 }
	if code == 4 { return 5, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 3, 0 }
	if code == 6 { return 5, 1 }
	if code == 5 { return 5, 2 }
	if code == 4 { return 6, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 4, 0 }
	if code == 6 { return 6, 1 }
	if code == 5 { return 6, 2 }
	if code == 4 { return 7, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 5, 0 }
	if code == 6 { return 7, 1 }
	if code == 5 { return 7, 2 }
	if code == 4 { return 8, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 6, 0 }
	if code == 6 { return 8, 1 }
	if code == 5 { return 8, 2 }
	code = (code << 1) | r.ReadBits(1)
	if code == 7 { return 7, 0 }
	code = (code << 2) | r.ReadBits(2)
	if code == 7 { return 8, 0 }
	return 0, 0
}

func decodeCoeffTokenN4(r *nal.Reader) (int, int) {
	var code uint32
	code = (code << 4) | r.ReadBits(4)
	if code == 15 { return 0, 0 }
	if code == 14 { return 1, 1 }
	if code == 13 { return 2, 2 }
	if code == 12 { return 3, 3 }
	if code == 11 { return 4, 3 }
	if code == 10 { return 5, 3 }
	if code == 9 { return 6, 3 }
	if code == 8 { return 7, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 15 { return 2, 1 }
	if code == 12 { return 3, 1 }
	if code == 14 { return 3, 2 }
	if code == 10 { return 4, 1 }
	if code == 11 { return 4, 2 }
	if code == 8 { return 5, 1 }
	if code == 9 { return 5, 2 }
	code = (code << 1) | r.ReadBits(1)
	if code == 15 { return 1, 0 }
	if code == 11 { return 2, 0 }
	if code == 8 { return 3, 0 }
	if code == 14 { return 6, 1 }
	if code == 13 { return 6, 2 }
	if code == 12 { return 8, 2 }
	code = (code << 1) | r.ReadBits(1)
	if code == 15 { return 4, 0 }
	if code == 11 { return 5, 0 }
	if code == 8 { return 6, 0 }
	if code == 14 { return 7, 1 }
	if code == 13 { return 7, 2 }
	if code == 12 { return 8, 3 }
	code = (code << 1) | r.ReadBits(1)
	if code == 15 { return 7, 0 }
	if code == 11 { return 8, 0 }
	if code == 8 { return 8, 1 }
	return 0, 0
}


// decodeCoeffTokenChromaDC decodes coeff_token for chroma DC (4:2:0, max 4 coeffs).
// ITU-T H.264 Table 9-5(e)
func decodeCoeffTokenChromaDC(r *nal.Reader) (int, int) {
	if r.ReadBit() == 1 { return 0, 0 }       // "1"
	if r.ReadBit() == 1 { return 1, 1 }       // "01"
	if r.ReadBit() == 1 { return 2, 2 }       // "001"
	// "000" prefix
	if r.ReadBit() == 1 {
		if r.ReadBit() == 1 { return 3, 3 }   // "000 11"
		return 4, 3                             // "000 01" — wait
	}
	// "0000" prefix  
	if r.ReadBit() == 1 { return 1, 0 }       // "0000 1"
	// "00000" prefix
	if r.ReadBit() == 1 { return 2, 1 }       // "00000 1"
	// "000000" prefix
	if r.ReadBit() == 1 { return 3, 2 }       // "000000 1"
	// "0000000" prefix
	if r.ReadBit() == 1 { return 4, 2 }       // "0000000 1"
	// "00000000" prefix
	if r.ReadBit() == 1 { return 2, 0 }       // "00000000 1"
	// "000000000" prefix
	if r.ReadBit() == 1 { return 3, 1 }       // "000000000 1"
	// "0000000000" prefix
	if r.ReadBit() == 1 { return 4, 1 }       // "0000000000 1"
	// "00000000000" prefix
	if r.ReadBit() == 1 { return 3, 0 }       // "00000000000 1"
	// "000000000000" prefix
	if r.ReadBit() == 1 { return 4, 0 }       // "000000000000 1"
	return 0, 0 // fallback
}

// DecodeCAVLCChromaDC decodes a chroma DC 2×2 block (4:2:0, max 4 coefficients).
func DecodeCAVLCChromaDC(r *nal.Reader) [4]int16 {
	var block [4]int16
	totalCoeff, trailingOnes := decodeCoeffTokenChromaDC(r)
	if totalCoeff == 0 { return block }

	signs := make([]int16, trailingOnes)
	for i := trailingOnes - 1; i >= 0; i-- {
		if r.ReadBit() == 1 { signs[i] = -1 } else { signs[i] = 1 }
	}

	levels := make([]int16, totalCoeff)
	idx := totalCoeff - 1
	for i := trailingOnes - 1; i >= 0; i-- { levels[idx] = signs[i]; idx-- }

	suffixLength := 0
	if totalCoeff > 10 && trailingOnes < 3 { suffixLength = 1 }
	for i := trailingOnes; i < totalCoeff; i++ {
		levelCode := decodeLevelPrefix(r, suffixLength)
		if i == trailingOnes && trailingOnes < 3 { levelCode += 2 }
		if levelCode%2 == 0 { levels[idx] = int16(levelCode/2 + 1) } else { levels[idx] = int16(-(levelCode + 1) / 2) }
		absLevel := levels[idx]; if absLevel < 0 { absLevel = -absLevel }
		if suffixLength == 0 { suffixLength = 1 }
		if int(absLevel) > (3 << uint(suffixLength-1)) && suffixLength < 6 { suffixLength++ }
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
			if scanPos >= 0 && scanPos < 4 { block[scanPos] = levels[coeffIdx] }
			scanPos -= run + 1; zerosLeft -= run
		} else {
			if scanPos >= 0 && scanPos < 4 { block[scanPos] = levels[coeffIdx] }
			scanPos--
		}
		coeffIdx--
	}
	return block
}

// decodeChromaDCTotalZeros: from FFmpeg chroma_dc_total_zeros tables
func decodeChromaDCTotalZeros(r *nal.Reader, totalCoeff int) int {
	switch totalCoeff {
	case 1: // lens: 1,2,3,3  bits: 1,1,1,0
		if r.ReadBit() == 1 { return 0 }
		if r.ReadBit() == 1 { return 1 }
		if r.ReadBit() == 1 { return 2 }
		return 3
	case 2: // lens: 1,2,2  bits: 1,1,0
		if r.ReadBit() == 1 { return 0 }
		if r.ReadBit() == 1 { return 1 }
		return 2
	case 3: // lens: 1,1  bits: 1,0
		if r.ReadBit() == 1 { return 0 }
		return 1
	}
	return 0
}
