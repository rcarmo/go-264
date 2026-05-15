package frame

import "testing"

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
