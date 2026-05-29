package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestWriteBackInter4x4HandlesInvalidInputs(t *testing.T) {
	writeBackInter4x4(nil, nil, 0, 0, 0, nil)
	writeBackInter4x4(make([]syntax.MotionVector, 1), nil, 4, 0, 0, &syntax.MBInter{MBType: syntax.PMBTypeP16x16})
	writeBackInter4x4(nil, make([]int8, 1), 4, 0, 0, &syntax.MBInter{MBType: syntax.PMBTypeP16x16})
	writeBackInter4x4(make([]syntax.MotionVector, 1), make([]int8, 1), 4, -1, -1, &syntax.MBInter{MBType: syntax.PMBTypeP16x16})
}

func TestWriteBackIntra4x4HandlesInvalidInputs(t *testing.T) {
	writeBackIntra4x4(nil, 0, 0, 0)
	writeBackIntra4x4(make([]int8, 1), 4, -1, -1)
}

func TestFillMV4HandlesInvalidInputs(t *testing.T) {
	fillMV4(nil, nil, 0, 0, 0, 1, 1, syntax.MotionVector{X: 1}, 0)
	fillMV4(make([]syntax.MotionVector, 1), nil, 4, 0, 0, 2, 2, syntax.MotionVector{X: 1}, 0)
	fillMV4(nil, make([]int8, 1), 4, 0, 0, 2, 2, syntax.MotionVector{X: 1}, 0)
	fillMV4(make([]syntax.MotionVector, 1), make([]int8, 1), 4, -1, -1, 2, 2, syntax.MotionVector{X: 1}, 0)
}

func TestCABACMVDContextVectorClampsMagnitude(t *testing.T) {
	got := cabacMVDContextVector(syntax.MotionVector{X: -99, Y: 42})
	if got.X != 70 || got.Y != 42 {
		t.Fatalf("context vector got %+v want {X:70 Y:42}", got)
	}
	if returned := (syntax.MotionVector{X: -99, Y: 42}); returned.X != -99 {
		t.Fatalf("test invariant: reconstruction MVD should stay signed/full, got %+v", returned)
	}
}

func TestCABACMVContextHelpersHandleInvalidInputs(t *testing.T) {
	if got := cabacRefIdxCtx([]int8{1}, 0, 0, 0); got != 0 {
		t.Fatalf("cabacRefIdxCtx zero stride got %d want 0", got)
	}
	if got := cabacRefIdxCtx([]int8{1}, 4, -1, 0); got != 0 {
		t.Fatalf("cabacRefIdxCtx negative origin got %d want 0", got)
	}
	if got := cabacMVDAMVD([]syntax.MotionVector{{X: 9, Y: -7}}, 0, 0, 0, 0); got != 0 {
		t.Fatalf("cabacMVDAMVD zero stride got %d want 0", got)
	}
	fillMVD4(nil, 0, 0, 0, 1, 1, syntax.MotionVector{X: 1})
	fillMVD4(make([]syntax.MotionVector, 1), 4, -1, -1, 2, 2, syntax.MotionVector{X: 1})
}

func TestGetMV4HandlesInvalidInputs(t *testing.T) {
	if _, ref := getMV4(nil, []int8{0}, 4, 0, 0); ref != -2 {
		t.Fatalf("short mv cache ref=%d want -2", ref)
	}
	if _, ref := getMV4([]syntax.MotionVector{{X: 1}}, []int8{0}, 0, 0, 0); ref != -2 {
		t.Fatalf("zero stride ref=%d want -2", ref)
	}
	if _, ref := getMV4([]syntax.MotionVector{{X: 1}}, []int8{0}, 4, -1, 0); ref != -2 {
		t.Fatalf("negative x ref=%d want -2", ref)
	}
}

func TestP16x8PredictsBottomFromCurrentTopPartition(t *testing.T) {
	mv4 := make([]syntax.MotionVector, 16)
	ref4 := make([]int8, 16)
	mb := &syntax.MBInter{
		MBType: syntax.PMBTypeP16x8,
		RefIdx: [4]int8{0, 0},
		MV:     [4]syntax.MotionVector{{X: 3, Y: 3}, {X: 0, Y: -1}},
	}
	applyMVPredictors(mb, mv4, ref4, 4, 0, 0)
	if got, want := mb.MV[1], (syntax.MotionVector{X: 3, Y: 2}); got != want {
		t.Fatalf("bottom P16x8 MV=%+v want %+v", got, want)
	}
}

