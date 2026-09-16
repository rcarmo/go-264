package transform

// IDCT4x4Batch applies the inverse 4×4 integer transform to a contiguous batch
// of row-major 4×4 coefficient blocks. It uses the same scalar/SIMD dispatch as
// IDCT4x4 while avoiding per-block slice construction in callers that already
// store residuals as [][16]int16.
func IDCT4x4Batch(blocks [][16]int16) {
	IDCT4x4BatchMask(blocks, ^uint64(0))
}

// IDCT4x4BatchMask applies IDCT4x4 to blocks whose bit is set in mask. This
// lets callers keep dense residual arrays while skipping transform work for
// known-zero blocks (for example, uncoded CBP luma groups).
func IDCT4x4BatchMask(blocks [][16]int16, mask uint64) {
	if len(blocks) == 0 || mask == 0 {
		return
	}
	if hasSSE2Transform {
		for i := range blocks {
			if mask&(uint64(1)<<uint(i)) != 0 {
				IDCT4x4_SSE2(&blocks[i][0])
			}
		}
		return
	}
	if HasNEON {
		for i := range blocks {
			if mask&(uint64(1)<<uint(i)) != 0 {
				IDCT4x4_NEON(&blocks[i][0])
			}
		}
		return
	}
	for i := range blocks {
		if mask&(uint64(1)<<uint(i)) != 0 {
			IDCT4x4Scalar(blocks[i][:])
		}
	}
}
