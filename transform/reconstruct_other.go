//go:build !arm64 || purego || !go1.27

package transform

// Reconstruct4x4Available reports whether the fused 4x4 kernel is enabled.
func Reconstruct4x4Available() bool { return false }

// Reconstruct4x4 returns false without writing when no fused kernel is
// available, so callers use the existing dequantization/IDCT/add path.
func Reconstruct4x4(dst, prediction []byte, coeff *[16]int16, dstStride, predStride, qp int) bool {
	return false
}