func TestWriteBackBidiDirectPreservesZeroSubMV(t *testing.T) {
	mv4 := make([]syntax.MotionVector, 16)
	ref4 := make([]int8, 16)
	mb := &syntax.MBBidi{
		MBType:   syntax.BMBTypeDirect16x16,
		RefIdxL0: [4]int8{0},
		MVL0:     [4]syntax.MotionVector{{X: 3, Y: -2}},
	}
	writeBackBidiL0Context(mv4, ref4, 4, 0, 0, mb)
	for i := 0; i < 16; i++ {
		if mv4[i] != (syntax.MotionVector{}) || ref4[i] != 0 {
			t.Fatalf("direct writeback idx=%d mv=%+v ref=%d, want zero/ref0", i, mv4[i], ref4[i])
		}
	}
}

func TestWriteBackBidiDirectUsesPerPartSubMVs(t *testing.T) {
	mv4 := make([]syntax.MotionVector, 16)
	ref4 := make([]int8, 16)
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16, RefIdxL0: [4]int8{0}, MVL0: [4]syntax.MotionVector{{X: 9, Y: 9}}}
	mb.SubMVL0[0] = syntax.MotionVector{X: 1, Y: 1}
	mb.SubMVL0[4] = syntax.MotionVector{X: 2, Y: 2}
	mb.SubMVL0[8] = syntax.MotionVector{X: 3, Y: 3}
	mb.SubMVL0[12] = syntax.MotionVector{X: 4, Y: 4}
	writeBackBidiL0Context(mv4, ref4, 4, 0, 0, mb)
	want := []struct {
		idx int
		mv  syntax.MotionVector
	}{{0, mb.SubMVL0[0]}, {2, mb.SubMVL0[4]}, {8, mb.SubMVL0[8]}, {10, mb.SubMVL0[12]}}
	for _, w := range want {
		if mv4[w.idx] != w.mv || ref4[w.idx] != 0 {
			t.Fatalf("idx=%d mv=%+v ref=%d want %+v/ref0", w.idx, mv4[w.idx], ref4[w.idx], w.mv)
		}
	}
}

func TestColocatedDirectUses8x8ShapeMetadata(t *testing.T) {
	col := &frame.Frame{MotionStride4: 8, MBType: []uint32{ffMBType16x16, ffMBType8x8}, MotionL0: make([][2]int16, 64), RefIdxL0: make([]int8, 64)}
	if colocatedDirectUses8x8(col, 0, 0) {
		t.Fatalf("uniform 16x16 colocated shape must not use 8x8 direct derivation")
	}
	if !colocatedDirectUses8x8(col, 1, 0) {
		t.Fatalf("8x8 colocated shape should use 8x8 direct derivation")
	}
	col.MotionL0[3] = [2]int16{0, 1}
	if !colocatedDirectUses8x8(col, 0, 0) {
		t.Fatalf("16x16-shaped colocated row with distinct 8x8 representatives should use 8x8 zeroing")
	}
	if colocatedDirectUses8x8(col, -1, 0) || colocatedDirectUses8x8(col, 2, 0) {
		t.Fatalf("invalid colocated coordinates must be unavailable")
	}
}

func TestColocatedDirect8x8ZeroUsesFFmpegRepresentative(t *testing.T) {
	col := &frame.Frame{POC: 14, MotionStride4: 8, MotionL0: make([][2]int16, 64), RefIdxL0: make([]int8, 64)}
	for i := range col.RefIdxL0 {
		col.RefIdxL0[i] = -2
	}
	// mb=(1,1), bottom-right 8x8 direct partition samples 4x4 position
	// x=1*4+3, y=1*4+3 (FFmpeg x8*3/y8*3), not the 8x8 centre.
	idx := 7*col.MotionStride4 + 7
	col.RefIdxL0[idx] = 0
	col.MotionL0[idx] = [2]int16{-1, 1}
	if !colocatedDirect8x8Zero(col, 1, 1, 3, 6) {
		t.Fatalf("expected FFmpeg representative {-1,1}/ref0 to trigger direct zero")
	}
	col.MotionL0[idx] = [2]int16{-1, 2}
	if colocatedDirect8x8Zero(col, 1, 1, 3, 6) {
		t.Fatalf("expected y magnitude > 1 not to trigger direct zero")
	}
	if colocatedDirect8x8Zero(col, -1, 1, 3, 6) || colocatedDirect8x8Zero(col, 1, -1, 3, 6) || colocatedDirect8x8Zero(col, 1, 1, 4, 6) {
		t.Fatalf("invalid macroblock coordinates or partition must not sample wrapped cache positions")
	}
}

