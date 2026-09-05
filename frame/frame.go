package frame

import (
	"fmt"
	"image"
)

// YUV frame and plane management for H.264 decoder.

// Frame represents a decoded YUV 4:2:0 picture.
type Frame struct {
	Width, Height int
	// CropRect is the visible luma rectangle within a coded picture. A zero
	// rectangle means the whole picture. Decoder output views have this cleared:
	// their dimensions and plane origins already describe the visible image.
	CropRect         image.Rectangle
	Y                []uint8    // Luma plane (Width × Height)
	U                []uint8    // Chroma U plane (Width/2 × Height/2)
	V                []uint8    // Chroma V plane (Width/2 × Height/2)
	StrideY          int        // Y plane stride (may be > Width for alignment)
	StrideC          int        // Chroma stride
	POC              int        // Picture order count: min(TopFieldOrderCnt, BottomFieldOrderCnt)
	FullPOC          int        // Same derived picture order count; retained for API compatibility
	FrameNum         int        // Frame number
	IsIDR            bool       // Is this an IDR frame?
	IsRef            bool       // Is this a reference frame?
	NonExisting      bool       // frame_num gap placeholder; has no decoded samples
	MotionStride4    int        // width of 4x4 motion/ref caches
	MotionL0         [][2]int16 // decoded list0 4x4 motion cache for B-direct colocated checks
	RefIdxL0         []int8     // original slice-local list0 indices for spatial-direct colocated checks
	TemporalRefIdxL0 []int8     // picture-wide indices into RefListL0POC/Num, matching MotionL0
	MotionL1         [][2]int16 // decoded list1 4x4 motion cache for colocated direct fallback
	RefIdxL1         []int8     // decoded list1 4x4 ref cache matching MotionL1
	MBType           []uint32   // FFmpeg-style per-MB shape/use flags for colocated direct derivation
	RefListL0POC     []int      // picture-wide union of unwrapped L0 reference POCs
	RefListL0Num     []int      // ordered L0 reference frame_num values matching RefListL0POC
}

// OutputView returns a zero-copy, visible-sized view without changing the coded
// picture used for reconstruction and reference prediction. Plane strides are
// preserved; the caller must still use Width/Height when exporting rows. The
// view shares pixel storage with f and must not be used as a reference picture.
func (f *Frame) OutputView() (*Frame, error) {
	if f == nil {
		return nil, fmt.Errorf("nil frame")
	}
	r := f.CropRect
	if r == (image.Rectangle{}) {
		return f, nil
	}
	if r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > f.Width || r.Max.Y > f.Height || r.Empty() ||
		(r.Min.X|r.Min.Y|r.Max.X|r.Max.Y)&1 != 0 {
		return nil, fmt.Errorf("invalid 4:2:0 crop %v for %dx%d picture", r, f.Width, f.Height)
	}
	view := *f
	view.Width, view.Height = r.Dx(), r.Dy()
	view.CropRect = image.Rectangle{}
	var ok bool
	if view.Y, ok = cropPlane(f.Y, f.StrideY, r.Min.X, r.Min.Y, view.Width, view.Height); !ok {
		return nil, fmt.Errorf("cropped luma outside plane")
	}
	if view.U, ok = cropPlane(f.U, f.StrideC, r.Min.X/2, r.Min.Y/2, view.Width/2, view.Height/2); !ok {
		return nil, fmt.Errorf("cropped chroma U outside plane")
	}
	if view.V, ok = cropPlane(f.V, f.StrideC, r.Min.X/2, r.Min.Y/2, view.Width/2, view.Height/2); !ok {
		return nil, fmt.Errorf("cropped chroma V outside plane")
	}
	return &view, nil
}

