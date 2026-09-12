//go:build arm64 && !purego

package filterbank

func fftStage(x, roots []complex128, half, stride int) {
	if len(x) != 0 {
		fftStageNEON(&x[0], &roots[0], len(x), half, stride)
	}
}

//go:noescape
func fftStageNEON(x, roots *complex128, n, half, stride int)
