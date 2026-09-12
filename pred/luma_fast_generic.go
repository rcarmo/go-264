//go:build !amd64 || purego

package pred

func interLumaFast(out []byte, outStride int, ref []byte, refStride, baseX, baseY, w, h int, mv MotionVector) bool {
	return false
}
