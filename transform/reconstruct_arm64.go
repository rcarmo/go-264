//go:build arm64 && !purego && go1.27

package transform

//go:noescape
func reconstruct4x4NEON(dst, prediction *byte, coeff, scale *int16, dstStride, predStride int)

// Reconstruct4x4Available reports whether the fused 4x4 kernel is enabled.
func Reconstruct4x4Available() bool { return HasNEON }

// Reconstruct4x4 reconstructs a strided 4x4 block without
// materializing intermediate residuals. A negative QP means coefficients have
// already been scaled (including separately transformed Intra16 DC).
// Returns false without writing when SIMD is disabled or either slice cannot
// hold four rows of four pixels at its supplied stride.
func Reconstruct4x4(dst, prediction []byte, coeff *[16]int16, dstStride, predStride, qp int) bool {
	if !HasNEON {
		return false
	}
	// Unlike the coefficient array, slices do not establish a 4x4 footprint.
	// Validate this public boundary before assembly can bypass Go bounds checks.
	if dstStride < 4 || predStride < 4 || len(dst) < 4 || len(prediction) < 4 ||
		dstStride > (len(dst)-4)/3 || predStride > (len(prediction)-4)/3 {
		return false
	}
	var scale *int16
	if qp >= 0 {
		scale = &dequant4Packed[min(qp, 51)][0]
	}
	reconstruct4x4NEON(&dst[0], &prediction[0], &coeff[0], scale, dstStride, predStride)
	return true
}
