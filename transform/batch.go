package transform

// IDCT4x4Batch applies the inverse 4×4 integer transform to a contiguous batch
// of row-major 4×4 coefficient blocks. It uses the same scalar/SIMD dispatch as
// IDCT4x4 while avoiding per-block slice construction in callers that already
// store residuals as [][16]int16.
func IDCT4x4Batch(blocks [][16]int16) {
	if len(blocks) == 0 {
		return
	}
	if HasAVX2 {
		for i := range blocks {
			IDCT4x4_AVX2(&blocks[i][0])
		}
		return
	}
	if HasNEON {
		for i := range blocks {
			IDCT4x4_NEON(&blocks[i][0])
		}
		return
	}
	for i := range blocks {
		IDCT4x4Scalar(blocks[i][:])
	}
}
