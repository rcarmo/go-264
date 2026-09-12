//go:build arm64

package me

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestSAD16x16NEONFullRange(t *testing.T) {
	rng := rand.New(rand.NewSource(264))
	for _, strides := range [][2]int{{16, 16}, {17, 31}, {31, 17}, {64, 49}} {
		a := make([]byte, 15*strides[0]+16)
		b := make([]byte, 15*strides[1]+16)
		for n := 0; n < 10000; n++ {
			for i := range a {
				a[i] = byte(rng.Intn(256))
			}
			for i := range b {
				b[i] = byte(rng.Intn(256))
			}
			want := sad16Scalar(a, b, strides[0], strides[1])
			got := SAD16x16(a, b, strides[0], strides[1])
			if got != want {
				t.Fatalf("strides%v iteration%d got%d want%d", strides, n, got, want)
			}
		}
	}
}

func TestSAD16x16InvalidGeometryUsesSafeScalar(t *testing.T) {
	a, b := make([]byte, 256), make([]byte, 256)
	for i := range a {
		a[i], b[i] = byte(i), byte(255-i)
	}
	if got, want := SAD16x16(a, b, 0, 16), sad16Scalar(a, b, 0, 16); got != want {
		t.Fatalf("zero stride got%d want%d", got, want)
	}
	for i, tc := range []struct {
		a, b   []byte
		sa, sb int
	}{
		{nil, nil, 16, 16}, {make([]byte, 255), make([]byte, 256), 16, 16},
		{make([]byte, 256), make([]byte, 255), 16, 16},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("truncated scalar input must preserve panic")
				}
			}()
			_ = SAD16x16(tc.a, tc.b, tc.sa, tc.sb)
		})
	}
}

func sad16Scalar(a, b []byte, sa, sb int) uint32 {
	var sad uint32
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			d := int(a[y*sa+x]) - int(b[y*sb+x])
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
	}
	return sad
}
