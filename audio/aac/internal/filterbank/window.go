package filterbank

// windowScalar preserves multiplication before addition and the existing
// traversal order. Buffers are disjoint or exactly aliased, never partly aliased.
func windowScalar(dst, src, weights []float64, reverse, add bool) {
	for i := range dst {
		j := i
		if reverse {
			j = len(dst) - 1 - i
		}
		v := src[i] * weights[j]
		if add {
			dst[i] += v
		} else {
			dst[i] = v
		}
	}
}

func overlapScalar(dst, src, previous []float64) {
	for i := range dst {
		dst[i] = src[i] + previous[i]
	}
}
