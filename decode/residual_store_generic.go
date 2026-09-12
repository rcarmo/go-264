//go:build (!amd64 && !arm64) || purego

package decode

func residualAddStoreSIMD(dst []byte, dstStride int, predicted []byte, predStride int, residual []int16, residualStride, w, h int) bool {
	return false
}
