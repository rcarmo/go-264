//go:build !amd64 || purego

package convert

func s16Kernel(dst []int16, src []float64)        { s16Scalar(dst, src) }
func stereoToMono(dst, src []float64)             { stereoToMonoScalar(dst, src) }
func monoToStereo(dst, src []float64)             { monoToStereoScalar(dst, src) }
func interleaveStereo(dst, left, right []float64) { interleaveStereoScalar(dst, left, right) }
