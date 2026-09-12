package convert

import "math"

func s16Scalar(dst []int16, src []float64) {
	for i, v := range src {
		x := math.Round(v * 32768)
		if x > 32767 {
			x = 32767
		}
		if x < -32768 {
			x = -32768
		}
		dst[i] = int16(x)
	}
}

func stereoToMonoScalar(dst, src []float64) {
	for i := range dst {
		dst[i] = (src[2*i] + src[2*i+1]) * 0.5
	}
}
func monoToStereoScalar(dst, src []float64) {
	for i, v := range src {
		dst[2*i], dst[2*i+1] = v, v
	}
}
func interleaveStereoScalar(dst, left, right []float64) {
	for i := range left {
		dst[2*i], dst[2*i+1] = left[i], right[i]
	}
}
