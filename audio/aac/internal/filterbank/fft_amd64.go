//go:build amd64 && !purego

package filterbank

// All amd64 CPUs support SSE2. No AVX/FMA or runtime feature probe is needed.
// Internal callers pass n complex values with n divisible by 2*half and a
// transform-plan root table covering (half-1)*stride.
func fftStage(x, roots []complex128, half, stride int) {
	if len(x) == 0 {
		return
	}
	fftStageSSE2(&x[0], &roots[0], len(x), half, stride)
}

//go:noescape
func fftStageSSE2(x, roots *complex128, n, half, stride int)
