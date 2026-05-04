package frame

// YUV frame and plane management for H.264 decoder.

// Frame represents a decoded YUV 4:2:0 picture.
type Frame struct {
	Width, Height int
	Y             []uint8 // Luma plane (Width × Height)
	U             []uint8 // Chroma U plane (Width/2 × Height/2)
	V             []uint8 // Chroma V plane (Width/2 × Height/2)
	StrideY       int     // Y plane stride (may be > Width for alignment)
	StrideC       int     // Chroma stride
	POC           int     // Picture order count
	FrameNum      int     // Frame number
	IsIDR         bool    // Is this an IDR frame?
	IsRef         bool    // Is this a reference frame?
}

// NewFrame allocates a YUV 4:2:0 frame.
func NewFrame(width, height int) *Frame {
	if width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		return &Frame{Width: width, Height: height}
	}
	strideY := (width + 15) &^ 15
	strideC := strideY / 2
	h := (height + 15) &^ 15

	f := &Frame{
		Width:   width,
		Height:  height,
		Y:       make([]uint8, strideY*h),
		U:       make([]uint8, strideC*(h/2)),
		V:       make([]uint8, strideC*(h/2)),
		StrideY: strideY,
		StrideC: strideC,
	}
	// Neutral chroma for partially implemented chroma reconstruction and skipped
	// chroma blocks. H.264 4:2:0 YUV defaults should be grey (U=V=128), not
	// green/purple from zero-filled chroma planes.
	for i := range f.U {
		f.U[i] = 128
	}
	for i := range f.V {
		f.V[i] = 128
	}
	return f
}

// PixelY returns luma pixel at (x, y).
func (f *Frame) PixelY(x, y int) uint8 {
	return f.Y[y*f.StrideY+x]
}

// SetPixelY sets luma pixel at (x, y).
func (f *Frame) SetPixelY(x, y int, v uint8) {
	f.Y[y*f.StrideY+x] = v
}

func (f *Frame) PixelU(x, y int) uint8 { return f.U[y*f.StrideC+x] }
func (f *Frame) PixelV(x, y int) uint8 { return f.V[y*f.StrideC+x] }
func (f *Frame) SetPixelU(x, y int, v uint8) { f.U[y*f.StrideC+x] = v }
func (f *Frame) SetPixelV(x, y int, v uint8) { f.V[y*f.StrideC+x] = v }

// Block4x4Y extracts a 4×4 luma block at macroblock-relative position.
func (f *Frame) Block4x4Y(mbX, mbY, blkIdx int) []uint8 {
	// blkIdx: 0-15 within macroblock (raster scan of 4×4 blocks)
	bx := (blkIdx % 4) * 4
	by := (blkIdx / 4) * 4
	x := mbX*16 + bx
	y := mbY*16 + by
	block := make([]uint8, 16)
	for row := 0; row < 4; row++ {
		copy(block[row*4:], f.Y[(y+row)*f.StrideY+x:])
	}
	return block
}

// WriteBlock4x4Y writes a 4×4 luma block to the frame.
func (f *Frame) WriteBlock4x4Y(mbX, mbY, blkIdx int, block []uint8) {
	bx := (blkIdx % 4) * 4
	by := (blkIdx / 4) * 4
	x := mbX*16 + bx
	y := mbY*16 + by
	for row := 0; row < 4; row++ {
		copy(f.Y[(y+row)*f.StrideY+x:], block[row*4:(row+1)*4])
	}
}

// DPB (Decoded Picture Buffer) manages reference frames.
type DPB struct {
	Frames  []*Frame
	MaxSize int
}

// NewDPB creates a decoded picture buffer.
func NewDPB(maxSize int) *DPB {
	return &DPB{MaxSize: maxSize}
}

// Add adds a decoded frame to the buffer.
func (d *DPB) Add(f *Frame) {
	d.Frames = append(d.Frames, f)
	if len(d.Frames) > d.MaxSize {
		// Remove oldest non-reference frame
		for i, frame := range d.Frames {
			if !frame.IsRef {
				d.Frames = append(d.Frames[:i], d.Frames[i+1:]...)
				break
			}
		}
	}
}

// GetRef returns a reference frame by frame number.
func (d *DPB) GetRef(frameNum int) *Frame {
	for _, f := range d.Frames {
		if f.FrameNum == frameNum && f.IsRef {
			return f
		}
	}
	return nil
}

// Flush clears all frames from the buffer.
func (d *DPB) Flush() {
	d.Frames = d.Frames[:0]
}