func TestColocatedDirect16x16ZeroUsesTopLeftRepresentative(t *testing.T) {
	col := &frame.Frame{POC: 12, MotionStride4: 8, MotionL0: make([][2]int16, 64), RefIdxL0: make([]int8, 64)}
	for i := range col.RefIdxL0 {
		col.RefIdxL0[i] = -2
	}
	idx := 4*col.MotionStride4 + 4
	col.RefIdxL0[idx] = 0
	col.MotionL0[idx] = [2]int16{1, -1}
	if !colocatedDirect16x16Zero(col, 1, 1, 6) {
		t.Fatalf("small top-left colocated MV should zero 16x16 direct")
	}
	col.MotionL0[idx] = [2]int16{2, 0}
	if colocatedDirect16x16Zero(col, 1, 1, 6) {
		t.Fatalf("large top-left colocated MV must not zero 16x16 direct")
	}
}

func TestApplyBDirect16x16SpatialSubMVsCopiesAndZerosRepresentatives(t *testing.T) {
	col := &frame.Frame{MotionStride4: 8, MBType: []uint32{0, ffMBType8x8}, MotionL0: make([][2]int16, 64), RefIdxL0: make([]int8, 64)}
	for i := range col.RefIdxL0 {
		col.RefIdxL0[i] = -2
	}
	// mb=(1,0), parts 0/1 have large colocated MVs, part 2 is zero-eligible.
	for _, p := range []int{0, 1, 3} {
		x := 4 + (p&1)*3
		y := (p >> 1) * 3
		idx := y*col.MotionStride4 + x
		col.RefIdxL0[idx] = 0
		col.MotionL0[idx] = [2]int16{3, 2}
	}
	idx2 := 3*col.MotionStride4 + 4
	col.RefIdxL0[idx2] = 0
	col.MotionL0[idx2] = [2]int16{1, 1}

	mb := &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16, RefIdxL0: [4]int8{0}, RefIdxL1: [4]int8{-1, -1, -1, -1}}
	mb.MVL0[0] = syntax.MotionVector{X: 0, Y: 1}
	applyBDirect16x16SpatialSubMVs(mb, col, 1, 0)
	if mb.SubMVL0[0] != mb.MVL0[0] || mb.SubMVL0[4] != mb.MVL0[0] || mb.SubMVL0[12] != mb.MVL0[0] {
		t.Fatalf("non-zero-eligible parts should retain full direct MV: %+v", mb.SubMVL0)
	}
	if mb.SubMVL0[8] != (syntax.MotionVector{}) {
		t.Fatalf("zero-eligible part should be cleared, got %+v", mb.SubMVL0[8])
	}
}

func TestBSubPartOffset4x4MatchesSubPartitionShapes(t *testing.T) {
	tests := []struct {
		typ        uint32
		part, x, y int
	}{
		{1, 0, 0, 0},  // 8x8
		{4, 1, 0, 1},  // 8x4 bottom
		{5, 1, 1, 0},  // 4x8 right
		{10, 2, 0, 1}, // 4x4 bottom-left scan
		{10, 3, 1, 1}, // 4x4 bottom-right scan
	}
	for _, tt := range tests {
		x, y := bSubPartOffset4x4(tt.typ, tt.part)
		if x != tt.x || y != tt.y {
			t.Fatalf("type=%d part=%d offset=(%d,%d), want (%d,%d)", tt.typ, tt.part, x, y, tt.x, tt.y)
		}
	}
}

