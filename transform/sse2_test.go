package transform

import (
	"math/rand"
	"testing"
)

// Wide reference is written by explicit matrix-axis passes, independently of
// the SIMD transpose sequence. Stored pass results narrow to int16.
func idct4WideReference(block *[16]int16) {
	for pass := 0; pass < 2; pass++ {
		for axis := 0; axis < 4; axis++ {
			var v [4]int32
			for i := range v {
				idx := axis*4 + i
				if pass == 1 {
					idx = i*4 + axis
				}
				v[i] = int32(block[idx])
			}
			a, b, c, d := v[0]+v[2], v[0]-v[2], (v[1]>>1)-v[3], v[1]+(v[3]>>1)
			o := [4]int32{a + d, b + c, b - c, a - d}
			for i, x := range o {
				idx := axis*4 + i
				if pass == 1 {
					idx = i*4 + axis
					x = (x + 32) >> 6
				}
				block[idx] = int16(x)
			}
		}
	}
}

func TestTransformFullRangeScalarSIMD(t *testing.T) {
	rng := rand.New(rand.NewSource(264))
	for n := 0; n < 20000; n++ {
		var src [18]int16
		for i := range src {
			src[i] = int16(rng.Uint32())
		}
		if n < 16 {
			for i := 0; i < 16; i++ {
				src[i+1] = 0
			}
			src[n+1] = -32768
		}
		for _, inverse := range []bool{true, false} {
			got, want := src, src
			var ref [16]int16
			copy(ref[:], src[1:17])
			if inverse {
				idct4WideReference(&ref)
				IDCT4x4(got[1:17])
				IDCT4x4Scalar(want[1:17])
			} else {
				DCT4x4(got[1:17])
				DCT4x4Scalar(want[1:17])
				copy(ref[:], want[1:17])
			}
			if got != want {
				t.Fatalf("inverse%t case%d got%v want%v", inverse, n, got, want)
			}
			for i, v := range ref {
				if got[i+1] != v {
					t.Fatalf("reference inverse%t case%d sample%d", inverse, n, i)
				}
			}
		}
	}
}

func BenchmarkPackedTransform4(b *testing.B) {
	for _, kind := range []string{"inverse", "forward"} {
		for _, mode := range []string{"scalar", "dispatch"} {
			b.Run(kind+"/"+mode, func(b *testing.B) {
				input := [16]int16{17, -34, 56, -91, 12, 15, -7, 9, 124, -242, 103, 51, 0, 3, -5, 100}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					x := input
					if kind == "inverse" {
						if mode == "scalar" {
							IDCT4x4Scalar(x[:])
						} else {
							IDCT4x4(x[:])
						}
					} else {
						if mode == "scalar" {
							DCT4x4Scalar(x[:])
						} else {
							DCT4x4(x[:])
						}
					}
				}
			})
		}
	}
}
