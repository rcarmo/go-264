//go:build amd64 && !purego

package filterbank

func applyWindow(dst, src, weights []float64, reverse, add bool) {
	if len(dst) == 0 {
		return
	}
	var flags uint64
	if reverse {
		flags |= 1
	}
	if add {
		flags |= 2
	}
	windowSSE2(&dst[0], &src[0], &weights[0], len(dst), flags)
}

func addOverlap(dst, src, previous []float64) {
	if len(dst) == 0 {
		return
	}
	overlapSSE2(&dst[0], &src[0], &previous[0], len(dst))
}

//go:noescape
func windowSSE2(dst, src, weights *float64, n int, flags uint64)

//go:noescape
func overlapSSE2(dst, src, previous *float64, n int)
