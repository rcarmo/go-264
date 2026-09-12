package me

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestSmallSADPackedParity(t *testing.T) {
	r := rand.New(rand.NewSource(264))
	for _, n := range []int{4, 8} {
		for _, stride := range []int{n, n + 1, 32} {
			for off := 0; off < 16; off++ {
				a, b := make([]byte, stride*(n-1)+n+off), make([]byte, stride*(n-1)+n+off)
				r.Read(a)
				r.Read(b)
				want := sadSmallScalar(a[off:], b[off:], stride, stride, n)
				got := sadSmall(a[off:], b[off:], stride, stride, n)
				if got != want {
					t.Fatal(n, stride, off, got, want)
				}
			}
		}
	}
}
func BenchmarkSmallSAD(b *testing.B) {
	for _, n := range []int{4, 8} {
		for _, mode := range []string{"scalar", "dispatch"} {
			b.Run(fmt.Sprintf("n%d/%s", n, mode), func(b *testing.B) {
				a, c := make([]byte, 32*n), make([]byte, 32*n)
				for i := range a {
					a[i] = byte(i * 17)
					c[i] = byte(i * 31)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "scalar" {
						sadSmallScalar(a, c, 32, 32, n)
					} else {
						sadSmall(a, c, 32, 32, n)
					}
				}
			})
		}
	}
}
