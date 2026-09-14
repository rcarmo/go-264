//go:build !arm64 || purego || !go1.27

package pred

func interPredLumaSIMD(out []byte, outStride int, ref []byte, refStride, sx, sy, w, h, fx, fy int) bool {
	return false
}
