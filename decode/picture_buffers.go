package decode

import "github.com/rcarmo/go-264/frame"

// pictureBufferPool is enabled only by StreamDecoder, which copies each output
// before handing it to the consumer. Batch Decoder outputs share coded storage
// and therefore must never use this pool. Each entry owns one coded picture's
// pixels and saved motion; reference marking may create other Frame headers
// sharing those arrays.
type pictureBufferPool struct {
	frames []*frame.Frame
}

// pictureBufferInUse includes pictures already output but still referenced, and
// pictures no longer referenced but still waiting for output. Compare the coded
// backing store because MMCO marking copies metadata, not pixels or motion.
func pictureBufferInUse(f *frame.Frame, refs, pending []*frame.Frame) bool {
	for _, ref := range refs {
		if sameOutputPicture(f, ref) {
			return true
		}
	}
	for _, out := range pending {
		if sameOutputPicture(f, out) {
			return true
		}
	}
	return false
}

// prune retains the live reference/output union and at most one free picture
// of the current geometry. Active old-geometry pictures survive until an IDR
// has actually flushed or discarded them; unused peak-sized buffers do not.
func (p *pictureBufferPool) prune(mbW, mbH int, refs, pending []*frame.Frame) {
	n, free := 0, false
	for _, f := range p.frames {
		keep := pictureBufferInUse(f, refs, pending)
		if !keep && !free && f.Width == mbW*16 && f.Height == mbH*16 {
			keep, free = true, true
		}
		if keep {
			p.frames[n] = f
			n++
		}
	}
	clear(p.frames[n:])
	p.frames = p.frames[:n]
}

// take runs only before starting a picture, after the preceding publication
// and its synchronous callback have returned. Nothing may read a free picture
// again, so resetting its buffers cannot alter prediction or delayed output.
func (p *pictureBufferPool) take(mbW, mbH int, refs, pending []*frame.Frame) *frame.Frame {
	p.prune(mbW, mbH, refs, pending)
	for _, f := range p.frames {
		if !pictureBufferInUse(f, refs, pending) {
			resetPictureFrame(f)
			return f
		}
	}
	f := newPictureFrame(mbW, mbH)
	p.frames = append(p.frames, f)
	return f
}

// newPictureFrame allocates coded pixels and permanent motion metadata. Slice
// scratch is separate: these arrays must survive while the picture is a reference.
func newPictureFrame(mbW, mbH int) *frame.Frame {
	width, height := mbW*16, mbH*16
	f := &frame.Frame{Width: width, Height: height, StrideY: width, StrideC: width / 2}
	f.Y, f.U, f.V = makePicturePlanes(width*height, width*height/4)
	for i := range f.U {
		f.U[i], f.V[i] = 128, 128
	}
	n := mbW * mbH * 16
	f.MotionStride4 = mbW * 4
	motion := make([][2]int16, 2*n)
	f.MotionL0, f.MotionL1 = motion[:n:n], motion[n:2*n:2*n]
	refs := make([]int8, 3*n)
	f.RefIdxL0, f.TemporalRefIdxL0, f.RefIdxL1 = refs[:n:n], refs[n:2*n:2*n], refs[2*n:3*n:3*n]
	f.MBType = make([]uint32, mbW*mbH)
	return f
}

// makePicturePlanes groups the three byte planes into one allocation, avoiding
// separate allocator rounding for each chroma plane. Limit every slice's capacity
// so a consumer appending to one plane cannot overwrite its neighbour.
func makePicturePlanes(luma, chroma int) (y, u, v []byte) {
	buf := make([]byte, luma+2*chroma)
	return buf[:luma:luma], buf[luma : luma+chroma : luma+chroma], buf[luma+chroma : luma+2*chroma : luma+2*chroma]
}

// resetPictureFrame restores the same initial values as a fresh coded frame.
// Reset the entire metadata header so crop, marking, POC and reference-list
// information cannot leak into the next picture; retain only fixed-size buffers.
func resetPictureFrame(f *frame.Frame) {
	*f = frame.Frame{
		Width: f.Width, Height: f.Height, StrideY: f.StrideY, StrideC: f.StrideC,
		Y: f.Y, U: f.U, V: f.V, MotionStride4: f.MotionStride4,
		MotionL0: f.MotionL0, MotionL1: f.MotionL1,
		RefIdxL0: f.RefIdxL0, RefIdxL1: f.RefIdxL1,
		TemporalRefIdxL0: f.TemporalRefIdxL0, MBType: f.MBType,
	}
	clear(f.Y)
	for i := range f.U {
		f.U[i], f.V[i] = 128, 128
	}
	clear(f.MotionL0)
	clear(f.MotionL1)
	clear(f.RefIdxL0)
	clear(f.RefIdxL1)
	clear(f.TemporalRefIdxL0)
	clear(f.MBType)
}
