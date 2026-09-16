package decode

func residualAddStore(dst []byte, dstStride int, predicted []byte, predStride int, residual []int16, residualStride, w, h int) {
	if w <= 0 || h <= 0 || dstStride < w || predStride < w || residualStride < w ||
		len(dst) < w || len(predicted) < w || len(residual) < w ||
		h-1 > (len(dst)-w)/dstStride || h-1 > (len(predicted)-w)/predStride || h-1 > (len(residual)-w)/residualStride {
		return
	}
	if residualAddStoreSIMD(dst, dstStride, predicted, predStride, residual, residualStride, w, h) {
		return
	}
	residualAddStoreScalar(dst, dstStride, predicted, predStride, residual, residualStride, w, h)
}

func residualAddStoreScalar(dst []byte, dstStride int, predicted []byte, predStride int, residual []int16, residualStride, w, h int) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := int(predicted[y*predStride+x]) + int(residual[y*residualStride+x])
			if v < 0 {
				v = 0
			} else if v > 255 {
				v = 255
			}
			dst[y*dstStride+x] = byte(v)
		}
	}
}
