package decode

import (
	"bytes"
	"math/rand"
	"testing"
)

func blendReference(dst, a, b []byte, stride, w, h, w0, w1 int) {
	blendReferenceParams(dst, a, b, stride, w, h, w0, w1, 32, 6, 0, w0 == 32 && w1 == 32)
}

func blendReferenceParams(dst, a, b []byte, stride, w, h, w0, w1, round, shift, offset int, plainAverage bool) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*stride + x
			if plainAverage {
				dst[i] = byte((int(a[i]) + int(b[i]) + 1) >> 1)
			} else {
				dst[i] = clipWeightedSample(((int(a[i])*w0 + int(b[i])*w1 + round) >> shift) + offset)
			}
		}
	}
}

func TestBiBlendAllBytesAndWeightBoundaries(t *testing.T) {
	weights := [][2]int{{32, 32}, {-64, 128}, {128, -64}, {0, 64}, {64, 0}, {1, 63}, {63, 1}, {-1, 65}, {65, -1}}
	var a, b, got, want [256]byte
	for _, weight := range weights {
		for av := 0; av < 256; av++ {
			for bv := 0; bv < 256; bv++ {
				a[0], b[0], got[0], want[0] = byte(av), byte(bv), 0xa5, 0xa5
				biBlendRectPixels(got[:], a[:], b[:], 4, 4, 1, weight[0], weight[1])
				blendReference(want[:], a[:], b[:], 4, 4, 1, weight[0], weight[1])
				if got[0] != want[0] {
					t.Fatalf("a=%d b=%d weights=%v got=%d want=%d", av, bv, weight, got[0], want[0])
				}
			}
		}
	}
}

func TestBiBlendExplicitDenominatorOffsetBoundaries(t *testing.T) {
	params := []struct {
		w0, w1, denom, offset int
	}{
		{-128, 127, 0, -128}, {127, -128, 0, 127},
		{-128, -128, 7, -128}, {127, 127, 7, 127},
		{1, 1, 0, 0}, {64, 64, 5, -1}, {-1, 65, 3, 64},
	}
	var a, b, got, want [256]byte
	for _, p := range params {
		shift, round := p.denom+1, 1<<p.denom
		for av := 0; av < 256; av++ {
			for bv := 0; bv < 256; bv++ {
				a[0], b[0], got[0], want[0] = byte(av), byte(bv), 0xa5, 0xa5
				biBlendRectParams(got[:], a[:], b[:], 4, 4, 1, p.w0, p.w1, round, shift, p.offset)
				blendReferenceParams(want[:], a[:], b[:], 4, 4, 1, p.w0, p.w1, round, shift, p.offset, false)
				if got[0] != want[0] {
					t.Fatalf("a=%d b=%d params=%+v got=%d want=%d", av, bv, p, got[0], want[0])
				}
			}
		}
	}
}

func TestBiBlendShapesStridesSentinelsAndInPlace(t *testing.T) {
	rng := rand.New(rand.NewSource(5264))
	weights := [][2]int{{32, 32}, {-64, 128}, {128, -64}, {17, 47}, {80, -16}}
	for _, stride := range []int{4, 8, 16, 23} {
		for _, shape := range []struct{ w, h int }{{4, 4}, {8, 4}, {8, 8}, {16, 8}, {8, 16}, {16, 16}} {
			if shape.w > stride {
				continue
			}
			n := (shape.h-1)*stride + shape.w
			a, b := make([]byte, n+17), make([]byte, n+17)
			for i := range a {
				a[i], b[i] = byte(rng.Intn(256)), byte(rng.Intn(256))
			}
			for _, weight := range weights {
				want := bytes.Repeat([]byte{0xa5}, n+17)
				got := append([]byte(nil), want...)
				blendReference(want, a, b, stride, shape.w, shape.h, weight[0], weight[1])
				biBlendRectPixels(got, a, b, stride, shape.w, shape.h, weight[0], weight[1])
				if !bytes.Equal(got, want) {
					t.Fatalf("stride=%d shape=%+v weights=%v", stride, shape, weight)
				}
				inPlaceWant := append([]byte(nil), a...)
				inPlaceGot := append([]byte(nil), a...)
				blendReference(inPlaceWant, inPlaceWant, b, stride, shape.w, shape.h, weight[0], weight[1])
				biBlendRectPixels(inPlaceGot, inPlaceGot, b, stride, shape.w, shape.h, weight[0], weight[1])
				if !bytes.Equal(inPlaceGot, inPlaceWant) {
					t.Fatalf("in-place stride=%d shape=%+v weights=%v", stride, shape, weight)
				}
			}
		}
	}
}

