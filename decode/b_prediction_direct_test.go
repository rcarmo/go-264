package decode

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
)

func fillBPredBlockBuffered(dst []byte, ref *frame.Frame, srcBaseX, srcBaseY, dstX, dstY, w, h int, mv syntax.MotionVector) bool {
	refH := frameLumaHeight(ref)
	if ref == nil || ref.Width <= 0 || refH <= 0 || ref.StrideY <= 0 || ref.Width > ref.StrideY || len(dst) < 256 || !valid16x16Rect(dstX, dstY, w, h) {
		return false
	}
	lastPixel := (refH-1)*ref.StrideY + ref.Width - 1
	if lastPixel < 0 || lastPixel >= len(ref.Y) {
		return false
	}
	var tmp [256]byte
	pred.InterPredLumaH264(tmp[:], 16, ref.Y, ref.StrideY, srcBaseX, srcBaseY, w, h, pred.MotionVector{X: mv.X, Y: mv.Y})
	for y := 0; y < h; y++ {
		copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], tmp[y*16:y*16+w])
	}
	return true
}

func TestFillBPredBlockDirectMatchesBuffered(t *testing.T) {
	rng := rand.New(rand.NewSource(264))
	ref := frame.NewFrame(48, 48)
	for i := range ref.Y {
		ref.Y[i] = byte(rng.Intn(256))
	}
	shapes := []struct{ x, y, w, h int }{
		{0, 0, 16, 16}, {0, 0, 16, 8}, {0, 8, 16, 8},
		{0, 0, 8, 16}, {8, 0, 8, 16}, {0, 0, 8, 8},
		{8, 8, 8, 8}, {4, 8, 8, 4}, {12, 12, 4, 4},
	}
	bases := [][2]int{{0, 0}, {7, 9}, {32, 32}, {47, 47}}
	for _, shape := range shapes {
		for _, base := range bases {
			for fy := -4; fy < 4; fy++ {
				for fx := -4; fx < 4; fx++ {
					want := bytes.Repeat([]byte{0xa5}, 256)
					got := append([]byte(nil), want...)
					mv := syntax.MotionVector{X: int16(fx), Y: int16(fy)}
					if !fillBPredBlockBuffered(want, ref, base[0], base[1], shape.x, shape.y, shape.w, shape.h, mv) {
						t.Fatal("reference rejected valid case")
					}
					if !fillBPredBlock(got, ref, base[0], base[1], shape.x, shape.y, shape.w, shape.h, mv) {
						t.Fatal("direct path rejected valid case")
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("shape=%+v base=%v mv=%+v", shape, base, mv)
					}
				}
			}
		}
	}
}

func TestFillBPredBlockOverlapMatchesBuffered(t *testing.T) {
	for _, mv := range []syntax.MotionVector{{}, {X: 1}, {Y: 2}, {X: 3, Y: 3}} {
		original := make([]byte, 32*32)
		for i := range original {
			original[i] = byte(i*73 + 19)
		}
		want := append([]byte(nil), original...)
		got := append([]byte(nil), original...)
		wantRef := &frame.Frame{Width: 32, Height: 32, StrideY: 32, Y: want}
		gotRef := &frame.Frame{Width: 32, Height: 32, StrideY: 32, Y: got}
		fillBPredBlockBuffered(want[:256], wantRef, 0, 0, 4, 3, 8, 8, mv)
		fillBPredBlock(got[:256], gotRef, 0, 0, 4, 3, 8, 8, mv)
		if !bytes.Equal(got, want) {
			t.Fatalf("overlap mv=%+v", mv)
		}
	}
}

func TestFillBPredBlockInvalidLeavesDestination(t *testing.T) {
	ref := frame.NewFrame(16, 16)
	for _, tc := range []struct{ x, y, w, h int }{
		{-1, 0, 8, 8}, {0, -1, 8, 8}, {0, 0, 0, 8},
		{0, 0, 17, 1}, {15, 15, 2, 2},
	} {
		dst := bytes.Repeat([]byte{0xa5}, 256)
		if fillBPredBlock(dst, ref, 0, 0, tc.x, tc.y, tc.w, tc.h, syntax.MotionVector{}) {
			t.Fatalf("accepted malformed rect %+v", tc)
		}
		if !bytes.Equal(dst, bytes.Repeat([]byte{0xa5}, 256)) {
			t.Fatalf("modified malformed rect %+v", tc)
		}
	}
}

func BenchmarkFillBPredBlock(b *testing.B) {
	ref := frame.NewFrame(48, 48)
	for i := range ref.Y {
		ref.Y[i] = byte(i*73 + 19)
	}
	var dst [256]byte
	mv := syntax.MotionVector{X: 1, Y: 3}
	b.Run("buffered-8x8", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fillBPredBlockBuffered(dst[:], ref, 8, 8, 4, 4, 8, 8, mv)
		}
	})
	b.Run("direct-8x8", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fillBPredBlock(dst[:], ref, 8, 8, 4, 4, 8, 8, mv)
		}
	})
	b.Run("buffered-16x16", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fillBPredBlockBuffered(dst[:], ref, 8, 8, 0, 0, 16, 16, mv)
		}
	})
	b.Run("direct-16x16", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fillBPredBlock(dst[:], ref, 8, 8, 0, 0, 16, 16, mv)
		}
	})
}
