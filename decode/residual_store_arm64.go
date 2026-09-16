//go:build arm64 && !purego

package decode

func residualAddStoreSIMD(dst []byte, dstStride int, predicted []byte, predStride int, residual []int16, residualStride, w, h int) bool {
	if (w != 4 && w != 8) || byteSlicesOverlapPortable(dst, predicted) && &dst[0] != &predicted[0] {
		return false
	}
	residualAddStoreNEON(&dst[0], dstStride, &predicted[0], predStride, &residual[0], residualStride, w, h)
	return true
}

//go:noescape
func residualAddStoreNEON(dst *byte, dstStride int, predicted *byte, predStride int, residual *int16, residualStride, w, h int)
