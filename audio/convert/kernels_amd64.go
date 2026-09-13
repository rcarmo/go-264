//go:build amd64 && !purego

package convert

var s16HasAVX2 = s16CPUHasAVX2()

func s16Kernel(dst []int16, src []float64) {
	if len(src) == 0 {
		return
	}
	bulk := 0
	if s16HasAVX2 {
		bulk = len(src) &^ 3
		if bulk != 0 {
			s16AVX2(&dst[0], &src[0], bulk)
		}
	}
	if bulk != len(src) {
		s16SSE2(&dst[bulk], &src[bulk], len(src)-bulk)
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
func s16CPUHasAVX2() bool

//go:noescape
func s16AVX2(dst *int16, src *float64, n int)

//go:noescape
func s16SSE2(dst *int16, src *float64, n int)

//go:noescape
func stereoToMonoSSE2(dst, src *float64, frames int)

//go:noescape
func monoToStereoSSE2(dst, src *float64, frames int)

//go:noescape
func interleaveStereoSSE2(dst, left, right *float64, frames int)
