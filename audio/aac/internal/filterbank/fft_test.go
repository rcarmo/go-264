package filterbank

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

func sameComplexBits(a, b complex128) bool {
	return math.Float64bits(real(a)) == math.Float64bits(real(b)) && math.Float64bits(imag(a)) == math.Float64bits(imag(b))
}

func TestFFTStageScalarParity(t *testing.T) {
	rng := rand.New(rand.NewSource(264))
	for _, n := range []int{2, 4, 8, 16, shortTransform, longTransform} {
		for half := 1; half < n; half *= 2 {
			for offset := 0; offset < 2; offset++ {
				name := fmt.Sprintf("n%d/half%d/offset%d", n, half, offset)
				t.Run(name, func(t *testing.T) {
					x := make([]complex128, n+2)
					for i := range x {
						x[i] = complex(math.Ldexp(rng.Float64()*2-1, i%80-40), math.Ldexp(rng.Float64()*2-1, i%70-35))
					}
					roots := makePlan(n).roots
					origRoots := slices.Clone(roots)
					want := slices.Clone(x)
					stride := n / (2 * half)
					fftStageScalar(want[offset:offset+n], roots, half, stride)
					fftStage(x[offset:offset+n], roots, half, stride)
					for i := range x {
						if !sameComplexBits(x[i], want[i]) {
							t.Fatalf("sample %d: got %v, want %v", i, x[i], want[i])
						}
					}
					if !slices.Equal(roots, origRoots) {
						t.Fatal("root table mutated")
					}
				})
			}
		}
	}
}

func TestFFTStageSignedZerosAndSubnormals(t *testing.T) {
	negZero := math.Copysign(0, -1)
	values := []float64{0, negZero, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 1, -1, math.MaxFloat64 / 4, -math.MaxFloat64 / 4}
	roots := []complex128{complex(1, negZero), complex(0.5, -0.5), complex(negZero, 1), complex(-1, negZero)}
	for _, a := range values {
		for _, b := range values {
			x := []complex128{complex(a, b), complex(b, a), complex(-a, b), complex(a, -b), complex(b, -a), complex(a, b), complex(b, a), complex(a, b)}
			want := slices.Clone(x)
			fftStageScalar(want, roots, 4, 1)
			fftStage(x, roots, 4, 1)
			for i := range x {
				if !sameComplexBits(x[i], want[i]) {
					t.Fatalf("a=%g b=%g i=%d: got %v, want %v", a, b, i, x[i], want[i])
				}
			}
		}
	}
	fftStage(nil, nil, 1, 1)
}

func BenchmarkFFTStage(b *testing.B) {
	for _, n := range []int{shortTransform, longTransform} {
		for _, mode := range []string{"scalar", "dispatch"} {
			b.Run(fmt.Sprintf("n%d/%s", n, mode), func(b *testing.B) {
				p := makePlan(n)
				x := make([]complex128, n)
				fn := fftStageScalar
				if mode == "dispatch" {
					fn = fftStage
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// All-zero input avoids growth/overflow while keeping the same
					// data-independent butterfly instruction stream.
					for half := 1; half < n; half *= 2 {
						fn(x, p.roots, half, n/(2*half))
					}
				}
			})
		}
	}
}