func TestWriteBackBidiB8x8UsesSubPartitionShapes(t *testing.T) {
	mv4 := make([]syntax.MotionVector, 16)
	ref4 := make([]int8, 16)
	for i := range ref4 {
		ref4[i] = -2
	}
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeB8x8}
	mb.SubMBType[0] = 1 // L0_8x8: fills top-left 2x2
	mb.SubMBType[1] = 5 // L0_4x8: fills two vertical 1x2 partitions in top-right
	mb.SubMVL0[0] = syntax.MotionVector{X: 1, Y: 1}
	mb.SubMVL0[4] = syntax.MotionVector{X: 2, Y: 2}
	mb.SubMVL0[5] = syntax.MotionVector{X: 3, Y: 3}
	mb.RefIdxL0[0], mb.RefIdxL0[1] = 0, 1
	writeBackBidiL0Context(mv4, ref4, 4, 0, 0, mb)

	for _, idx := range []int{0, 1, 4, 5} {
		if mv4[idx] != mb.SubMVL0[0] || ref4[idx] != 0 {
			t.Fatalf("8x8 fill idx=%d mv=%+v ref=%d", idx, mv4[idx], ref4[idx])
		}
	}
	for _, idx := range []int{2, 6} {
		if mv4[idx] != mb.SubMVL0[4] || ref4[idx] != 1 {
			t.Fatalf("4x8 left fill idx=%d mv=%+v ref=%d", idx, mv4[idx], ref4[idx])
		}
	}
	for _, idx := range []int{3, 7} {
		if mv4[idx] != mb.SubMVL0[5] || ref4[idx] != 1 {
			t.Fatalf("4x8 right fill idx=%d mv=%+v ref=%d", idx, mv4[idx], ref4[idx])
		}
	}
}

func TestWriteBackBidiB8x8DirectWritesBothLists(t *testing.T) {
	mv4L0 := make([]syntax.MotionVector, 16)
	mv4L1 := make([]syntax.MotionVector, 16)
	ref4L0 := make([]int8, 16)
	ref4L1 := make([]int8, 16)
	for i := range ref4L0 {
		ref4L0[i], ref4L1[i] = -2, -2
	}
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeB8x8}
	mb.SubMBType[2] = 0 // Direct bottom-left 8x8.
	mb.RefIdxL0[2], mb.RefIdxL1[2] = 0, 1
	mb.SubMVL0[8] = syntax.MotionVector{X: 4, Y: -1}
	mb.SubMVL1[8] = syntax.MotionVector{X: -2, Y: 3}

	writeBackBidiL0Context(mv4L0, ref4L0, 4, 0, 0, mb)
	writeBackBidiL1Context(mv4L1, ref4L1, 4, 0, 0, mb)

	for _, idx := range []int{8, 9, 12, 13} {
		if mv4L0[idx] != mb.SubMVL0[8] || ref4L0[idx] != 0 {
			t.Fatalf("L0 direct fill idx=%d mv=%+v ref=%d", idx, mv4L0[idx], ref4L0[idx])
		}
		if mv4L1[idx] != mb.SubMVL1[8] || ref4L1[idx] != 1 {
			t.Fatalf("L1 direct fill idx=%d mv=%+v ref=%d", idx, mv4L1[idx], ref4L1[idx])
		}
	}
}

func TestPredictBPartMotionUsesShapeForAllBTwoPartitionTypes(t *testing.T) {
	stride := 8
	mv4 := make([]syntax.MotionVector, stride*8)
	ref4 := make([]int8, stride*8)
	for i := range ref4 {
		ref4[i] = -2
	}
	// For first 16x8 partitions, directional prediction must prefer top even
	// when generic median would choose a different value.
	mv4[0*stride+1] = syntax.MotionVector{X: 30, Y: 30} // top
	ref4[0*stride+1] = 0
	mv4[1*stride+0] = syntax.MotionVector{X: 20, Y: 20} // left
	ref4[1*stride+0] = 0
	mv4[0*stride+5] = syntax.MotionVector{X: 0, Y: 0} // diagonal
	ref4[0*stride+5] = 0

	for _, typ := range []uint32{4, 6, 8, 10, 12, 14, 16, 18, 20} {
		got := predictBPartMotion4x4(mv4, ref4, stride, 1, 1, typ, 0, 0)
		if got != (syntax.MotionVector{X: 30, Y: 30}) {
			t.Fatalf("type %d 16x8 part0 MVP=%+v want top", typ, got)
		}
	}

	// For second 8x16 partitions, directional prediction must prefer diagonal.
	for i := range ref4 {
		ref4[i] = -2
		mv4[i] = syntax.MotionVector{}
	}
	mv4[0*stride+5] = syntax.MotionVector{X: -3, Y: 2} // diagonal
	ref4[0*stride+5] = 0
	mv4[1*stride+2] = syntax.MotionVector{X: 6, Y: 6} // left for generic median
	ref4[1*stride+2] = 0
	mv4[0*stride+3] = syntax.MotionVector{X: 7, Y: 7} // top for generic median
	ref4[0*stride+3] = 0
	for _, typ := range []uint32{5, 7, 9, 11, 13, 15, 17, 19, 21} {
		got := predictBPartMotion4x4(mv4, ref4, stride, 1, 1, typ, 1, 0)
		if got != (syntax.MotionVector{X: -3, Y: 2}) {
			t.Fatalf("type %d 8x16 part1 MVP=%+v want diagonal", typ, got)
		}
	}
}

