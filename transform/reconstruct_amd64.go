//go:build amd64 && !purego

package transform

//go:noescape
func reconstruct4x4FusedSSE2(dst, prediction *byte, coeff, scale *int16, dstStride, predStride int)

// Reconstruct4x4Available reports whether the fused 4x4 SSE2 kernel is enabled.
func Reconstruct4x4Available() bool { return true }

// Reconstruct4x4 fuses optional inverse scaling, inverse transform and clipped
// prediction addition. A negative QP means coefficients are already scaled.
// The assembly reads coefficient syntax storage without modifying it.
func Reconstruct4x4(dst, prediction []byte, coeff *[16]int16, dstStride, predStride, qp int) bool {
	if dstStride < 4 || predStride < 4 || len(dst) < 4 || len(prediction) < 4 ||
		dstStride > (len(dst)-4)/3 || predStride > (len(prediction)-4)/3 {
		return false
	}
	var scale *int16
	if qp >= 0 {
		scale = &dequant4Packed[min(qp, 51)][0]
	}
	reconstruct4x4FusedSSE2(&dst[0], &prediction[0], &coeff[0], scale, dstStride, predStride)
	return true
}
