package frame

import (
	"image"
	"testing"
)

func TestOutputViewPreservesCodedPicture(t *testing.T) {
	coded := NewFrame(48, 32)
	coded.FrameNum, coded.FullPOC, coded.IsRef = 7, 14, true
	for y := 0; y < coded.Height; y++ {
		for x := 0; x < coded.Width; x++ {
			coded.SetPixelY(x, y, uint8(x+3*y))
		}
	}
	for y := 0; y < coded.Height/2; y++ {
		for x := 0; x < coded.Width/2; x++ {
			coded.SetPixelU(x, y, uint8(20+x+2*y))
			coded.SetPixelV(x, y, uint8(140+x+2*y))
		}
	}
	coded.CropRect = image.Rect(2, 4, 44, 28)
	view, err := coded.OutputView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Width != 42 || view.Height != 24 || view.StrideY != 48 || view.StrideC != 24 {
		t.Fatalf("visible dimensions/strides: %dx%d, %d/%d", view.Width, view.Height, view.StrideY, view.StrideC)
	}
	if coded.Width != 48 || coded.Height != 32 || coded.CropRect != image.Rect(2, 4, 44, 28) {
		t.Fatal("output view changed coded geometry")
	}
	if view == coded || &view.Y[0] != &coded.Y[4*coded.StrideY+2] ||
		&view.U[0] != &coded.U[2*coded.StrideC+1] || &view.V[0] != &coded.V[2*coded.StrideC+1] {
		t.Fatal("output planes must share storage at the visible origin")
	}
	if view.FrameNum != 7 || view.FullPOC != 14 || !view.IsRef {
		t.Fatal("output view lost picture metadata")
	}
	for y := 0; y < view.Height; y++ {
		for x := 0; x < view.Width; x++ {
			if view.PixelY(x, y) != coded.PixelY(x+2, y+4) {
				t.Fatalf("luma origin mismatch at %d,%d", x, y)
			}
		}
	}
	if view.PixelU(20, 11) != coded.PixelU(21, 13) || view.PixelV(20, 11) != coded.PixelV(21, 13) {
		t.Fatal("chroma origin mismatch")
	}
	if view.SafePixelY(100, 100) != coded.PixelY(43, 27) || coded.SafePixelY(100, 100) != coded.PixelY(47, 31) {
		t.Fatal("visible and reference edge clamping must use different rectangles")
	}
	again, err := view.OutputView()
	if err != nil || again != view {
		t.Fatal("output view was cropped twice")
	}
}

func TestOutputViewRejectsInvalidCrop(t *testing.T) {
	for _, r := range []image.Rectangle{
		image.Rect(-2, 0, 16, 16), image.Rect(0, 0, 18, 16),
		image.Rect(1, 0, 16, 16), image.Rect(0, 0, 16, 15),
		{Min: image.Pt(4, 4), Max: image.Pt(4, 12)},
	} {
		f := NewFrame(16, 16)
		f.CropRect = r
		if _, err := f.OutputView(); err == nil {
			t.Errorf("accepted crop %v", r)
		}
	}
	for _, plane := range []string{"Y", "U", "V"} {
		f := NewFrame(16, 16)
		f.CropRect = image.Rect(2, 2, 16, 16)
		switch plane {
		case "Y":
			f.Y = f.Y[:len(f.Y)-1]
		case "U":
			f.U = f.U[:len(f.U)-1]
		case "V":
			f.V = f.V[:len(f.V)-1]
		}
		if _, err := f.OutputView(); err == nil {
			t.Errorf("accepted short %s plane", plane)
		}
	}
}

func TestNewFrame(t *testing.T) {
	f := NewFrame(320, 240)
	if f.Width != 320 || f.Height != 240 {
		t.Fatalf("size %dx%d want 320x240", f.Width, f.Height)
	}
	// Stride should be >= width and 16-aligned
	if f.StrideY < 320 || f.StrideY%16 != 0 {
		t.Fatalf("strideY=%d want >=320 and 16-aligned", f.StrideY)
	}
	if f.StrideC != f.StrideY/2 {
		t.Fatalf("strideC=%d want %d", f.StrideC, f.StrideY/2)
	}
	t.Logf("Frame 320x240: strideY=%d strideC=%d Y=%d U=%d V=%d bytes",
		f.StrideY, f.StrideC, len(f.Y), len(f.U), len(f.V))
}

func TestFramePixels(t *testing.T) {
	f := NewFrame(16, 16)
	f.SetPixelY(5, 3, 42)
	if v := f.PixelY(5, 3); v != 42 {
		t.Fatalf("pixel(5,3)=%d want 42", v)
	}
}

func TestSafePixelYHandlesMalformedFrames(t *testing.T) {
	var nilFrame *Frame
	if got := nilFrame.SafePixelY(0, 0); got != 0 {
		t.Fatalf("nil SafePixelY got %d want 0", got)
	}
	bad := &Frame{Width: 16, Height: 16, StrideY: 16, Y: make([]uint8, 1)}
	if got := bad.SafePixelY(99, 99); got != 0 {
		t.Fatalf("short-plane SafePixelY got %d want 0", got)
	}
}

func TestBlock4x4YHandlesMalformedInputs(t *testing.T) {
	var nilFrame *Frame
	if got := nilFrame.Block4x4Y(0, 0, 0); len(got) != 16 || got[0] != 0 {
		t.Fatalf("nil Block4x4Y got %v want zero block", got)
	}
	bad := &Frame{Width: 16, Height: 16, StrideY: 16, Y: make([]uint8, 1)}
	if got := bad.Block4x4Y(0, 0, 0); len(got) != 16 || got[0] != 0 {
		t.Fatalf("short-plane Block4x4Y got %v want zero block", got)
	}
	bad.WriteBlock4x4Y(0, 0, 0, []uint8{1})
	bad.WriteBlock4x4Y(0, 0, 16, make([]uint8, 16))
}

func TestBlock4x4(t *testing.T) {
	f := NewFrame(32, 32)
	// Fill MB (0,0) with known values
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			f.SetPixelY(x, y, uint8(y*16+x))
		}
	}
	// Extract block 0 (top-left 4x4)
	blk := f.Block4x4Y(0, 0, 0)
	if blk[0] != 0 || blk[3] != 3 || blk[4] != 16 {
		t.Fatalf("block4x4[0]: %v", blk[:8])
	}
}

func TestDPB(t *testing.T) {
	dpb := NewDPB(3)
	for i := 0; i < 5; i++ {
		f := NewFrame(16, 16)
		f.FrameNum = i
		f.IsRef = i%2 == 0
		dpb.Add(f)
	}
	if len(dpb.Frames) > 3 {
		t.Fatalf("DPB size %d want <= 3", len(dpb.Frames))
	}
	t.Logf("DPB has %d frames after adding 5", len(dpb.Frames))
}
