package transform

import (
	"math/rand"
	"testing"
)

func TestPackedDequantAllQP(t *testing.T) {
	r := rand.New(rand.NewSource(4264))
	for qp := 0; qp <= 51; qp++ {
		for n := 0; n < 200; n++ {
			var a [66]int16
			for i := range a {
				a[i] = int16(r.Uint32())
			}
			b := a
			Dequant8x8(a[1:65], qp)
			dequant8Scalar(b[1:65], qp)
			if a != b {
				t.Fatal("8x8", qp, n)
			}
			for _, start := range []int{0, 1} {
				a = b
				want := a
				dequant4Scalar(want[1:17], qp, start)
				dequant4Kernel(a[1:17], qp, start)
				if a != want {
					t.Fatal("4x4", qp, n, start)
				}
			}
		}
	}
}
func BenchmarkPackedDequant(b *testing.B) {
	for _, size := range []int{4, 8} {
		for _, mode := range []string{"scalar", "dispatch"} {
			name := "4x4/"
			if size == 8 {
				name = "8x8/"
			}
			b.Run(name+mode, func(b *testing.B) {
				var src [64]int16
				for i := range src {
					src[i] = int16(i*991 - 20000)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					a := src
					if size == 4 {
						if mode == "scalar" {
							dequant4Scalar(a[:16], 27, 0)
						} else {
							dequant4Kernel(a[:16], 27, 0)
						}
					} else {
						if mode == "scalar" {
							dequant8Scalar(a[:], 27)
						} else {
							dequant8Kernel(a[:], 27)
						}
					}
				}
			})
		}
	}
}
