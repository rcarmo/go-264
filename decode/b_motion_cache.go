package decode

import (
	"github.com/rcarmo/go-264/entropy/cabac"
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

// bMotionCache is the B-slice motion/ref cache layer. It deliberately mirrors
// FFmpeg's split list0/list1 cache model while still storing values in the
// decoder's flat 4x4 arrays. Keeping this as a named object gives the B-direct
// port a single place to grow FFmpeg-compatible cache semantics instead of
// spreading scan/cache rules through pipeline.go.
type bMotionCache struct {
	stride4 int
	mv      [2][]syntax.MotionVector
	mvd     [2][]syntax.MotionVector
	ref     [2][]int8
}

func newBMotionCache(stride4, mbHeight int) bMotionCache {
	n := stride4 * mbHeight * 4
	c := bMotionCache{
		stride4: stride4,
		mv:      [2][]syntax.MotionVector{make([]syntax.MotionVector, n), make([]syntax.MotionVector, n)},
		mvd:     [2][]syntax.MotionVector{make([]syntax.MotionVector, n), make([]syntax.MotionVector, n)},
		ref:     [2][]int8{make([]int8, n), make([]int8, n)},
	}
	for list := 0; list < 2; list++ {
		for i := range c.ref[list] {
			c.ref[list][i] = -2
		}
	}
	return c
}

func (c bMotionCache) mv4(list int) []syntax.MotionVector {
	if list < 0 || list > 1 {
		return nil
	}
	return c.mv[list]
}

func (c bMotionCache) mvd4(list int) []syntax.MotionVector {
	if list < 0 || list > 1 {
		return nil
	}
	return c.mvd[list]
}

func (c bMotionCache) ref4(list int) []int8 {
	if list < 0 || list > 1 {
		return nil
	}
	return c.ref[list]
}

func (c bMotionCache) refIdxCtxs(mbX, mbY int) [4]int {
	return cabacRefIdxCtxsForMB(c.ref[0], c.stride4, mbX, mbY)
}

func (c bMotionCache) decodeCABACPInterMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx,
	numRefFrames uint32, lastQScaleDiff int,
	leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int,
	leftCBP, topCBP uint32,
	leftNonSkip, topNonSkip bool,
	mbX, mbY int,
	transform8x8Mode bool, transform8x8Ctx int,
	leftMBType, topMBType uint32,
	leftChromaPred, topChromaPred int8,
	leftEdge8x8, topEdge8x8 [2]int8,
) (*syntax.MBInter, *syntax.MBIntra, bool) {
	return decodeCABACPInterMB(dec, models, numRefFrames, lastQScaleDiff,
		leftNZ, topNZ, leftChromaNZ, topChromaNZ,
		leftCBP, topCBP, leftNonSkip, topNonSkip,
		c.refIdxCtxs(mbX, mbY), c.mvd[0], c.stride4, mbX, mbY,
		transform8x8Mode, transform8x8Ctx,
		leftMBType, topMBType,
		leftChromaPred, topChromaPred,
		leftEdge8x8, topEdge8x8)
}

func (c bMotionCache) decodeCABACBidiMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx,
	numRefL0, numRefL1 uint32, lastQScaleDiff int,
	leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int,
	leftCBP, topCBP uint32,
	leftNonSkip, topNonSkip bool,
	leftIsDirect, topIsDirect bool,
	mbX, mbY int,
	transform8x8Mode bool, transform8x8Ctx int,
	leftMBType, topMBType uint32,
	leftChromaPred, topChromaPred int8,
	leftEdge8x8, topEdge8x8 [2]int8,
) (*syntax.MBBidi, *syntax.MBIntra, bool) {
	return decodeCABACBidiMB(dec, models,
		numRefL0, numRefL1, lastQScaleDiff,
		leftNZ, topNZ, leftChromaNZ, topChromaNZ,
		leftCBP, topCBP,
		leftNonSkip, topNonSkip,
		leftIsDirect, topIsDirect,
		c.refIdxCtxs(mbX, mbY), c.mv[0], c.ref[0], c.mv[1], c.ref[1], c.mvd[0], c.mvd[1], c.stride4, mbX, mbY,
		transform8x8Mode, transform8x8Ctx,
		leftMBType, topMBType,
		leftChromaPred, topChromaPred,
		leftEdge8x8, topEdge8x8)
}

