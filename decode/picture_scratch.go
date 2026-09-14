package decode

import "github.com/rcarmo/go-264/filter"

// pictureScratch is reused only after a picture finishes or is abandoned. A
// pictureState borrows these arrays while its slices reconstruct and deblock;
// saveSlice copies the motion information needed by later pictures into Frame.
// Nothing here aliases a published Frame or retains its pixels or references.
//
// Resetting every cell, including cells untouched by an aborted picture, keeps
// reuse independent of the preceding slice types, coverage and frame geometry.
type pictureScratch struct {
	motion            bMotionCache
	deblock           []filter.MBDeblockInfo
	intraModes        []int8
	mbSliceID         []int
	mbIsIntra         []bool
	nzCtx             [][16]int
	chromaNZCtx       [][2][4]int
	cbpCtx            []uint32
	mbTypeCtx         []uint32
	nonSkipCtx        []bool
	transform8x8Ctx   []bool
	chromaPredModeCtx []int8
	mbQPCtx           []int
	intra8x8ModeCtx   []int8
	intra8x8RightCtx  []int8
	intra8x8BottomCtx []int8
	mbFFTypeCtx       []uint32
}

// resetPictureBuffer retains only an exact-size allocation. In particular, a
// smaller picture after a geometry or resource-budget change must not retain
// the previous picture's peak-capacity scratch buffers.
func resetPictureBuffer[T any](buf []T, size int) []T {
	if cap(buf) != size {
		return make([]T, size)
	}
	buf = buf[:size]
	clear(buf)
	return buf
}

// reset supplies fresh-picture contexts without allocating again at a stable
// resolution. Only newPicture calls it: later slices must keep picture-wide
// ownership, entropy-neighbor and filter metadata from earlier slices.
func (s *pictureScratch) reset(mbWidth, mbHeight int) {
	mbs := mbWidth * mbHeight
	blocks := mbs * 16
	s.deblock = resetPictureBuffer(s.deblock, mbs)
	s.intraModes = resetPictureBuffer(s.intraModes, blocks)
	s.mbSliceID = resetPictureBuffer(s.mbSliceID, mbs)
	s.mbIsIntra = resetPictureBuffer(s.mbIsIntra, mbs)
	s.nzCtx = resetPictureBuffer(s.nzCtx, mbs)
	s.chromaNZCtx = resetPictureBuffer(s.chromaNZCtx, mbs)
	s.cbpCtx = resetPictureBuffer(s.cbpCtx, mbs)
	s.mbTypeCtx = resetPictureBuffer(s.mbTypeCtx, mbs)
	s.nonSkipCtx = resetPictureBuffer(s.nonSkipCtx, mbs)
	s.transform8x8Ctx = resetPictureBuffer(s.transform8x8Ctx, mbs)
	s.chromaPredModeCtx = resetPictureBuffer(s.chromaPredModeCtx, mbs)
	s.mbQPCtx = resetPictureBuffer(s.mbQPCtx, mbs)
	s.intra8x8ModeCtx = resetPictureBuffer(s.intra8x8ModeCtx, mbs*4)
	s.intra8x8RightCtx = resetPictureBuffer(s.intra8x8RightCtx, mbs*4)
	s.intra8x8BottomCtx = resetPictureBuffer(s.intra8x8BottomCtx, mbs*4)
	s.mbFFTypeCtx = resetPictureBuffer(s.mbFFTypeCtx, mbs)

	// Equal MB counts can still have different aspect ratios, so update stride
	// even when every allocation is reused.
	s.motion.stride4 = mbWidth * 4
	s.motion.direct = resetPictureBuffer(s.motion.direct, blocks)
	for list := 0; list < 2; list++ {
		s.motion.mv[list] = resetPictureBuffer(s.motion.mv[list], blocks)
		s.motion.mvd[list] = resetPictureBuffer(s.motion.mvd[list], blocks)
		s.motion.ref[list] = resetPictureBuffer(s.motion.ref[list], blocks)
		for i := range s.motion.ref[list] {
			s.motion.ref[list][i] = -2 // spatial neighbor unavailable
		}
	}
	for i := range s.intraModes {
		s.intraModes[i] = 2 // default DC prediction mode
	}
	for i := range s.mbSliceID {
		s.mbSliceID[i] = -1 // no slice has claimed this macroblock
	}
	for i := range s.intra8x8ModeCtx {
		s.intra8x8ModeCtx[i] = -1
		s.intra8x8RightCtx[i], s.intra8x8BottomCtx[i] = 2, 2
	}
}
