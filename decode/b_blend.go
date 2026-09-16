package decode

func biBlendRectPixels(dst, predL0, predL1 []byte, stride, w, h, w0, w1 int) {
	biBlendRectParams(dst, predL0, predL1, stride, w, h, w0, w1, 32, 6, 0)
}

func biBlendRectParams(dst, predL0, predL1 []byte, stride, w, h, w0, w1, round, shift, offset int) {
	if w <= 0 || h <= 0 || stride < w || len(dst) < w || len(predL0) < w || len(predL1) < w || shift <= 0 || shift > 30 ||
		h-1 > (len(dst)-w)/stride || h-1 > (len(predL0)-w)/stride || h-1 > (len(predL1)-w)/stride {
		return
	}
	plainAverage := w0 == 32 && w1 == 32 && round == 32 && shift == 6 && offset == 0
	if biBlendFast(dst, predL0, predL1, stride, w, h, w0, w1, round, shift, offset, plainAverage) {
		return
	}
	biBlendRectScalarParams(dst, predL0, predL1, stride, w, h, w0, w1, round, shift, offset, plainAverage)
}

func biBlendRectScalar(dst, predL0, predL1 []byte, stride, w, h, w0, w1 int) {
	biBlendRectScalarParams(dst, predL0, predL1, stride, w, h, w0, w1, 32, 6, 0, w0 == 32 && w1 == 32)
}

func biBlendRectScalarParams(dst, predL0, predL1 []byte, stride, w, h, w0, w1, round, shift, offset int, plainAverage bool) {
	for y := 0; y < h; y++ {
		row := y * stride
		for x := 0; x < w; x++ {
			idx := row + x
			if plainAverage {
				dst[idx] = byte((int(predL0[idx]) + int(predL1[idx]) + 1) >> 1)
			} else {
				dst[idx] = clipWeightedSample(((int(predL0[idx])*w0 + int(predL1[idx])*w1 + round) >> shift) + offset)
			}
		}
	}
}