func (c bMotionCache) predictSkipL0(x4, y4 int) syntax.MotionVector {
	return predictSkipMV4x4(c.mv[0], c.ref[0], c.stride4, x4, y4)
}

func (c bMotionCache) directSpatialL0Ref(x4, y4 int) int8 {
	return predictBDirectSpatialL0Ref(c.mv[0], c.ref[0], c.stride4, x4, y4)
}

func (c bMotionCache) get(list, x4, y4 int) (syntax.MotionVector, int8) {
	return getMV4(c.mv4(list), c.ref4(list), c.stride4, x4, y4)
}

func (c bMotionCache) predictDirectSpatial(list, x4, y4 int) (int8, syntax.MotionVector) {
	return predictBDirectSpatialL0ForSimpleRefs(c.mv4(list), c.ref4(list), c.stride4, x4, y4)
}

func (c bMotionCache) initDirect16x16(mb *syntax.MBBidi, refL0 int8, mvL0 syntax.MotionVector, refL1 int8, mvL1 syntax.MotionVector) {
	if mb == nil || mb.MBType != syntax.BMBTypeDirect16x16 {
		return
	}
	mb.RefIdxL0[0] = refL0
	mb.RefIdxL1 = [4]int8{refL1, refL1, refL1, refL1}
	mb.MVL0[0] = mvL0
	mb.MVL1[0] = mvL1
}

func (c bMotionCache) applyDirect16x16Spatial(mbX, mbY int, mb *syntax.MBBidi, colocated *frame.Frame) {
	applyBDirect16x16SpatialSubMVs(mb, colocated, mbX, mbY)
}

func (c bMotionCache) applyDirect8x8Spatial(mbX, mbY int, mb *syntax.MBBidi, refL0 int8, mvL0 syntax.MotionVector, refL1 int8, mvL1 syntax.MotionVector, colocated *frame.Frame) {
	applyB8x8DirectSpatial(mb, refL0, mvL0, refL1, mvL1, colocated, mbX, mbY)
}

func (c bMotionCache) writeBackIntra(mbX, mbY int) {
	writeBackIntra4x4(c.ref[0], c.stride4, mbX, mbY)
	writeBackIntra4x4(c.ref[1], c.stride4, mbX, mbY)
}

func (c bMotionCache) applyInterMVPredictors(mb *syntax.MBInter, mbX, mbY int) {
	applyMVPredictors(mb, c.mv[0], c.ref[0], c.stride4, mbX, mbY)
}

func (c bMotionCache) writeBackInterL0(mbX, mbY int, mb *syntax.MBInter) {
	writeBackInter4x4(c.mv[0], c.ref[0], c.stride4, mbX, mbY, mb)
}

func (c bMotionCache) writeBackBidi(mbX, mbY int, mb *syntax.MBBidi) {
	writeBackBidiListContext(c.mv[0], c.ref[0], c.stride4, mbX, mbY, mb, 0)
	writeBackBidiListContext(c.mv[1], c.ref[1], c.stride4, mbX, mbY, mb, 1)
}

func (c bMotionCache) saveL0ToFrame(f *frame.Frame, mbFFTypes []uint32) {
	if f == nil {
		return
	}
	f.MotionStride4 = c.stride4
	f.MotionL0 = make([][2]int16, len(c.mv[0]))
	f.RefIdxL0 = append(f.RefIdxL0[:0], c.ref[0]...)
	f.MBType = append(f.MBType[:0], mbFFTypes...)
	for i, mv := range c.mv[0] {
		f.MotionL0[i] = [2]int16{mv.X, mv.Y}
	}
}
