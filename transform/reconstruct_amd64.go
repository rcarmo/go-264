//go:build amd64 && !purego

package transform

//go:noescape
func reconstructAdd4x4SSE2(dst, prediction *byte, residual *int16, dstStride, predStride int)

// Reconstruct4x4Available reports whether the composed 4x4 SSE2 path is enabled.
func Reconstruct4x4Available() bool { return true }

// Reconstruct4x4 applies inverse scaling, packed inverse transform and clipped
// prediction addition using SSE2. A negative QP means coefficients are already
// scaled. Coefficients are copied because decoder syntax storage is immutable.
func Reconstruct4x4(dst, prediction []byte, coeff *[16]int16, dstStride, predStride, qp int) bool {
	if dstStride < 4 || predStride < 4 || len(dst) < 4 || len(prediction) < 4 ||
		dstStride > (len(dst)-4)/3 || predStride > (len(prediction)-4)/3 {
		return false
	}
	residual := *coeff
	if qp >= 0 {
		dequant4SSE2(&residual[0], &dequant4Packed[min(qp, 51)][0])
	}
	IDCT4x4_SSE2(&residual[0])
	reconstructAdd4x4SSE2(&dst[0], &prediction[0], &residual[0], dstStride, predStride)
	return true
}
