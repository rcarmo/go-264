//go:build (!amd64 && !arm64) || purego

package filterbank

func fftStage(x, roots []complex128, half, stride int) {
	fftStageScalar(x, roots, half, stride)
}
