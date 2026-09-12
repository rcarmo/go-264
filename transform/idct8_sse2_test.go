package transform

import (
	"math/rand"
	"testing"
)

func TestIDCT8PackedFullRange(t *testing.T) {
	r := rand.New(rand.NewSource(8264))
	for n := 0; n < 10000; n++ {
		var a [66]int16
		for i := range a {
			a[i] = int16(r.Uint32())
		}
		b := a
		IDCT8x8(a[1:65])
		IDCT8x8Scalar(b[1:65])
		if a != b {
			t.Fatalf("case%d: packed/scalar mismatch", n)
		}
	}
}

func BenchmarkPackedIDCT8(b *testing.B) {
	for _, mode := range []string{"scalar", "dispatch"} {
		b.Run(mode, func(b *testing.B) {
			var input [64]int16
			for i := range input {
				input[i] = int16(i*17 - 451)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				x := input
				if mode == "scalar" {
					IDCT8x8Scalar(x[:])
				} else {
					IDCT8x8(x[:])
				}
			}
		})
	}
}
