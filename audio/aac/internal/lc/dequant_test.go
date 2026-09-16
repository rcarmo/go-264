package lc

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func directDequant(q int32, gain float64) float64 {
	v := math.Abs(float64(q))
	v = v * math.Cbrt(v) * gain
	if q < 0 {
		v = -v
	}
	return v
}

func TestDequantEveryMagnitudeAndGain(t *testing.T) {
	quant := make([]int32, 2*quantLimit+1)
	for i := range quant {
		quant[i] = int32(i - quantLimit)
	}
	got := make([]float64, len(quant))
	for scale := 0; scale <= 255; scale++ {
		gain := math.Exp2(float64(scale-100) * 0.25)
		if !dequantBand(got, quant, gain) {
			t.Fatal("valid input rejected")
		}
		for i, q := range quant {
			want := directDequant(q, gain)
			if math.Float64bits(got[i]) != math.Float64bits(want) {
				t.Fatalf("scale=%d q=%d got=%x want=%x", scale, q, math.Float64bits(got[i]), math.Float64bits(want))
			}
		}
	}
}

func TestDequantBoundsTailAndValidation(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 7, 127, 128, 1024} {
		for off := 0; off < 2; off++ {
			quant := make([]int32, n+2)
			got := make([]float64, n+2)
			for i := range quant {
				quant[i], got[i] = int32(i%19)-9, 123
			}
			before := slices.Clone(quant)
			want := slices.Clone(got)
			for i := off; i < off+n; i++ {
				want[i] = directDequant(quant[i], 0.25)
			}
			if !dequantBand(got[off:off+n], quant[off:off+n], 0.25) || !slices.Equal(got, want) || !slices.Equal(quant, before) {
				t.Fatalf("n%d/off%d", n, off)
			}
		}
	}
	for _, bad := range []int32{-8192, 8192, math.MinInt32, math.MaxInt32} {
		got := []float64{11, 22, 33}
		if dequantBand(got, []int32{1, bad, 2}, 1) || !slices.Equal(got, []float64{11, 22, 33}) {
			t.Fatal("invalid index not rejected transactionally", bad)
		}
	}
	if dequantBand(make([]float64, 1), []int32{1, 2}, 1) {
		t.Fatal("length mismatch")
	}
}

func BenchmarkDequant(b *testing.B) {
	for _, n := range []int{16, 128, 1024} {
		for _, mode := range []string{"direct", "table-scalar", "table-simd"} {
			b.Run(fmt.Sprintf("n%d/%s", n, mode), func(b *testing.B) {
				q, dst := make([]int32, n), make([]float64, n)
				for i := range q {
					q[i] = int32((i*97)%16383) - 8191
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					switch mode {
					case "direct":
						for j := range dst {
							dst[j] = directDequant(q[j], 0.25)
						}
					case "table-scalar":
						dequantTableScalar(dst, q, 0.25)
					default:
						dequantApply(dst, q, 0.25)
					}
				}
			})
		}
	}
}
