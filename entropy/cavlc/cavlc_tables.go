package cavlc

//go:generate go run ../../internal/tables/gen_cavlc_tables.go -o cavlc_tables.go
// Source: ITU-T H.264 Table 9-4, 9-5, 9-6; mirroring FFmpeg h264_cavlc.c VLC tables.
// Re-run the generator after any spec table update; do not hand-edit this file.

// CAVLC tables from FFmpeg h264_cavlc.c (authoritative).

import "github.com/rcarmo/go-264/nal"

// Table 9-7: total_zeros VLC
var totalZerosLen = [15][16]uint8{
	{1, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 9},
	{3, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 6, 6, 6, 6, 0},
	{4, 3, 3, 3, 4, 4, 3, 3, 4, 5, 5, 6, 5, 6, 0, 0},
	{5, 3, 4, 4, 3, 3, 3, 4, 3, 4, 5, 5, 5, 0, 0, 0},
	{4, 4, 4, 3, 3, 3, 3, 3, 4, 5, 4, 5, 0, 0, 0, 0},
	{6, 5, 3, 3, 3, 3, 3, 3, 4, 3, 6, 0, 0, 0, 0, 0},
	{6, 5, 3, 3, 3, 2, 3, 4, 3, 6, 0, 0, 0, 0, 0, 0},
	{6, 4, 5, 3, 2, 2, 3, 3, 6, 0, 0, 0, 0, 0, 0, 0},
	{6, 6, 4, 2, 2, 3, 2, 5, 0, 0, 0, 0, 0, 0, 0, 0},
	{5, 5, 3, 2, 2, 2, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{4, 4, 3, 3, 1, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{4, 4, 2, 1, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{3, 3, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
}

var totalZerosBits = [15][16]uint8{
	{1, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 1},
	{7, 6, 5, 4, 3, 5, 4, 3, 2, 3, 2, 3, 2, 1, 0, 0},
	{5, 7, 6, 5, 4, 3, 4, 3, 2, 3, 2, 1, 1, 0, 0, 0},
	{3, 7, 5, 4, 6, 5, 4, 3, 3, 2, 2, 1, 0, 0, 0, 0},
	{5, 4, 3, 7, 6, 5, 4, 3, 2, 1, 1, 0, 0, 0, 0, 0},
	{1, 1, 7, 6, 5, 4, 3, 2, 1, 1, 0, 0, 0, 0, 0, 0},
	{1, 1, 5, 4, 3, 3, 2, 1, 1, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 1, 3, 3, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 0, 1, 3, 2, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 0, 1, 3, 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 1, 1, 2, 1, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
}

var totalZerosMaxVal = [15]int{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

// Table 9-10: run_before VLC
var runBeforeLen = [7][16]uint8{
	{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{2, 2, 2, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{2, 2, 2, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{2, 2, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{2, 3, 3, 3, 3, 3, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{3, 3, 3, 3, 3, 3, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0},
}

var runBeforeBits = [7][16]uint8{
	{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{3, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{3, 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{3, 2, 3, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{3, 0, 1, 3, 2, 5, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{7, 6, 5, 4, 3, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0},
}

// DecodeTotalZeros decodes total_zeros (Table 9-7).
func DecodeTotalZeros(r *nal.Reader, totalCoeff int) int {
	if r == nil {
		return 0
	}
	if tz, ok := decodeTotalZerosLookup(r, totalCoeff); ok {
		return tz
	}
	if totalCoeff <= 0 || totalCoeff >= 16 {
		return 0
	}
	tableIdx := totalCoeff - 1
	maxVal := totalZerosMaxVal[tableIdx]

	avail := r.BitsLeft()
	peekLen := 9 // max code length in total_zeros table
	if avail < peekLen {
		peekLen = avail
	}
	if peekLen <= 0 {
		return 0
	}
	bits := r.PeekBits(peekLen)

	for val := 0; val <= maxVal; val++ {
		cLen := int(totalZerosLen[tableIdx][val])
		cBits := uint32(totalZerosBits[tableIdx][val])
		if cLen == 0 || cLen > peekLen {
			continue
		}
		shift := uint(peekLen - cLen)
		if (bits >> shift) == cBits {
			// Advance through the reader instead of Seek(pos+cLen): Seek uses
			// raw EBSP bit offsets and can land inside/after an emulation-prevention
			// byte, while ReadBits preserves RBSP skip semantics.
			r.ReadBits(cLen)
			return val
		}
	}
	r.ReadBit() // fallback; preserve emulation-prevention handling
	return 0
}

// DecodeRunBefore decodes run_before (Table 9-10).
func DecodeRunBefore(r *nal.Reader, zerosLeft int) int {
	if r == nil || zerosLeft <= 0 {
		return 0
	}
	if run, ok := decodeRunBeforeLookup(r, zerosLeft); ok {
		return run
	}

	tableIdx := zerosLeft - 1
	if tableIdx > 6 {
		tableIdx = 6
	}
	maxRun := zerosLeft
	if maxRun > 15 {
		maxRun = 15
	}

	avail := r.BitsLeft()
	peekLen := 11 // max code length in run_before table
	if avail < peekLen {
		peekLen = avail
	}
	if peekLen <= 0 {
		return 0
	}
	bits := r.PeekBits(peekLen)

	for run := 0; run <= maxRun; run++ {
		cLen := int(runBeforeLen[tableIdx][run])
		cBits := uint32(runBeforeBits[tableIdx][run])
		if cLen == 0 || cLen > peekLen {
			continue
		}
		shift := uint(peekLen - cLen)
		if (bits >> shift) == cBits {
			r.ReadBits(cLen)
			return run
		}
	}
	r.ReadBit()
	return 0
}

// Coeff_token VLC tables from FFmpeg (Table 9-5a/b/c)
// Indexed as [totalCoeff*4 + trailingOnes]

// Table 9-5a: 0 <= nC < 2
var ctLen0 = [68]uint8{
	1, 0, 0, 0,
	6, 2, 0, 0, 8, 6, 3, 0, 9, 8, 7, 5, 10, 9, 8, 6,
	11, 10, 9, 7, 13, 11, 10, 8, 13, 13, 11, 9, 13, 13, 13, 10,
	14, 14, 13, 11, 14, 14, 14, 13, 15, 15, 14, 14, 15, 15, 15, 14,
	16, 15, 15, 15, 16, 16, 16, 15, 16, 16, 16, 16, 16, 16, 16, 16,
}
var ctBits0 = [68]uint8{
	1, 0, 0, 0,
	5, 1, 0, 0, 7, 4, 1, 0, 7, 6, 5, 3, 7, 6, 5, 3,
	7, 6, 5, 4, 15, 6, 5, 4, 11, 14, 5, 4, 8, 10, 13, 4,
	15, 14, 9, 4, 11, 10, 13, 12, 15, 14, 9, 12, 11, 10, 13, 8,
	15, 1, 9, 12, 11, 14, 13, 8, 7, 10, 9, 12, 4, 6, 5, 8,
}

// Table 9-5b: 2 <= nC < 4
var ctLen1 = [68]uint8{2, 0, 0, 0, 6, 2, 0, 0, 6, 5, 3, 0, 7, 6, 6, 4, 8, 6, 6, 4, 8, 7, 7, 5, 9, 8, 8, 6, 11, 9, 9, 6, 11, 11, 11, 7, 12, 11, 11, 9, 12, 12, 12, 11, 12, 12, 12, 11, 13, 13, 13, 12, 13, 13, 13, 13, 13, 14, 13, 13, 14, 14, 14, 13, 14, 14, 14, 14}
var ctBits1 = [68]uint8{3, 0, 0, 0, 11, 2, 0, 0, 7, 7, 3, 0, 7, 10, 9, 5, 7, 6, 5, 4, 4, 6, 5, 6, 7, 6, 5, 8, 15, 6, 5, 4, 11, 14, 13, 4, 15, 10, 9, 4, 11, 14, 13, 12, 8, 10, 9, 8, 15, 14, 13, 12, 11, 10, 9, 12, 7, 11, 6, 8, 9, 8, 10, 1, 7, 6, 5, 4}

// Table 9-5c: 4 <= nC < 8
var ctLen2 = [68]uint8{4, 0, 0, 0, 6, 4, 0, 0, 6, 5, 4, 0, 6, 5, 5, 4, 7, 5, 5, 4, 7, 5, 5, 4, 7, 6, 6, 4, 7, 6, 6, 4, 8, 7, 7, 5, 8, 8, 7, 6, 9, 8, 8, 7, 9, 9, 8, 8, 9, 9, 9, 8, 10, 9, 9, 9, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10}
var ctBits2 = [68]uint8{15, 0, 0, 0, 15, 14, 0, 0, 11, 15, 13, 0, 8, 12, 14, 12, 15, 10, 11, 11, 11, 8, 9, 10, 9, 14, 13, 9, 8, 10, 9, 8, 15, 14, 13, 13, 11, 14, 10, 12, 15, 10, 13, 12, 11, 14, 9, 12, 8, 10, 13, 8, 13, 7, 9, 12, 9, 12, 11, 10, 5, 8, 7, 6, 1, 4, 3, 2}

// Table 9-5d: 8 <= nC
var ctLen3 = [68]uint8{6, 0, 0, 0, 6, 6, 0, 0, 6, 6, 6, 0, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6}
var ctBits3 = [68]uint8{3, 0, 0, 0, 0, 1, 0, 0, 4, 5, 6, 0, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63}

// decodeCoeffTokenFromTable reads coeff_token using FFmpeg VLC tables.
func decodeCoeffTokenFromTable(r *nal.Reader, nC int) (int, int) {
	if r == nil {
		return 0, 0
	}
	if tc, to, ok := decodeCoeffTokenLookup(r, nC); ok {
		return tc, to
	}
	var ctLen *[68]uint8
	var ctBits *[68]uint8
	if nC < 2 {
		ctLen = &ctLen0
		ctBits = &ctBits0
	} else if nC < 4 {
		ctLen = &ctLen1
		ctBits = &ctBits1
	} else if nC < 8 {
		ctLen = &ctLen2
		ctBits = &ctBits2
	} else {
		ctLen = &ctLen3
		ctBits = &ctBits3
	}

	avail := r.BitsLeft()
	peekLen := 16
	if avail < peekLen {
		peekLen = avail
	}
	if peekLen <= 0 {
		return 0, 0
	}
	bits := r.PeekBits(peekLen)

	bestLen := 0
	bestTC, bestTO := 0, 0
	for tc := 0; tc <= 16; tc++ {
		maxTO := 3
		if tc < maxTO {
			maxTO = tc
		}
		for to := 0; to <= maxTO; to++ {
			idx := tc*4 + to
			cLen := int(ctLen[idx])
			cBits := uint32(ctBits[idx])
			if cLen == 0 || cLen > peekLen {
				continue
			}
			shift := uint(peekLen - cLen)
			if (bits >> shift) == cBits {
				if bestLen == 0 || cLen < bestLen {
					bestLen = cLen
					bestTC = tc
					bestTO = to
				}
			}
		}
	}

	if bestLen > 0 {
		r.ReadBits(bestLen)
		return bestTC, bestTO
	}
	r.ReadBit()
	return 0, 0
}

// Chroma DC coeff_token Table 9-5(e), indexed by totalCoeff*4+trailingOnes.
var chromaDCCoeffTokenLen = [20]uint8{2, 0, 0, 0, 6, 1, 0, 0, 6, 6, 3, 0, 6, 7, 7, 6, 6, 8, 8, 7}
var chromaDCCoeffTokenBits = [20]uint8{1, 0, 0, 0, 7, 1, 0, 0, 4, 6, 1, 0, 3, 3, 2, 5, 2, 3, 2, 0}

var chromaDCTotalZerosLen = [3][4]uint8{
	{1, 2, 3, 3},
	{1, 2, 2, 0},
	{1, 1, 0, 0},
}
var chromaDCTotalZerosBits = [3][4]uint8{
	{1, 1, 1, 0},
	{1, 1, 0, 0},
	{1, 0, 0, 0},
}

func decodeCoeffTokenChromaDCTable(r *nal.Reader) (int, int) {
	avail := r.BitsLeft()
	peekLen := 8
	if avail < peekLen {
		peekLen = avail
	}
	if peekLen <= 0 {
		return 0, 0
	}
	bits := r.PeekBits(peekLen)
	bestLen, bestTC, bestTO := 0, 0, 0
	for tc := 0; tc <= 4; tc++ {
		maxTO := 3
		if tc < maxTO {
			maxTO = tc
		}
		for to := 0; to <= maxTO; to++ {
			idx := tc*4 + to
			l := int(chromaDCCoeffTokenLen[idx])
			if l == 0 || l > peekLen {
				continue
			}
			if bits>>uint(peekLen-l) == uint32(chromaDCCoeffTokenBits[idx]) {
				if bestLen == 0 || l < bestLen {
					bestLen, bestTC, bestTO = l, tc, to
				}
			}
		}
	}
	if bestLen > 0 {
		r.ReadBits(bestLen)
		return bestTC, bestTO
	}
	r.ReadBit()
	return 0, 0
}

func decodeChromaDCTotalZerosTable(r *nal.Reader, totalCoeff int) int {
	if totalCoeff <= 0 || totalCoeff >= 4 {
		return 0
	}
	idx := totalCoeff - 1
	avail := r.BitsLeft()
	peekLen := 3
	if avail < peekLen {
		peekLen = avail
	}
	if peekLen <= 0 {
		return 0
	}
	bits := r.PeekBits(peekLen)
	for val := 0; val < 4; val++ {
		l := int(chromaDCTotalZerosLen[idx][val])
		if l == 0 || l > peekLen {
			continue
		}
		if bits>>uint(peekLen-l) == uint32(chromaDCTotalZerosBits[idx][val]) {
			r.ReadBits(l)
			return val
		}
	}
	r.ReadBit()
	return 0
}
