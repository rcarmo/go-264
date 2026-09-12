package me

// SAD (Sum of Absolute Differences) — core motion estimation metric.
// Used to compare blocks in the encoder and for mode decision.

// SAD16x16 computes the sum of absolute differences between two 16×16 blocks.
func SAD16x16(a, b []uint8, strideA, strideB int) uint32 {
	if (hasSSE2 || hasNEON) && sad16Safe(a, b, strideA, strideB) {
		return SAD16x16_ASM(&a[0], &b[0], strideA, strideB)
	}
	var sad uint32
	for y := 0; y < 16; y++ {
		rowA := a[y*strideA : y*strideA+16]
		rowB := b[y*strideB : y*strideB+16]
		for x := 0; x < 16; x++ {
			d := int(rowA[x]) - int(rowB[x])
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
	}
	return sad
}

func sad16Safe(a, b []uint8, strideA, strideB int) bool {
	if strideA < 16 || strideB < 16 {
		return false
	}
	// Each kernel reads only16bytes from each of16rows.
	return len(a) >= 15*strideA+16 && len(b) >= 15*strideB+16
}

// SAD8x8 computes SAD for an 8×8 block.
func SAD8x8(a, b []uint8, strideA, strideB int) uint32 {
	return uint32(sadSmall(a, b, strideA, strideB, 8))
}

// SAD4x4 computes SAD for a 4×4 block.
func SAD4x4(a, b []uint8, strideA, strideB int) uint32 {
	return uint32(sadSmall(a, b, strideA, strideB, 4))
}

// SATD4x4 computes the Sum of Absolute Transformed Differences (Hadamard).
// More accurate than SAD for rate-distortion decisions.
func SATD4x4(a, b []uint8, strideA, strideB int) uint32 {
	// Compute residual
	var diff [16]int16
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			diff[y*4+x] = int16(a[y*strideA+x]) - int16(b[y*strideB+x])
		}
	}

	// 4×4 Hadamard transform
	// Horizontal
	for i := 0; i < 4; i++ {
		r := diff[i*4 : i*4+4]
		a0 := r[0] + r[1]
		a1 := r[2] + r[3]
		a2 := r[0] - r[1]
		a3 := r[2] - r[3]
		r[0] = a0 + a1
		r[1] = a2 + a3
		r[2] = a0 - a1
		r[3] = a2 - a3
	}
	// Vertical
	for j := 0; j < 4; j++ {
		a0 := diff[j] + diff[4+j]
		a1 := diff[8+j] + diff[12+j]
		a2 := diff[j] - diff[4+j]
		a3 := diff[8+j] - diff[12+j]
		diff[j] = a0 + a1
		diff[4+j] = a2 + a3
		diff[8+j] = a0 - a1
		diff[12+j] = a2 - a3
	}

	// Sum of absolute values
	var satd uint32
	for _, v := range diff {
		if v < 0 {
			v = -v
		}
		satd += uint32(v)
	}
	return (satd + 1) >> 1
}
