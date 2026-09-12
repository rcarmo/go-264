//go:build arm64 && !purego

package me

import (
	"math/rand"
	"testing"
)

func TestSmallSADNEONFullRange(t *testing.T) {
	rng := rand.New(rand.NewSource(8264))
	for _, size := range []int{4, 8} {
		for _, strides := range [][2]int{{size, size}, {size + 1, size + 7}, {31, 47}} {
			a := make([]byte, (size-1)*strides[0]+size)
			b := make([]byte, (size-1)*strides[1]+size)
			for n := 0; n < 10000; n++ {
				for i := range a {
					a[i] = byte(rng.Intn(256))
				}
				for i := range b {
					b[i] = byte(rng.Intn(256))
				}
				if got, want := sadSmall(a, b, strides[0], strides[1], size), sadSmallScalar(a, b, strides[0], strides[1], size); got != want {
					t.Fatal(size, strides, n, got, want)
				}
			}
		}
	}
}