func cropPlane(p []uint8, stride, x, y, width, height int) ([]uint8, bool) {
	// Check before multiplying, including for malformed manually built Frames.
	if stride <= 0 || x < 0 || y < 0 || width <= 0 || height <= 0 || x > stride || width > stride-x || y > len(p)/stride {
		return nil, false
	}
	start := y*stride + x
	if start > len(p) || width > len(p)-start || height-1 > (len(p)-start-width)/stride {
		return nil, false
	}
	return p[start:], true
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

// SafePixelY returns luma pixel at (x, y), clamping out-of-bounds coordinates
// to the frame edges instead of panicking. Use in prediction reference reads
// where the caller cannot guarantee bounds; the hot path keeps PixelY.
func (f *Frame) SafePixelY(x, y int) uint8 {
	if f == nil || f.Width <= 0 || f.Height <= 0 || f.StrideY <= 0 || f.Width > f.StrideY {
		return 0
	}
	last := (f.Height-1)*f.StrideY + (f.Width - 1)
	if last < 0 || last >= len(f.Y) {
		return 0
	}
	if x < 0 {
		x = 0
	} else if x >= f.Width {
		x = f.Width - 1
	}
	if y < 0 {
		y = 0
	} else if y >= f.Height {
		y = f.Height - 1
	}
	return f.Y[y*f.StrideY+x]
}

// SetPixelY sets luma pixel at (x, y).
func (f *Frame) SetPixelY(x, y int, v uint8) {
	f.Y[y*f.StrideY+x] = v
}

func (f *Frame) PixelU(x, y int) uint8       { return f.U[y*f.StrideC+x] }
func (f *Frame) PixelV(x, y int) uint8       { return f.V[y*f.StrideC+x] }
func (f *Frame) SetPixelU(x, y int, v uint8) { f.U[y*f.StrideC+x] = v }
func (f *Frame) SetPixelV(x, y int, v uint8) { f.V[y*f.StrideC+x] = v }

// Block4x4Y extracts a 4×4 luma block at macroblock-relative position.
func (f *Frame) Block4x4Y(mbX, mbY, blkIdx int) []uint8 {
	block := make([]uint8, 16)
	// blkIdx: 0-15 within macroblock (raster scan of 4×4 blocks)
	bx := (blkIdx % 4) * 4
	by := (blkIdx / 4) * 4
	x := mbX*16 + bx
	y := mbY*16 + by
	if f == nil || blkIdx < 0 || blkIdx >= 16 || !f.coversYRect(x, y, 4, 4) {
		return block
	}
	for row := 0; row < 4; row++ {
		copy(block[row*4:], f.Y[(y+row)*f.StrideY+x:(y+row)*f.StrideY+x+4])
	}
	return block
}

// WriteBlock4x4Y writes a 4×4 luma block to the frame.
func (f *Frame) WriteBlock4x4Y(mbX, mbY, blkIdx int, block []uint8) {
	bx := (blkIdx % 4) * 4
	by := (blkIdx / 4) * 4
	x := mbX*16 + bx
	y := mbY*16 + by
	if f == nil || blkIdx < 0 || blkIdx >= 16 || len(block) < 16 || !f.coversYRect(x, y, 4, 4) {
		return
	}
	for row := 0; row < 4; row++ {
		copy(f.Y[(y+row)*f.StrideY+x:(y+row)*f.StrideY+x+4], block[row*4:(row+1)*4])
	}
}

func (f *Frame) coversYRect(x, y, w, h int) bool {
	if f == nil || w <= 0 || h <= 0 || x < 0 || y < 0 || f.Width <= 0 || f.Height <= 0 || f.StrideY <= 0 || f.Width > f.StrideY {
		return false
	}
	if x+w > f.Width || y+h > f.Height {
		return false
	}
	last := (y+h-1)*f.StrideY + x + w
	return last >= 0 && last <= len(f.Y)
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
	for d.MaxSize > 0 && len(d.Frames) > d.MaxSize {
		// Non-reference pictures are never prediction candidates. Prefer evicting
		// them; if the buffer contains only references, apply the H.264 sliding
		// window rule and discard the oldest short-term reference.
		remove := -1
		for i, candidate := range d.Frames {
			if !candidate.IsRef {
				remove = i
				break
			}
		}
		if remove < 0 {
			remove = 0
		}
		d.Frames = append(d.Frames[:remove], d.Frames[remove+1:]...)
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

// Interface helpers so *Frame satisfies decode.Frame without import cycle.
// These thin wrappers expose metadata fields as method calls.
func (f *Frame) GetWidth() int    { return f.Width }
func (f *Frame) GetHeight() int   { return f.Height }
func (f *Frame) GetPOC() int      { return f.POC }
func (f *Frame) GetFrameNum() int { return f.FrameNum }
func (f *Frame) IsIDRFrame() bool { return f.IsIDR }
func (f *Frame) IsRefFrame() bool { return f.IsRef }