func TestBiBlendPartialAliasPreservesScalarOrder(t *testing.T) {
	for _, weight := range [][2]int{{32, 32}, {-64, 128}, {80, -16}} {
		baseWant := make([]byte, 512)
		for i := range baseWant {
			baseWant[i] = byte(i*73 + 19)
		}
		baseGot := append([]byte(nil), baseWant...)
		blendReference(baseWant[3:], baseWant[1:], baseWant[5:], 16, 8, 8, weight[0], weight[1])
		biBlendRectPixels(baseGot[3:], baseGot[1:], baseGot[5:], 16, 8, 8, weight[0], weight[1])
		if !bytes.Equal(baseGot, baseWant) {
			t.Fatalf("weights=%v", weight)
		}
	}
}

func TestBiBlendOutOfContractParametersUseScalar(t *testing.T) {
	var a, b [256]byte
	for i := range a {
		a[i], b[i] = byte(i), byte(255-i)
	}
	for _, p := range []struct{ w0, w1, round, shift, offset int }{
		{32767, -32768, 1024, 12, 4096},
		{-129, 128, 32, 6, 0},
		{127, 127, 256, 8, -129},
	} {
		got, want := bytes.Repeat([]byte{0xa5}, 256), bytes.Repeat([]byte{0xa5}, 256)
		biBlendRectParams(got, a[:], b[:], 16, 16, 16, p.w0, p.w1, p.round, p.shift, p.offset)
		blendReferenceParams(want, a[:], b[:], 16, 16, 16, p.w0, p.w1, p.round, p.shift, p.offset, false)
		if !bytes.Equal(got, want) {
			t.Fatalf("params=%+v", p)
		}
	}
}

func TestBiBlendInvalidInputsUnchanged(t *testing.T) {
	for _, tc := range []struct{ stride, w, h int }{{3, 4, 1}, {4, 0, 1}, {4, 4, 0}, {16, 16, 2}} {
		dst := bytes.Repeat([]byte{0xa5}, 16)
		biBlendRectPixels(dst, make([]byte, 16), make([]byte, 16), tc.stride, tc.w, tc.h, 32, 32)
		if !bytes.Equal(dst, bytes.Repeat([]byte{0xa5}, 16)) {
			t.Fatal(tc)
		}
	}
}

func TestBiBlendZeroAlloc(t *testing.T) {
	var dst, a, b [256]byte
	if n := testing.AllocsPerRun(1000, func() { biBlendRectPixels(dst[:], a[:], b[:], 16, 16, 16, -64, 128) }); n != 0 {
		t.Fatal(n)
	}
}

func BenchmarkBiBlend16x16(b *testing.B) {
	var dst, a, c [256]byte
	for i := range a {
		a[i], c[i] = byte(i), byte(255-i)
	}
	for _, tc := range []struct {
		name   string
		w0, w1 int
	}{{"average", 32, 32}, {"weighted", -16, 80}} {
		b.Run("scalar-"+tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				biBlendRectScalar(dst[:], a[:], c[:], 16, 16, 16, tc.w0, tc.w1)
			}
		})
		b.Run("dispatch-"+tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				biBlendRectPixels(dst[:], a[:], c[:], 16, 16, 16, tc.w0, tc.w1)
			}
		})
	}
}
