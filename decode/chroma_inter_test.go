package decode

import (
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func fillChromaInterPredReference(dst []uint8, plane []uint8, stride, width, height, baseX, baseY int, mv syntax.MotionVector) {
	dx := int(mv.X) >> 3
	dy := int(mv.Y) >> 3
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			sx, sy := baseX+dx+x, baseY+dy+y
			if sx < 0 {
				sx = 0
			}
			if sy < 0 {
				sy = 0
			}
			if sx >= width {
				sx = width - 1
			}
			if sy >= height {
				sy = height - 1
			}
			dst[y*8+x] = plane[sy*stride+sx]
		}
	}
}

func TestFillChromaInterPredFastPathMatchesReference(t *testing.T) {
	const stride = 24
	const width = 20
	const height = 18
	plane := make([]uint8, stride*height)
	for y := 0; y < height; y++ {
		for x := 0; x < stride; x++ {
			plane[y*stride+x] = uint8((x*13 + y*7) & 0xff)
		}
	}
	cases := []struct {
		name         string
		baseX, baseY int
		mv           syntax.MotionVector
	}{
		{"interior", 6, 5, syntax.MotionVector{X: 8, Y: 16}},
		{"left-edge", -2, 4, syntax.MotionVector{X: 0, Y: 0}},
		{"top-edge", 4, -2, syntax.MotionVector{X: 0, Y: 0}},
		{"right-bottom-edge", 17, 15, syntax.MotionVector{X: 0, Y: 0}},
	}
	var d Decoder
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, want [64]uint8
			d.fillChromaInterPred(got[:], plane, stride, width, height, tc.baseX, tc.baseY, tc.mv)
			fillChromaInterPredReference(want[:], plane, stride, width, height, tc.baseX, tc.baseY, tc.mv)
			if got != want {
				t.Fatalf("chroma pred mismatch\ngot:  %v\nwant: %v", got, want)
			}
		})
	}
}

func BenchmarkFillChromaInterPredInterior(b *testing.B) {
	const stride = 960
	const width = 960
	const height = 540
	plane := make([]uint8, stride*height)
	dst := make([]uint8, 64)
	for i := range plane {
		plane[i] = uint8(i)
	}
	var d Decoder
	mv := syntax.MotionVector{X: 8, Y: 16}
	b.ReportAllocs()
	b.SetBytes(64)
	for i := 0; i < b.N; i++ {
		d.fillChromaInterPred(dst, plane, stride, width, height, 128, 128, mv)
	}
}

func BenchmarkFillChromaInterPredClipped(b *testing.B) {
	const stride = 960
	const width = 960
	const height = 540
	plane := make([]uint8, stride*height)
	dst := make([]uint8, 64)
	for i := range plane {
		plane[i] = uint8(i)
	}
	var d Decoder
	mv := syntax.MotionVector{X: 8, Y: 16}
	b.ReportAllocs()
	b.SetBytes(64)
	for i := 0; i < b.N; i++ {
		d.fillChromaInterPred(dst, plane, stride, width, height, -2, -2, mv)
	}
}
