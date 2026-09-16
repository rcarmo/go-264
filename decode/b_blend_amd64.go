//go:build amd64 && !purego

package decode

func biBlendFast(dst, predL0, predL1 []byte, stride, w, h, w0, w1, round, shift, offset int, plainAverage bool) bool {
	if (w != 4 && w != 8 && w != 16) || stride < w || h <= 0 || w0 < -128 || w0 > 128 || w1 < -128 || w1 > 128 ||
		round < 0 || round > 128 || shift < 1 || shift > 8 || offset < -128 || offset > 127 ||
		!blendAliasSafe(dst, predL0) || !blendAliasSafe(dst, predL1) {
		return false
	}
	if plainAverage {
		biBlendAvgSSE2(&dst[0], &predL0[0], &predL1[0], stride, w, h)
	} else {
		biBlendWeightedSSE2(&dst[0], &predL0[0], &predL1[0], stride, w, h, w0, w1, round, shift, offset)
	}
	return true
}

func blendAliasSafe(dst, src []byte) bool {
	return &dst[0] == &src[0] || !byteSlicesOverlapPortable(dst, src)
}

//go:noescape
func biBlendAvgSSE2(dst, predL0, predL1 *byte, stride, w, h int)

//go:noescape
func biBlendWeightedSSE2(dst, predL0, predL1 *byte, stride, w, h, w0, w1, round, shift, offset int)
