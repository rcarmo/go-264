package filterbank

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func TestRotationsScalarParity(t *testing.T) {
	values := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, -1, 1, math.MaxFloat64 / 4, 0.125}
	for _, n := range []int{0, 1, 2, 3, 127, 128, 1024, 2048} {
		for offset := 0; offset < 2; offset++ {
			t.Run(fmt.Sprintf("n%d/offset%d", n, offset), func(t *testing.T) {
				coeff, dst := make([]float64, n+2), make([]float64, n+2)
				rot, complexDst := make([]complex128, n+2), make([]complex128, n+2)
				for i := range coeff {
					coeff[i], dst[i] = values[i%len(values)], 123
					rot[i] = complex(values[(i/len(values))%len(values)], values[(i/3)%len(values)])
					complexDst[i] = complex(123, 456)
				}
				want := slices.Clone(complexDst)
				rotateInputScalar(want[offset:offset+n], coeff[offset:offset+n], rot[offset:offset+n])
				rotateInput(complexDst[offset:offset+n], coeff[offset:offset+n], rot[offset:offset+n])
				for i := range want {
					if !sameComplexBits(complexDst[i], want[i]) {
						t.Fatalf("input rotation %d got %v want %v", i, complexDst[i], want[i])
					}
				}
				// Use bounded finite inputs for postrotation; non-finite internal
				// values are rejected by the public bank before state publication.
				for i := range rot {
					rot[i] = complex(float64(i%7)/8, float64(i%9)/16)
					complexDst[i] = complex(coeff[i], coeff[(i+1)%len(coeff)])
				}
				wantReal := slices.Clone(dst)
				rotateOutputScalar(wantReal[offset:offset+n], complexDst[offset:offset+n], rot[offset:offset+n], 1.0/1024)
				rotateOutput(dst[offset:offset+n], complexDst[offset:offset+n], rot[offset:offset+n], 1.0/1024)
				checkFloatBits(t, dst, wantReal)
			})
		}
	}
}

func BenchmarkRotations(b *testing.B) {
	for _, mode := range []string{"scalar", "dispatch"} {
		b.Run(mode, func(b *testing.B) {
			coeff := make([]float64, coeffCount)
			x, dst := make([]complex128, longTransform), make([]float64, longTransform)
			in, out := rotateInputScalar, rotateOutputScalar
			if mode == "dispatch" {
				in, out = rotateInput, rotateOutput
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				in(x[:coeffCount], coeff, longPlan.pre)
				out(dst, x, longPlan.post, 1.0/1024)
			}
		})
	}
}