func TestCABACBListsForTypeUsesPerPartitionTables(t *testing.T) {
	tests := []struct {
		typ            uint32
		wantL0, wantL1 bool
	}{
		{syntax.BMBTypeL016x16, true, false},
		{syntax.BMBTypeL116x16, false, true},
		{syntax.BMBTypeBi16x16, true, true},
		{10, true, true}, // B_L1_L0_16x8
		{11, true, true}, // B_L1_L0_8x16
		{12, true, true}, // B_L0_Bi_16x8
		{13, true, true}, // B_L0_Bi_8x16
	}
	for _, tt := range tests {
		gotL0, gotL1 := cabacBListsForType(tt.typ)
		if gotL0 != tt.wantL0 || gotL1 != tt.wantL1 {
			t.Fatalf("type %d lists=(%v,%v), want (%v,%v)", tt.typ, gotL0, gotL1, tt.wantL0, tt.wantL1)
		}
	}
}

func TestApplyTemporalDirectB8x8OnlyTouchesDirectSubMBs(t *testing.T) {
	col := &frame.Frame{POC: 12, MotionStride4: 4, MotionL0: make([][2]int16, 16), RefIdxL0: make([]int8, 16)}
	for i := range col.RefIdxL0 {
		col.RefIdxL0[i] = 0
		col.MotionL0[i] = [2]int16{4, 2}
	}
	l0 := []*frame.Frame{{POC: 4}}
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeB8x8}
	mb.SubMBType = [4]uint32{0, 1, 0, 3}
	mb.SubMVL0[4] = syntax.MotionVector{X: 11, Y: 12}
	mb.SubMVL1[4] = syntax.MotionVector{X: -11, Y: -12}
	mb.SubMVL0[12] = syntax.MotionVector{X: 21, Y: 22}
	mb.SubMVL1[12] = syntax.MotionVector{X: -21, Y: -22}

	applyTemporalDirect(mb, col, 0, 0, 8, l0, col.POC)

	if mb.SubMVL0[0] == (syntax.MotionVector{}) || mb.SubMVL1[0] == (syntax.MotionVector{}) {
		t.Fatalf("direct sub-MB 0 was not temporally derived: L0=%+v L1=%+v", mb.SubMVL0[0], mb.SubMVL1[0])
	}
	if mb.SubMVL0[8] == (syntax.MotionVector{}) || mb.SubMVL1[8] == (syntax.MotionVector{}) {
		t.Fatalf("direct sub-MB 2 was not temporally derived: L0=%+v L1=%+v", mb.SubMVL0[8], mb.SubMVL1[8])
	}
	if mb.SubMVL0[4] != (syntax.MotionVector{X: 11, Y: 12}) || mb.SubMVL1[4] != (syntax.MotionVector{X: -11, Y: -12}) {
		t.Fatalf("explicit sub-MB 1 was overwritten: L0=%+v L1=%+v", mb.SubMVL0[4], mb.SubMVL1[4])
	}
	if mb.SubMVL0[12] != (syntax.MotionVector{X: 21, Y: 22}) || mb.SubMVL1[12] != (syntax.MotionVector{X: -21, Y: -22}) {
		t.Fatalf("explicit sub-MB 3 was overwritten: L0=%+v L1=%+v", mb.SubMVL0[12], mb.SubMVL1[12])
	}
}
