package decode

import (
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func TestCAVLCB8x8SubpartitionCacheFeedsNextMB(t *testing.T) {
	c := newBMotionCache(12, 2)
	mb := &syntax.MBBidi{
		MBType:    syntax.BMBTypeB8x8,
		SubMBType: [4]uint32{0, 0, 1, 7},
		RefIdxL0:  [4]int8{0, 0, 0, -1}, RefIdxL1: [4]int8{0, 0, -1, 0},
	}
	mb.SubMVL1[12], mb.SubMVL1[13] = syntax.MotionVector{Y: -10}, syntax.MotionVector{Y: 9}
	// The production pipeline derives Direct regions before replaying CAVLC
	// explicit MVDs. Seed both top Direct 8x8 regions as that pass would.
	fillMV4(c.mv[0], c.ref[0], c.stride4, 0, 0, 4, 2, syntax.MotionVector{}, 0)
	fillMV4(c.mv[1], c.ref[1], c.stride4, 0, 0, 4, 2, syntax.MotionVector{}, 0)
	applyCAVLCB8x8Motion(c, mb, 0, 0)
	want := [2]syntax.MotionVector{{Y: -10}, {Y: 9}}
	for i := range want {
		if mb.SubMVL1[12+i] != want[i] {
			t.Fatalf("compact subpartition %d MV=%v want %v", i, mb.SubMVL1[12+i], want[i])
		}
	}
	for row := 0; row < 2; row++ {
		for col := 0; col < 2; col++ {
			idx := row*c.stride4 + col
			if c.ref[1][idx] != 0 || c.mv[1][idx] != (syntax.MotionVector{}) {
				t.Fatalf("direct cache cell %d ref/MV=%d/%v", idx, c.ref[1][idx], c.mv[1][idx])
			}
		}
	}
}

func TestCAVLCB8x8PreservesCompactMVDSlotsDuringExpansion(t *testing.T) {
	c := newBMotionCache(8, 2)
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeB8x8, SubMBType: [4]uint32{4, 0, 0, 0}, RefIdxL0: [4]int8{0, -1, -1, -1}, RefIdxL1: [4]int8{-1, -1, -1, -1}}
	mb.SubMVL0[0], mb.SubMVL0[2] = syntax.MotionVector{Y: -1}, syntax.MotionVector{Y: -5}
	applyCAVLCB8x8Motion(c, mb, 0, 0)
	if mb.SubMVL0[0] != (syntax.MotionVector{Y: -1}) || mb.SubMVL0[2] != (syntax.MotionVector{Y: -6}) {
		t.Fatalf("compact final MVs=%v want slots 0/2 = [{0 -1} {0 -6}]", mb.SubMVL0[:3])
	}
}

func TestCAVLCB8x8LocalReplayMatchesTraceFallbackAtEdges(t *testing.T) {
	for _, pos := range []struct{ x, y int }{{0, 0}, {1, 1}, {2, 0}, {2, 1}} {
		t.Run(string(rune('0'+pos.x))+string(rune('0'+pos.y)), func(t *testing.T) {
			base := newBMotionCache(12, 2)
			for list := 0; list < 2; list++ {
				for i := range base.ref[list] {
					base.ref[list][i] = int8(i%3 - 1)
					base.mv[list][i] = syntax.MotionVector{X: int16(i%17 - 8), Y: int16(i%11 - 5)}
				}
			}
			mb := syntax.MBBidi{MBType: syntax.BMBTypeB8x8, SubMBType: [4]uint32{0, 4, 7, 1},
				RefIdxL0: [4]int8{0, 0, -1, 0}, RefIdxL1: [4]int8{0, -1, 0, -1},
				Direct8x8InferenceSet: true}
			for i := range mb.SubMVL0 {
				mb.SubMVL0[i] = syntax.MotionVector{X: int16(i - 3), Y: int16(i + 1)}
				mb.SubMVL1[i] = syntax.MotionVector{X: int16(-i), Y: int16(i - 2)}
			}
			for _, infer := range []bool{false, true} {
				got, want := base, base
				for list := 0; list < 2; list++ {
					got.mv[list] = append([]syntax.MotionVector(nil), base.mv[list]...)
					got.ref[list] = append([]int8(nil), base.ref[list]...)
					want.mv[list] = append([]syntax.MotionVector(nil), base.mv[list]...)
					want.ref[list] = append([]int8(nil), base.ref[list]...)
				}
				actual, expected := mb, mb
				actual.Direct8x8Inference, expected.Direct8x8Inference = infer, infer
				want.trace = &traceConfig{flags: traceBMVP}
				applyCAVLCB8x8Motion(got, &actual, pos.x, pos.y)
				applyCAVLCB8x8Motion(want, &expected, pos.x, pos.y)
				if !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(got.mv, want.mv) || !reflect.DeepEqual(got.ref, want.ref) {
					t.Fatalf("at (%d,%d) inference=%t: local replay differs from full-coordinate replay", pos.x, pos.y, infer)
				}
			}
		})
	}
}

func TestPredictB8x16RightEdgeFallsBackToGenericCandidates(t *testing.T) {
	const stride = 8
	mv := make([]syntax.MotionVector, stride*8)
	ref := make([]int8, len(mv))
	for i := range ref {
		ref[i] = -2
	}
	x4, y4 := 4, 4
	// Right partition A is the completed left partition, B is above, C unused.
	fillMV4(mv, ref, stride, x4, y4, 2, 4, syntax.MotionVector{X: -41, Y: -1}, 0)
	mv[(y4-1)*stride+x4+2], ref[(y4-1)*stride+x4+2] = syntax.MotionVector{X: -14, Y: -5}, 0
	mv[(y4-1)*stride+x4+1], ref[(y4-1)*stride+x4+1] = syntax.MotionVector{}, -1
	got := predictBPartMotion4x4(mv, ref, stride, x4, y4, 5, 1, 0)
	if got != (syntax.MotionVector{X: -14, Y: -1}) {
		t.Fatalf("right-edge MVP=%v want {-14 -1}", got)
	}
}
