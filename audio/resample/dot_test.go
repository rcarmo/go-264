package resample

import (
	"math"
	"testing"
)

func TestDotExactTails(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 15, 48, 145, 511, 1153} {
		for off := 0; off < 3; off++ {
			aa, bb := make([]float64, n+off), make([]float64, n+off)
			a, b := aa[off:], bb[off:]
			for i := range a {
				a[i] = math.Sin(float64(i)*3.7) * 1e3
				b[i] = math.Cos(float64(i)*0.17) * 1e-3
			}
			want, got := dotScalar(a, b), dot(a, b)
			if math.Float64bits(want) != math.Float64bits(got) {
				t.Fatal(n, off, want, got)
			}
		}
	}
}
func BenchmarkDot(b *testing.B) {
	a, c := make([]float64, 145), make([]float64, 145)
	for i := range a {
		a[i] = float64(i) / 145
		c[i] = 1
	}
	b.Run("scalar", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dotResult = dotScalar(a, c)
		}
	})
	b.Run("dispatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dotResult = dot(a, c)
		}
	})
}

var dotResult float64
