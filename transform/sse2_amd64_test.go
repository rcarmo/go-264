//go:build amd64 && !purego

package transform

import (
	"math/rand"
	"testing"
)

func TestSSE2MatchesLegacyAssemblyFullRange(t *testing.T) {
	r := rand.New(rand.NewSource(264))
	for n := 0; n < 20000; n++ {
		var a [16]int16
		for i := range a {
			a[i] = int16(r.Uint32())
		}
		b, c := a, a
		IDCT4x4_SSE2(&a[0])
		IDCT4x4_AVX2(&b[0])
		idct4WideReference(&c)
		if a != b || a != c {
			t.Fatal("IDCT legacy/range", n, a, b, c)
		}
		for i := range a {
			a[i] = int16(r.Uint32())
		}
		b = a
		DCT4x4_SSE2(&a[0])
		DCT4x4_AVX2(&b[0])
		if a != b {
			t.Fatal("DCT legacy/range", n)
		}
	}
}
