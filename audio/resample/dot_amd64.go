//go:build amd64 && !purego

package resample

// SSE2 is guaranteed by the Go amd64 baseline. No AVX/FMA/OS-state detection is
// needed. Ordered scalar adds preserve the Go reference's rounding exactly.
func dot(a, b []float64) float64 {
	if len(a) != len(b) {
		panic("resample: internal dot shape")
	}
	if len(a) == 0 {
		return 0
	}
	return dotSSE2(&a[0], &b[0], len(a))
}

//go:noescape
func dotSSE2(a, b *float64, n int) float64
