//go:build amd64 && !purego

package transform

import (
	"math/rand"
	"testing"
)

func TestTranspose8Exact(t *testing.T) {
	var src, dst, twice [64]int16
	for i := range src {
		src[i] = int16(i*997 - 32000)
	}
	transpose8SSE2(&dst[0], &src[0])
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if dst[y*8+x] != src[x*8+y] {
				t.Fatal(y, x)
			}
		}
	}
	transpose8SSE2(&twice[0], &dst[0])
	if src != twice {
		t.Fatal("transpose involution")
	}
}

func TestIDCT8PackedMatchesLegacyFullRange(t *testing.T) {
	r := rand.New(rand.NewSource(2648))
	for n := 0; n < 10000; n++ {
		var a [64]int16
		for i := range a {
			a[i] = int16(r.Uint32())
		}
		b := a
		idct8Packed(a[:])
		IDCT8x8_ASM(&b[0])
		if a != b {
			t.Fatalf("case%d: legacy mismatch", n)
		}
	}
}
