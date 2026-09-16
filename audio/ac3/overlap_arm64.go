//go:build arm64 && !purego

package ac3

func overlapAdd(output, transformed, previous []float64) {
	if len(output) != 0 {
		overlapAddNEON(&output[0], &transformed[0], &previous[0], len(output))
	}
}

//go:noescape
func overlapAddNEON(output, transformed, previous *float64, n int)
