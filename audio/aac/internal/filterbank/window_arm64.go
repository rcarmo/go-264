//go:build arm64 && !purego

package filterbank

func applyWindow(dst, src, weights []float64, reverse, add bool) {
	// QEMU parity exposed vector-add differences for subnormals and some
	// rounded sums. Keep accumulation scalar; overwrite multiplication is exact.
	if add {
		windowScalar(dst, src, weights, reverse, true)
		return
	}
	if len(dst) == 0 {
		return
	}
	var flags uint64
	if reverse {
		flags = 1
	}
	windowNEON(&dst[0], &src[0], &weights[0], len(dst), flags)
}
func addOverlap(dst, src, previous []float64) { overlapScalar(dst, src, previous) }

//go:noescape
func windowNEON(dst, src, weights *float64, n int, flags uint64)
