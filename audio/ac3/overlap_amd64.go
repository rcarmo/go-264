//go:build amd64 && !purego

package ac3

func overlapAdd(output, transformed, previous []float64) {
	if len(output) != 0 {
		overlapAddSSE2(&output[0], &transformed[0], &previous[0], len(output))
	}
}

//go:noescape
func overlapAddSSE2(output, transformed, previous *float64, n int)
