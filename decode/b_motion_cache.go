package decode

import (
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
	ref     [2][]int8
}

func newBMotionCache(stride4, mbHeight int) bMotionCache {
	n := stride4 * mbHeight * 4
	c := bMotionCache{
		stride4: stride4,
		mv:      [2][]syntax.MotionVector{make([]syntax.MotionVector, n), make([]syntax.MotionVector, n)},
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

func (c bMotionCache) ref4(list int) []int8 {
	if list < 0 || list > 1 {
		return nil
	}
	return c.ref[list]
}

func (c bMotionCache) predictDirectSpatial(list, x4, y4 int) (int8, syntax.MotionVector) {
	return predictBDirectSpatialL0ForSimpleRefs(c.mv4(list), c.ref4(list), c.stride4, x4, y4)
}

func (c bMotionCache) applyDirect16x16Spatial(mbX, mbY int, mb *syntax.MBBidi, colocated *frame.Frame) {
	applyBDirect16x16SpatialSubMVs(mb, colocated, mbX, mbY)
}

func (c bMotionCache) applyDirect8x8Spatial(mbX, mbY int, mb *syntax.MBBidi, refL0 int8, mvL0 syntax.MotionVector, refL1 int8, mvL1 syntax.MotionVector, colocated *frame.Frame) {
	applyB8x8DirectSpatial(mb, refL0, mvL0, refL1, mvL1, colocated, mbX, mbY)
}

func (c bMotionCache) writeBackBidi(mbX, mbY int, mb *syntax.MBBidi) {
	writeBackBidiListContext(c.mv[0], c.ref[0], c.stride4, mbX, mbY, mb, 0)
	writeBackBidiListContext(c.mv[1], c.ref[1], c.stride4, mbX, mbY, mb, 1)
}
