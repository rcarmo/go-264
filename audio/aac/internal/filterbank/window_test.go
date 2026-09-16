package filterbank

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

func TestWindowAndOverlapScalarParity(t *testing.T) {
	rng := rand.New(rand.NewSource(264))
	for _, n := range []int{0, 1, 2, 3, 7, 16, 127, 128, 129, 1024} {
		for _, reverse := range []bool{false, true} {
			for _, add := range []bool{false, true} {
				for offset := 0; offset < 2; offset++ {
					t.Run(fmt.Sprintf("n%d/reverse%t/add%t/offset%d", n, reverse, add, offset), func(t *testing.T) {
						dst, src, w := make([]float64, n+2), make([]float64, n+2), make([]float64, n+2)
						for i := range dst {
							dst[i], src[i], w[i] = rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64()
						}
						beforeSrc, beforeW := slices.Clone(src), slices.Clone(w)
						want := slices.Clone(dst)
						windowScalar(want[offset:offset+n], src[offset:offset+n], w[offset:offset+n], reverse, add)
						applyWindow(dst[offset:offset+n], src[offset:offset+n], w[offset:offset+n], reverse, add)
						checkFloatBits(t, dst, want)
						if !slices.Equal(src, beforeSrc) || !slices.Equal(w, beforeW) {
							t.Fatal("input mutated")
						}
						overlapScalar(want[offset:offset+n], src[offset:offset+n], w[offset:offset+n])
						addOverlap(dst[offset:offset+n], src[offset:offset+n], w[offset:offset+n])
						checkFloatBits(t, dst, want)
					})
				}
			}
		}
	}
}

func TestWindowSignedZerosSubnormalAndExactAlias(t *testing.T) {
	src := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.MaxFloat64 / 4, -1, 1}
	w := []float64{-1, 1, -0.5, 0.5, 1, -1, 0.25}
	for _, reverse := range []bool{false, true} {
		for _, add := range []bool{false, true} {
			got, want := slices.Clone(src), slices.Clone(src)
			applyWindow(got, got, w, reverse, add)
			windowScalar(want, want, w, reverse, add)
			checkFloatBits(t, got, want)
		}
	}
	got, want := slices.Clone(src), slices.Clone(src)
	addOverlap(got, got, w)
	overlapScalar(want, want, w)
	checkFloatBits(t, got, want)
}

func checkFloatBits(t *testing.T, got, want []float64) {
	t.Helper()
	for i := range want {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("sample %d: got %g (%x), want %g (%x)", i, got[i], math.Float64bits(got[i]), want[i], math.Float64bits(want[i]))
		}
	}
}

func BenchmarkWindow(b *testing.B) {
	for _, n := range []int{128, 1024} {
		for _, mode := range []string{"scalar", "dispatch"} {
			b.Run(fmt.Sprintf("n%d/%s", n, mode), func(b *testing.B) {
				x, w, dst := make([]float64, n), halfWindow(ShapeKBD, n == 128), make([]float64, n)
				for i := range x {
					x[i] = float64(i%17) / 13
				}
				fn := windowScalar
				if mode == "dispatch" {
					fn = applyWindow
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					fn(dst, x, w, true, true)
				}
			})
		}
	}
}
