//go:build amd64 && !purego

package resample

import (
	"math"
	"math/rand"
	"testing"
)

func TestDotDispatchCanForceSSE2(t *testing.T) {
	old := dotHasAVX2
	dotHasAVX2 = false
	defer func() { dotHasAVX2 = old }()
	a := []float64{1, -2, 3, -4, 5}
	b := []float64{-0.5, 0.25, 0.125, -0.0625, 0.03125}
	if got, want := dot(a, b), dotScalar(a, b); math.Float64bits(got) != math.Float64bits(want) {
		t.Fatalf("SSE2 dispatch=%v scalar=%v", got, want)
	}
}

func TestDotAVX2MatchesSSE2(t *testing.T) {
	if !dotHasAVX2 {
		t.Skip("AVX2 with OS XMM/YMM state unavailable")
	}
	rng := rand.New(rand.NewSource(26418))
	values := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 1, -1, math.MaxFloat64 / 16, -math.MaxFloat64 / 16}
	for _, n := range []int{1, 2, 3, 4, 5, 7, 15, 48, 145, 511, 1153} {
		for off := 0; off < 3; off++ {
			a, b := make([]float64, n+off), make([]float64, n+off)
			for i := 0; i < n; i++ {
				a[off+i] = math.Ldexp(rng.Float64()*2-1, i%80-40)
				b[off+i] = math.Ldexp(rng.Float64()*2-1, i%70-35)
				if i < len(values) {
					a[off+i], b[off+i] = values[i], values[len(values)-1-i]
				}
			}
			got := dotAVX2(&a[off], &b[off], n)
			want := dotSSE2(&a[off], &b[off], n)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("n=%d off=%d AVX2=%016x SSE2=%016x", n, off, math.Float64bits(got), math.Float64bits(want))
			}
		}
	}
}
