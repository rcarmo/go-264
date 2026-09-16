package decode

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func fillChromaInterPredScalar(dst, plane []byte, stride, width, height, baseX, baseY int, mv syntax.MotionVector) {
	intX, intY := int(mv.X)>>3, int(mv.Y)>>3
	fx, fy := int(mv.X)&7, int(mv.Y)&7
	sx0, sy0 := baseX+intX, baseY+intY
	sample := func(x, y int) int {
		if x < 0 {
			x = 0
		} else if x >= width {
			x = width - 1
		}
		if y < 0 {
			y = 0
		} else if y >= height {
			y = height - 1
		}
		return int(plane[y*stride+x])
	}
	wx0, wx1, wy0, wy1 := 8-fx, fx, 8-fy, fy
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			sx, sy := sx0+x, sy0+y
			a, b, c, d := sample(sx, sy), sample(sx+1, sy), sample(sx, sy+1), sample(sx+1, sy+1)
			dst[y*8+x] = byte((wx0*wy0*a + wx1*wy0*b + wx0*wy1*c + wx1*wy1*d + 32) >> 6)
		}
	}
}

func TestChromaInterSSE2AllFractionsAndEdges(t *testing.T) {
	rng := rand.New(rand.NewSource(8264))
	var dec Decoder
	for _, stride := range []int{9, 17, 31} {
		height := 23
		width := stride
		plane := make([]byte, stride*height)
		for pattern := 0; pattern < 4; pattern++ {
			for i := range plane {
				switch pattern {
				case 0:
					plane[i] = 0
				case 1:
					plane[i] = 255
				case 2:
					plane[i] = byte((i % 2) * 255)
				default:
					plane[i] = byte(rng.Intn(256))
				}
			}
			for _, base := range [][2]int{{0, 0}, {1, 2}, {width - 9, height - 9}, {-2, -3}, {width - 7, height - 6}} {
				for fx := 0; fx < 8; fx++ {
					for fy := 0; fy < 8; fy++ {
						mv := syntax.MotionVector{X: int16(fx), Y: int16(fy)}
						got, want := bytes.Repeat([]byte{0xa5}, 73), bytes.Repeat([]byte{0xa5}, 73)
						dec.fillChromaInterPred(got, plane, stride, width, height, base[0], base[1], mv)
						fillChromaInterPredScalar(want, plane, stride, width, height, base[0], base[1], mv)
						if !bytes.Equal(got, want) {
							t.Fatalf("stride%d pattern%d base%v frac%d,%d", stride, pattern, base, fx, fy)
						}
					}
				}
			}
		}
	}
}

func TestChromaInterSSE2AliasPreservesScalarWrites(t *testing.T) {
	for fx := 1; fx < 8; fx++ {
		for fy := 1; fy < 8; fy++ {
			want := make([]byte, 1024)
			for i := range want {
				want[i] = byte(i*77 + 13)
			}
			got := append([]byte(nil), want...)
			mv := syntax.MotionVector{X: int16(fx), Y: int16(fy)}
			var dec Decoder
			fillChromaInterPredScalar(want[8:72], want, 32, 32, 32, 2, 2, mv)
			dec.fillChromaInterPred(got[8:72], got, 32, 32, 32, 2, 2, mv)
			if !bytes.Equal(got, want) {
				t.Fatal(fx, fy)
			}
		}
	}
}

func TestChromaInterSSE2ZeroAlloc(t *testing.T) {
	plane := make([]byte, 32*32)
	var dst [64]byte
	var dec Decoder
	if n := testing.AllocsPerRun(100, func() { dec.fillChromaInterPred(dst[:], plane, 32, 32, 32, 4, 4, syntax.MotionVector{X: 3, Y: 5}) }); n != 0 {
		t.Fatal(n)
	}
}

func BenchmarkFillChromaInterPredFractional(b *testing.B) {
	plane := make([]byte, 960*540)
	for i := range plane {
		plane[i] = byte(i*73 + 11)
	}
	var dst [64]byte
	var d Decoder
	mv := syntax.MotionVector{X: 3, Y: 5}
	for _, mode := range []string{"scalar", "dispatch"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(64)
			for i := 0; i < b.N; i++ {
				if mode == "scalar" {
					fillChromaInterPredScalar(dst[:], plane, 960, 960, 540, 128, 128, mv)
				} else {
					d.fillChromaInterPred(dst[:], plane, 960, 960, 540, 128, 128, mv)
				}
			}
		})
	}
}
