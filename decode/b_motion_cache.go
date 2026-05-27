package decode

import (
	"fmt"
	"os"

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
	direct  []bool
}

func newBMotionCache(stride4, mbHeight int) bMotionCache {
	n := stride4 * mbHeight * 4
	c := bMotionCache{
		stride4: stride4,
		mv:      [2][]syntax.MotionVector{make([]syntax.MotionVector, n), make([]syntax.MotionVector, n)},
		mvd:     [2][]syntax.MotionVector{make([]syntax.MotionVector, n), make([]syntax.MotionVector, n)},
		ref:     [2][]int8{make([]int8, n), make([]int8, n)},
		direct:  make([]bool, n),
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

func (c bMotionCache) refIdxCtxsB(mbX, mbY int) [4]int {
	return cabacBRefIdxCtxsForMB(c.ref[0], c.direct, c.stride4, mbX, mbY)
}

func (c bMotionCache) decodeCABACPInterMB(dec *cabac.CABACDecoder, models []cabac.CABACCtx,
	numRefFrames uint32, lastQScaleDiff int,
	leftNZ, topNZ *[16]int, leftChromaNZ, topChromaNZ *[2][4]int,
	leftCBP, topCBP uint32,
	leftNonSkip, topNonSkip bool,
	mbX, mbY int,
	currentPOC int,
	transform8x8Mode bool, transform8x8Ctx int,
	leftMBType, topMBType uint32,
	leftChromaPred, topChromaPred int8,
	leftEdge8x8, topEdge8x8 [2]int8,
) (*syntax.MBInter, *syntax.MBIntra, bool) {
	return decodeCABACPInterMB(dec, models, numRefFrames, lastQScaleDiff,
		leftNZ, topNZ, leftChromaNZ, topChromaNZ,
		leftCBP, topCBP, leftNonSkip, topNonSkip,
		c.refIdxCtxs(mbX, mbY), c.ref[0], c.mvd[0], c.stride4, mbX, mbY, currentPOC,
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
	currentPOC int,
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
		c.refIdxCtxsB(mbX, mbY), c.mv[0], c.ref[0], c.direct, c.mv[1], c.ref[1], c.mvd[0], c.mvd[1], c.stride4, mbX, mbY,
		currentPOC,
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

func (c bMotionCache) predictDirectSpatial(list, x4, y4, poc int) (int8, syntax.MotionVector) {
	mbX, mbY := -1, -1
	if c.stride4 > 0 {
		mbX, mbY = x4/4, y4/4
	}
	return predictBDirectSpatialL0ForSimpleRefsDiag(c.mv4(list), c.ref4(list), c.stride4, x4, y4, mbX, mbY, poc)
}

func (c bMotionCache) initDirect16x16(mb *syntax.MBBidi, refL0 int8, mvL0 syntax.MotionVector, refL1 int8, mvL1 syntax.MotionVector) {
	if mb == nil || mb.MBType != syntax.BMBTypeDirect16x16 {
		return
	}
	mb.RefIdxL0 = [4]int8{refL0, refL0, refL0, refL0}
	mb.RefIdxL1 = [4]int8{refL1, refL1, refL1, refL1}
	mb.MVL0[0] = mvL0
	mb.MVL1[0] = mvL1
}

func (c bMotionCache) applyDirectSpatial(mbX, mbY int, mb *syntax.MBBidi, refL0 int8, mvL0 syntax.MotionVector, refL1 int8, mvL1 syntax.MotionVector, colocated *frame.Frame) {
	if mb == nil {
		return
	}
	if mb.MBType == syntax.BMBTypeDirect16x16 {
		c.initDirect16x16(mb, refL0, mvL0, refL1, mvL1)
		applyBDirect16x16SpatialSubMVs(mb, colocated, mbX, mbY)
		return
	}
	applyB8x8DirectSpatial(mb, refL0, mvL0, refL1, mvL1, colocated, mbX, mbY)
}

func (c bMotionCache) fillDirectFlag(mbX, mbY, w4, h4 int, direct bool) {
	if c.stride4 <= 0 {
		return
	}
	x4, y4 := mbX*4, mbY*4
	for y := 0; y < h4; y++ {
		row := (y4+y)*c.stride4 + x4
		for x := 0; x < w4; x++ {
			idx := row + x
			if idx >= 0 && idx < len(c.direct) {
				c.direct[idx] = direct
			}
		}
	}
}

func (c bMotionCache) writeBackIntra(mbX, mbY int) {
	writeBackIntra4x4(c.ref[0], c.stride4, mbX, mbY)
	writeBackIntra4x4(c.ref[1], c.stride4, mbX, mbY)
	c.fillDirectFlag(mbX, mbY, 4, 4, false)
}

func (c bMotionCache) applyInterMVPredictors(mb *syntax.MBInter, mbX, mbY, poc int) {
	applyMVPredictorsDiag(mb, c.mv[0], c.ref[0], c.stride4, mbX, mbY, poc)
}

func (c bMotionCache) writeBackInterL0(mbX, mbY int, mb *syntax.MBInter) {
	writeBackInter4x4(c.mv[0], c.ref[0], c.stride4, mbX, mbY, mb)
	c.fillDirectFlag(mbX, mbY, 4, 4, false)
}

func (c bMotionCache) writeBackBidi(mbX, mbY, poc int, mb *syntax.MBBidi) {
	writeBackBidiListContext(c.mv[0], c.ref[0], c.stride4, mbX, mbY, mb, 0)
	writeBackBidiListContext(c.mv[1], c.ref[1], c.stride4, mbX, mbY, mb, 1)
	c.fillDirectFlag(mbX, mbY, 4, 4, mb != nil && mb.MBType == syntax.BMBTypeDirect16x16)
	if mb != nil && mb.MBType == syntax.BMBTypeB8x8 {
		for part, t := range mb.SubMBType {
			baseX4 := mbX*4 + (part&1)*2
			baseY4 := mbY*4 + (part>>1)*2
			for y := 0; y < 2; y++ {
				for x := 0; x < 2; x++ {
					idx := (baseY4+y)*c.stride4 + baseX4 + x
					if idx >= 0 && idx < len(c.direct) {
						c.direct[idx] = t == 0
					}
				}
			}
		}
	}
	c.traceBidiWriteBack(mbX, mbY, poc, mb)
}

func (c bMotionCache) traceBidiWriteBack(mbX, mbY, poc int, mb *syntax.MBBidi) {
	if os.Getenv("GO264_MOTION_WRITE_TRACE") == "" || mb == nil || c.stride4 <= 0 {
		return
	}
	mbAddr := 0
	if c.stride4 >= 4 {
		mbAddr = mbY*(c.stride4/4) + mbX
	}
	for part := 0; part < 4; part++ {
		x4 := mbX*4 + (part&1)*2
		y4 := mbY*4 + (part>>1)*2
		idx := y4*c.stride4 + x4
		if idx < 0 || idx >= len(c.mv[0]) || idx >= len(c.ref[0]) || idx >= len(c.mv[1]) || idx >= len(c.ref[1]) {
			continue
		}
		mv0, mv1 := c.mv[0][idx], c.mv[1][idx]
		sub0, sub1 := mb.SubMVL0[part*4], mb.SubMVL1[part*4]
		fmt.Fprintf(os.Stderr, "GOMOTWRITE mb=%04d x=%02d y=%02d poc=%d type=%d part=%d ref0=%d mv0={%d,%d} ref1=%d mv1={%d,%d} sub0={%d,%d} sub1={%d,%d}\n", mbAddr, mbX, mbY, poc, mb.MBType, part, c.ref[0][idx], mv0.X, mv0.Y, c.ref[1][idx], mv1.X, mv1.Y, sub0.X, sub0.Y, sub1.X, sub1.Y)
	}
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

func (c bMotionCache) applyDirectTemporal(mbX, mbY int, mb *syntax.MBBidi, colocated *frame.Frame, currentPOC int, l0Frames []*frame.Frame, colPOC int) {
	if mb == nil {
		return
	}
	applyTemporalDirect(mb, colocated, mbX, mbY, currentPOC, l0Frames, colPOC)
}
