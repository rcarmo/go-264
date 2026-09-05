package decode

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func fillChromaInterPredReference(dst []uint8, plane []uint8, stride, width, height, baseX, baseY int, mv syntax.MotionVector) {
	// Use quotient/remainder rather than the production shift/mask split so
	// negative fractional motion is checked independently.
	dx, fx := int(mv.X)/8, int(mv.X)%8
	dy, fy := int(mv.Y)/8, int(mv.Y)%8
	if fx < 0 {
		dx, fx = dx-1, fx+8
	}
	if fy < 0 {
		dy, fy = dy-1, fy+8
	}
	sample := func(x, y int) int {
		return int(plane[max(0, min(y, height-1))*stride+max(0, min(x, width-1))])
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			sx, sy := baseX+dx+x, baseY+dy+y
			top := (8-fx)*sample(sx, sy) + fx*sample(sx+1, sy)
			bottom := (8-fx)*sample(sx, sy+1) + fx*sample(sx+1, sy+1)
			dst[y*8+x] = uint8(((8-fy)*top + fy*bottom + 32) / 64)
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

func TestFillChromaInterPredRectMatchesReference(t *testing.T) {
	const stride, width, height = 24, 20, 18
	plane := make([]uint8, stride*height)
	for i := range plane {
		plane[i] = 253 // Row padding must never be used as an edge sample.
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			plane[y*stride+x] = uint8((x*13 + y*29 + x*y*7) % 251)
		}
	}
	var d Decoder
	for _, size := range [][2]int{{8, 8}, {8, 4}, {4, 8}, {4, 4}, {4, 2}, {2, 4}, {2, 2}, {4, 3}, {1, 1}, {3, 5}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			w, h := size[0], size[1]
			destinations := [][2]int{{0, 0}}
			if w != 8 || h != 8 {
				destinations = append(destinations, [2]int{8 - w, 8 - h})
			}
			for _, tc := range []struct {
				name                 string
				baseX, baseY, dx, dy int
			}{
				{"interior", 6, 5, 1, 2},
				{"negative-motion", 6, 5, -2, -1},
				{"left-edge", 0, 4, -1, 0},
				{"top-edge", 4, 0, 0, -1},
				{"right-edge", width - 1, 4, 0, 0},
				{"bottom-edge", 4, height - 1, 0, 0},
				{"bottom-right", width - 1, height - 1, 0, 0},
				{"far-top-left", 0, 0, -4096, -4096},
				{"far-bottom-right", 0, 0, 4095, 4095},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for fy := 0; fy < 8; fy++ {
						for fx := 0; fx < 8; fx++ {
							mv := syntax.MotionVector{X: int16(tc.dx*8 + fx), Y: int16(tc.dy*8 + fy)}
							var full [64]uint8
							fillChromaInterPredReference(full[:], plane, stride, width, height, tc.baseX, tc.baseY, mv)
							for _, position := range destinations {
								dstX, dstY := position[0], position[1]
								var got [80]uint8
								for i := range got {
									got[i] = uint8(i*17 + 31)
								}
								want := got
								for y := 0; y < h; y++ {
									copy(want[(dstY+y)*8+dstX:(dstY+y)*8+dstX+w], full[y*8:y*8+w])
								}
								if w == 8 && h == 8 {
									wrapped := got
									d.fillChromaInterPred(wrapped[:], plane, stride, width, height, tc.baseX, tc.baseY, mv)
									if wrapped != want {
										t.Fatalf("full wrapper fraction (%d,%d) differs from reference", fx, fy)
									}
								}
								d.fillChromaInterPredRect(got[:], plane, stride, width, height, tc.baseX, tc.baseY, dstX, dstY, w, h, mv)
								if got != want {
									t.Fatalf("fraction (%d,%d), destination (%d,%d): prediction or untouched bytes differ\ngot: %v\nwant: %v", fx, fy, dstX, dstY, got, want)
								}
							}
						}
					}
				})
			}
		})
	}
}

