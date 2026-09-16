//go:build amd64 && !purego

package filterbank

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

func TestFFTStageDispatchCanForceSSE2(t *testing.T) {
	old := fftHasAVX2
	fftHasAVX2 = false
	defer func() { fftHasAVX2 = old }()
	x := []complex128{complex(1, -2), complex(3, 4), complex(-5, 6), complex(7, -8)}
	want := slices.Clone(x)
	roots := makePlan(len(x)).roots
	fftStageScalar(want, roots, 2, 1)
	fftStage(x, roots, 2, 1)
	for i := range x {
		if !sameComplexBits(x[i], want[i]) {
			t.Fatalf("sample %d: SSE2 dispatch=%v scalar=%v", i, x[i], want[i])
		}
	}
}

func TestFFTStageAVX2MatchesSSE2(t *testing.T) {
	if !fftHasAVX2 {
		t.Skip("AVX2 with OS XMM/YMM state unavailable")
	}
	rng := rand.New(rand.NewSource(26413))
	values := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 1, -1}
	for _, n := range []int{4, 8, 16, shortTransform, longTransform} {
		roots := makePlan(n).roots
		for half := 2; half < n; half *= 2 {
			for offset := 0; offset < 2; offset++ {
				t.Run(fmt.Sprintf("n%d/half%d/offset%d", n, half, offset), func(t *testing.T) {
					x := make([]complex128, n+2)
					for i := range x {
						x[i] = complex(math.Ldexp(rng.Float64()*2-1, i%80-40), math.Ldexp(rng.Float64()*2-1, i%70-35))
						if i < len(values) {
							x[i] = complex(values[i], values[len(values)-1-i])
						}
					}
					want := slices.Clone(x)
					fftStageSSE2(&want[offset], &roots[0], n, half, n/(2*half))
					fftStageAVX2(&x[offset], &roots[0], n, half, n/(2*half))
					for i := range x {
						if !sameComplexBits(x[i], want[i]) {
							t.Fatalf("sample %d: AVX2=%v SSE2=%v", i, x[i], want[i])
						}
					}
				})
			}
		}
	}
}
