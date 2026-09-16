package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestSpatialDirectWithout8x8InferenceKeepsPerCellColZero(t *testing.T) {
	col := &frame.Frame{MotionStride4: 4, MotionL0: make([][2]int16, 16), RefIdxL0: make([]int8, 16), MotionL1: make([][2]int16, 16), RefIdxL1: make([]int8, 16), MBType: []uint32{ffMBType16x16}}
	for i := range col.RefIdxL0 {
		col.RefIdxL0[i] = 0
		col.RefIdxL1[i] = -1
		col.MotionL0[i] = [2]int16{2, 0}
	}
	col.MotionL0[0], col.MotionL0[4] = [2]int16{}, [2]int16{}
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeB8x8, Direct8x8InferenceSet: true}
	mb.SubMBType = [4]uint32{0, 1, 1, 1}
	applyB8x8DirectSpatial(mb, -1, syntax.MotionVector{}, 0, syntax.MotionVector{X: 5, Y: 3}, col, 0, 0)
	want := [4]syntax.MotionVector{{}, {X: 5, Y: 3}, {}, {X: 5, Y: 3}}
	for i := 0; i < 4; i++ {
		if mb.SubMVL1[i] != want[i] {
			t.Fatalf("cell%d=%v want%v", i, mb.SubMVL1[i], want[i])
		}
	}
}

func TestDirect16Without8x8InferenceWritesEveryCell(t *testing.T) {
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16, Direct8x8InferenceSet: true}
	mb.RefIdxL0, mb.RefIdxL1 = [4]int8{0, 0, 0, 0}, [4]int8{0, 0, 0, 0}
	for i := 0; i < 16; i++ {
		mb.SubMVL0[i] = syntax.MotionVector{X: int16(i)}
		mb.SubMVL1[i] = syntax.MotionVector{Y: int16(-i)}
	}
	mv0, ref0 := make([]syntax.MotionVector, 16), make([]int8, 16)
	mv1, ref1 := make([]syntax.MotionVector, 16), make([]int8, 16)
	writeBackBidiL0Context(mv0, ref0, 4, 0, 0, mb)
	writeBackBidiL1Context(mv1, ref1, 4, 0, 0, mb)
	for scan := 0; scan < 16; scan++ {
		x, y := syntax.Blk4x4Col[scan], syntax.Blk4x4Row[scan]
		idx := y*4 + x
		if mv0[idx] != mb.SubMVL0[scan] || mv1[idx] != mb.SubMVL1[scan] || ref0[idx] != 0 || ref1[idx] != 0 {
			t.Fatalf("scan %d cache=%v/%v refs=%d/%d", scan, mv0[idx], mv1[idx], ref0[idx], ref1[idx])
		}
	}
}