func TestFillChromaInterPredOverlappingBuffers(t *testing.T) {
	// An edge halo must not turn the full-block API's sequential writes into
	// snapshot reads. The rectangle API has the opposite contract: its former
	// temporary predicted the whole partition before any output was copied.
	for _, origin := range [][2]int{{0, 0}, {-1, -1}, {4, 3}} {
		input := make([]byte, 32*32)
		for i := range input {
			input[i] = byte(i*77 + 13)
		}
		mv := syntax.MotionVector{X: 3, Y: 5}
		var d Decoder
		got, want := append([]byte(nil), input...), append([]byte(nil), input...)
		fillChromaInterPredScalar(want[1:65], want, 32, 32, 32, origin[0], origin[1], mv)
		d.fillChromaInterPred(got[1:65], got, 32, 32, 32, origin[0], origin[1], mv)
		if !bytes.Equal(got, want) {
			t.Fatalf("origin=%v: full prediction changed overlapping write order", origin)
		}

		got, want = append([]byte(nil), input...), append([]byte(nil), input...)
		var prediction [64]byte
		fillChromaInterPredReference(prediction[:], input, 32, 32, 32, origin[0], origin[1], mv)
		for y := 0; y < 4; y++ {
			copy(want[1+(1+y)*8+2:1+(1+y)*8+6], prediction[y*8:y*8+4])
		}
		d.fillChromaInterPredRect(got[1:65], got, 32, 32, 32, origin[0], origin[1], 2, 1, 4, 4, mv)
		if !bytes.Equal(got, want) {
			t.Fatalf("origin=%v: rectangle prediction did not preserve its source snapshot", origin)
		}
	}
}

func TestFillChromaInterPredNarrowFootprints(t *testing.T) {
	// Give each block only its bilinear footprint, with coded width smaller
	// than stride and no trailing padding on the final source/destination row.
	// Extreme samples also exercise the largest possible weighted sum.
	for _, w := range []int{1, 2, 3, 4, 8} {
		for _, h := range []int{1, 3, 8} {
			width, height, stride := w+1, h+1, w+4
			plane := make([]byte, (height-1)*stride+width)
			for pattern := 0; pattern < 3; pattern++ {
				for i := range plane {
					plane[i] = 0
					if pattern == 1 || (pattern == 2 && i%2 == 0) {
						plane[i] = 255
					}
				}
				for fy := 0; fy < 8; fy++ {
					for fx := 0; fx < 8; fx++ {
						mv := syntax.MotionVector{X: int16(fx), Y: int16(fy)}
						var prediction, got [64]byte
						fillChromaInterPredReference(prediction[:], plane, stride, width, height, 0, 0, mv)
						for i := range got {
							got[i] = 0xa5
						}
						want := got
						for y := 0; y < h; y++ {
							copy(want[y*8:y*8+w], prediction[y*8:y*8+w])
						}
						end := (h-1)*8 + w
						if !fillChromaInterPredBlock(got[:end:end], plane, stride, width, height, 0, 0, w, h, mv) || got != want {
							t.Fatalf("%dx%d pattern=%d fraction=(%d,%d): prediction or untouched bytes differ", w, h, pattern, fx, fy)
						}
					}
				}
			}
		}
	}
}

func TestFillChromaInterPredRectMalformedInputsDoNotPanic(t *testing.T) {
	var d Decoder
	var dst [64]uint8
	d.fillChromaInterPredRect(dst[:], nil, 0, 0, 0, 0, 0, -1, 0, 4, 4, syntax.MotionVector{})
	d.fillChromaInterPredRect(dst[:], make([]uint8, 64), 8, 8, 8, 0, 0, 6, 0, 4, 4, syntax.MotionVector{})
	d.fillChromaInterPredRect(dst[:4], make([]uint8, 64), 8, 8, 8, 0, 0, 0, 0, 4, 4, syntax.MotionVector{})
	for i := range dst {
		dst[i] = 99
	}
	// The old full-block temporary supplied zeros for an invalid source,
	// without overwriting the other partitions already in the destination.
	d.fillChromaInterPredRect(dst[:], nil, 8, 8, 8, 0, 0, 2, 1, 4, 3, syntax.MotionVector{})
	for i, value := range dst {
		want := uint8(99)
		if x, y := i%8, i/8; x >= 2 && x < 6 && y >= 1 && y < 4 {
			want = 0
		}
		if value != want {
			t.Fatalf("invalid-source prediction at %d = %d, want %d", i, value, want)
		}
	}
}

func TestFillChromaInterPredMalformedInputsDoNotPanic(t *testing.T) {
	var d Decoder
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("fillChromaInterPred panicked on malformed input: %v", r)
		}
	}()
	var dst [64]uint8
	d.fillChromaInterPred(dst[:], nil, 8, 8, 8, 0, 0, syntax.MotionVector{})
	d.fillChromaInterPred(dst[:], []uint8{1, 2, 3}, 8, 8, 8, 0, 0, syntax.MotionVector{})
	d.fillChromaInterPred(dst[:], make([]uint8, 64), 4, 8, 8, 0, 0, syntax.MotionVector{})
	d.fillChromaInterPred(dst[:4], make([]uint8, 64), 8, 8, 8, 0, 0, syntax.MotionVector{})
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
