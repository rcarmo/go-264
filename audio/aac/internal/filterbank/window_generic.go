//go:build (!amd64 && !arm64) || purego

package filterbank

func applyWindow(dst, src, weights []float64, reverse, add bool) {
	windowScalar(dst, src, weights, reverse, add)
}

func addOverlap(dst, src, previous []float64) {
	overlapScalar(dst, src, previous)
}
