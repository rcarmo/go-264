package lc

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func sameBand(t *testing.T, a, b []float64) {
	t.Helper()
	for i := range a {
		if math.Float64bits(a[i]) != math.Float64bits(b[i]) {
			t.Fatalf("sample%d got%x want%x", i, math.Float64bits(a[i]), math.Float64bits(b[i]))
		}
	}
}
func TestStereoBandKernelsParity(t *testing.T) {
	vals := []float64{0, math.Copysign(0, -1), 1, -1, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 1e100, -1e100}
	for _, n := range []int{0, 1, 2, 3, 7, 128, 1024} {
		for off := 0; off < 2; off++ {
			t.Run(fmt.Sprintf("n%d/off%d", n, off), func(t *testing.T) {
				l, r := make([]float64, n+2), make([]float64, n+2)
				for i := range l {
					l[i] = vals[i%len(vals)]
					r[i] = vals[(i+3)%len(vals)]
				}
				wl, wr := slices.Clone(l), slices.Clone(r)
				midSide(l[off:off+n], r[off:off+n])
				midSideScalar(wl[off:off+n], wr[off:off+n])
				sameBand(t, l, wl)
				sameBand(t, r, wr)
				for _, gain := range []float64{-1, 1, 0, math.Copysign(0, -1), 0.125} {
					scaleBand(r[off:off+n], l[off:off+n], gain)
					scaleBandScalar(wr[off:off+n], wl[off:off+n], gain)
					sameBand(t, r, wr)
				}
				scaleBand(l[off:off+n], l[off:off+n], 0.75)
				scaleBandScalar(wl[off:off+n], wl[off:off+n], 0.75)
				sameBand(t, l, wl)
			})
		}
	}
}
func TestTNSBandOrderedParity(t *testing.T) {
	for order := 1; order <= 12; order++ {
		for _, reverse := range []bool{false, true} {
			for _, n := range []int{0, 1, 3, 17, 128, 1024} {
				t.Run(fmt.Sprintf("order%d/reverse%t/n%d", order, reverse, n), func(t *testing.T) {
					coeff := make([]float64, order)
					for i := range coeff {
						coeff[i] = float64(i%3-1) / 32
					}
					src := make([]float64, n+2)
					for i := range src {
						src[i] = float64(i%13-6) / 16
					}
					want := slices.Clone(src)
					copyCoeffs := slices.Clone(coeff)
					tnsBand(src[1:1+n], coeff, reverse)
					tnsBandScalar(want[1:1+n], coeff, reverse)
					sameBand(t, src, want)
					if !slices.Equal(coeff, copyCoeffs) {
						t.Fatal("coeff mutated")
					}
				})
			}
		}
	}
}
func BenchmarkReconstructionBands(b *testing.B) {
	for _, kind := range []string{"scale", "mid-side", "tns1", "tns4", "tns12"} {
		for _, mode := range []string{"scalar", "dispatch"} {
			b.Run(kind+"/"+mode, func(b *testing.B) {
				x, y := make([]float64, 128), make([]float64, 128)
				order := 4
				if kind == "tns1" {
					order = 1
				}
				if kind == "tns12" {
					order = 12
				}
				coeff := make([]float64, order)
				for i := range coeff {
					coeff[i] = 0.01
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					switch kind {
					case "scale":
						if mode == "scalar" {
							scaleBandScalar(y, x, 0.5)
						} else {
							scaleBand(y, x, 0.5)
						}
					case "mid-side":
						if mode == "scalar" {
							midSideScalar(x, y)
						} else {
							midSide(x, y)
						}
					default:
						if mode == "scalar" {
							tnsBandScalar(x, coeff, false)
						} else {
							tnsBand(x, coeff, false)
						}
					}
				}
			})
		}
	}
}
