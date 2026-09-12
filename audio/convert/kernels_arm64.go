//go:build arm64 && !purego

package convert

// Float-to-S16 rounding and stereo averaging preserve their scalar operation
// order until a separately qualified exact vector implementation exists.
func s16Kernel(dst []int16, src []float64) { s16Scalar(dst, src) }
func stereoToMono(dst, src []float64)      { stereoToMonoScalar(dst, src) }

func monoToStereo(dst, src []float64) {
	if len(src) > 0 {
		monoToStereoNEON(&dst[0], &src[0], len(src))
	}
}
func interleaveStereo(dst, left, right []float64) {
	if len(left) > 0 {
		interleaveStereoNEON(&dst[0], &left[0], &right[0], len(left))
	}
}

//go:noescape
func monoToStereoNEON(dst, src *float64, frames int)

//go:noescape
func interleaveStereoNEON(dst, left, right *float64, frames int)
