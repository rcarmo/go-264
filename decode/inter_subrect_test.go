package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
)

func referenceInterSubRect(dst []uint8, ref *frame.Frame, srcBaseX, srcBaseY, dstX, dstY, w, h int, mv syntax.MotionVector) {
	var tmp [256]uint8
	pred.InterPred16x16At(tmp[:], ref.Y, ref.StrideY, srcBaseX, srcBaseY, pred.MotionVector{X: mv.X, Y: mv.Y})
	for y := 0; y < h; y++ {
		copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], tmp[y*16:y*16+w])
	}
}

func TestCopyInterSubRectIntegerMatchesReference(t *testing.T) {
	ref := frame.NewFrame(32, 32)
	for y := 0; y < ref.Height; y++ {
		for x := 0; x < ref.Width; x++ {
			ref.Y[y*ref.StrideY+x] = uint8((x*7 + y*11) & 0xff)
		}
	}
	cases := []struct {
		name                           string
		srcBaseX, srcBaseY, dstX, dstY int
		w, h                           int
		mv                             syntax.MotionVector
	}{
		{"interior8x8", 8, 9, 0, 0, 8, 8, syntax.MotionVector{X: 4, Y: 8}},
		{"interior4x4", 10, 11, 4, 4, 4, 4, syntax.MotionVector{X: -4, Y: 4}},
		{"clipped", -2, -1, 8, 8, 8, 4, syntax.MotionVector{X: 0, Y: 0}},
	}
	var d Decoder
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, want [256]uint8
			d.copyInterSubRect(got[:], ref, tc.srcBaseX, tc.srcBaseY, tc.dstX, tc.dstY, tc.w, tc.h, tc.mv)
			referenceInterSubRect(want[:], ref, tc.srcBaseX, tc.srcBaseY, tc.dstX, tc.dstY, tc.w, tc.h, tc.mv)
			if got != want {
				t.Fatalf("subrect mismatch")
			}
		})
	}
}

func TestCopyInterSubRectMalformedInputsDoNotPanic(t *testing.T) {
	var d Decoder
	var dst [256]uint8
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("copyInterSubRect panicked on malformed input: %v", r)
		}
	}()
	d.copyInterSubRect(dst[:], nil, 0, 0, 0, 0, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:4], frame.NewFrame(16, 16), 0, 0, 0, 0, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:], &frame.Frame{Width: 0, Height: 1, StrideY: 1, Y: []uint8{1}}, 0, 0, 0, 0, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:], &frame.Frame{Width: 1, Height: 1, StrideY: 16, Y: []uint8{1}}, 0, 0, 0, 0, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:], &frame.Frame{Width: 17, Height: 1, StrideY: 16, Y: make([]uint8, 16)}, 0, 0, 0, 0, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:], &frame.Frame{Width: 16, Height: 2, StrideY: 16, Y: make([]uint8, 16)}, 0, 0, 0, 0, 8, 8, syntax.MotionVector{})
	ref := frame.NewFrame(16, 16)
	d.copyInterSubRect(dst[:], ref, 0, 0, -1, 0, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:], ref, 0, 0, 0, -1, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:], ref, 0, 0, 12, 0, 8, 8, syntax.MotionVector{})
	d.copyInterSubRect(dst[:], ref, 0, 0, 0, 12, 8, 8, syntax.MotionVector{})
}

func TestCopyInterSubRectFractionalStillMatchesReference(t *testing.T) {
	ref := frame.NewFrame(32, 32)
	for y := 0; y < ref.Height; y++ {
		for x := 0; x < ref.Width; x++ {
			ref.Y[y*ref.StrideY+x] = uint8((x*13 + y*5) & 0xff)
		}
	}
	var got, want [256]uint8
	var d Decoder
	mv := syntax.MotionVector{X: 1, Y: 2}
	d.copyInterSubRect(got[:], ref, 8, 9, 4, 4, 8, 8, mv)
	referenceInterSubRect(want[:], ref, 8, 9, 4, 4, 8, 8, mv)
	if got != want {
		t.Fatal("fractional subrect mismatch")
	}
}
