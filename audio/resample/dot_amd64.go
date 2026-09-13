//go:build amd64 && !purego

package resample

// SSE2 is guaranteed by the Go amd64 baseline. Optional AVX2 dispatch requires
// complete CPU/OS XMM+YMM state support. Ordered scalar adds preserve the Go
// reference's rounding exactly in either path.
var dotHasAVX2 = dotCPUHasAVX2()

func dot(a, b []float64) float64 {
	if len(a) != len(b) {
		panic("resample: internal dot shape")
	}
	if len(a) == 0 {
		return 0
	}
	if dotHasAVX2 {
		return dotAVX2(&a[0], &b[0], len(a))
	}
	return dotSSE2(&a[0], &b[0], len(a))
}

//go:noescape
func dotCPUHasAVX2() bool

//go:noescape
func dotAVX2(a, b *float64, n int) float64

//go:noescape
func dotSSE2(a, b *float64, n int) float64
