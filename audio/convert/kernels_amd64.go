//go:build amd64 && !purego

package convert

func s16Kernel(dst []int16, src []float64) {
	if len(src) > 0 {
		s16SSE2(&dst[0], &src[0], len(src))
	}
}
func stereoToMono(dst, src []float64) {
	if len(dst) > 0 {
		stereoToMonoSSE2(&dst[0], &src[0], len(dst))
	}
}
func monoToStereo(dst, src []float64) {
	if len(src) > 0 {
		monoToStereoSSE2(&dst[0], &src[0], len(src))
	}
}
func interleaveStereo(dst, left, right []float64) {
	if len(left) > 0 {
		interleaveStereoSSE2(&dst[0], &left[0], &right[0], len(left))
	}
}

//go:noescape
func s16SSE2(dst *int16, src *float64, n int)

//go:noescape
func stereoToMonoSSE2(dst, src *float64, frames int)

//go:noescape
func monoToStereoSSE2(dst, src *float64, frames int)

//go:noescape
func interleaveStereoSSE2(dst, left, right *float64, frames int)
