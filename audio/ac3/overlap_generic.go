//go:build (!amd64 && !arm64) || purego

package ac3

func overlapAdd(output, transformed, previous []float64) {
	overlapAddScalar(output, transformed, previous)
}
