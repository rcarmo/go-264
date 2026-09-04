package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestBMotionCacheInitializesSplitLists(t *testing.T) {
	c := newBMotionCache(8, 2)
	if len(c.mv4(0)) != 64 || len(c.mv4(1)) != 64 || len(c.mvd4(0)) != 64 || len(c.mvd4(1)) != 64 || len(c.ref4(0)) != 64 || len(c.ref4(1)) != 64 {
		t.Fatalf("unexpected cache sizes: mv0=%d mv1=%d mvd0=%d mvd1=%d ref0=%d ref1=%d", len(c.mv4(0)), len(c.mv4(1)), len(c.mvd4(0)), len(c.mvd4(1)), len(c.ref4(0)), len(c.ref4(1)))
	}
	for list := 0; list < 2; list++ {
		for i, ref := range c.ref4(list) {
			if ref != -2 {
				t.Fatalf("list=%d idx=%d ref=%d want -2", list, i, ref)
			}
		}
	}
}

func TestBMotionCacheHelpersUseListState(t *testing.T) {
	c := newBMotionCache(8, 2)
	c.ref4(0)[0], c.mv4(0)[0] = 0, syntax.MotionVector{X: 1, Y: 2}
	c.ref4(1)[0], c.mv4(1)[0] = 1, syntax.MotionVector{X: 3, Y: 4}
	mv, ref := c.get(1, 0, 0)
	if ref != 1 || mv != (syntax.MotionVector{X: 3, Y: 4}) {
		t.Fatalf("list1 get mv=%+v ref=%d", mv, ref)
	}
	ctx := c.refIdxCtxs(0, 0)
	if ctx[0] != 0 {
		t.Fatalf("top-left ref ctx=%d want 0", ctx[0])
	}
	// Put left/top neighbours around MB (1,1) so skip/direct predictors exercise
	// cache-owned L0 state instead of raw pipeline arrays.
	leftIdx := 4*8 + 3
	topIdx := 3*8 + 4
	c.ref4(0)[leftIdx], c.mv4(0)[leftIdx] = 0, syntax.MotionVector{X: 5, Y: 6}
	c.ref4(0)[topIdx], c.mv4(0)[topIdx] = 0, syntax.MotionVector{X: 0, Y: 0}
	if pred := c.predictSkipL0(4, 4); pred != (syntax.MotionVector{}) {
		t.Fatalf("skip pred=%+v want zero because top neighbour is zero", pred)
	}
	if ref := c.directSpatialL0Ref(4, 4); ref != 0 {
		t.Fatalf("direct spatial ref=%d want 0", ref)
	}
}

func TestBMotionCacheSaveL0ToFrame(t *testing.T) {
	c := newBMotionCache(4, 1)
	c.mv4(0)[0] = syntax.MotionVector{X: 7, Y: -2}
	c.ref4(0)[0] = 1
	f := &frame.Frame{}
	c.saveL0ToFrame(f, []uint32{123}, []*frame.Frame{{FrameNum: 1, POC: 2}, {FrameNum: 2, POC: 4}})
	if f.MotionStride4 != 4 || len(f.MotionL0) != 16 || len(f.RefIdxL0) != 16 || len(f.MotionL1) != 16 || len(f.RefIdxL1) != 16 || len(f.MBType) != 1 {
		t.Fatalf("unexpected saved frame sizes/stride: stride=%d motion0=%d ref0=%d motion1=%d ref1=%d mbtype=%d", f.MotionStride4, len(f.MotionL0), len(f.RefIdxL0), len(f.MotionL1), len(f.RefIdxL1), len(f.MBType))
	}
	if f.MotionL0[0] != [2]int16{7, -2} || f.RefIdxL0[0] != 1 || f.MotionL1[0] != [2]int16{} || f.RefIdxL1[0] != -2 || f.MBType[0] != 123 {
		t.Fatalf("unexpected saved frame values: mv0=%v ref0=%d mv1=%v ref1=%d mbtype=%d", f.MotionL0[0], f.RefIdxL0[0], f.MotionL1[0], f.RefIdxL1[0], f.MBType[0])
	}
	c.ref4(0)[0], f.RefIdxL0[0] = 2, 3
	if f.TemporalRefIdxL0[0] != 1 {
		t.Fatal("saved temporal indices alias the scratch or spatial indices")
	}
}

func TestSavedSliceIndicesKeepSpatialAndTemporalDirectSeparate(t *testing.T) {
	for _, raw := range []int8{0, 1} {
		t.Run(string(rune('0'+raw)), func(t *testing.T) {
			d := assemblyDecoder(2, 1)
			s := &sliceState{sps: d.SPS[0], pps: d.PPS[0], header: &syntax.Header{SliceType: syntax.SliceTypeP}}
			p := d.newPicture(s)
			d.picture, d.slice, d.mbW = p, s, 2
			a := &frame.Frame{IsRef: true, FrameNum: 0, POC: 0}
			b := &frame.Frame{IsRef: true, FrameNum: 1, POC: 4}
			d.DPB.Frames = []*frame.Frame{a, b}
			d.activeL0Refs = []*frame.Frame{a, b}
			p.motion.writeBackInterL0(0, 0, &syntax.MBInter{MBType: syntax.PMBTypeP16x16})
			d.saveSlice(s, 0, 1)

			// The second slice reverses List 0: local 0 becomes temporal 1,
			// and local 1 becomes temporal 0. Both have zero-eligible motion.
			d.activeL0Refs = []*frame.Frame{b, a}
			p.motion.writeBackInterL0(1, 0, &syntax.MBInter{MBType: syntax.PMBTypeP16x16,
				RefIdx: [4]int8{raw}, MV: [4]syntax.MotionVector{{X: 1, Y: 1}}})
			d.saveSlice(s, 1, 2)
			mb := &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16}
			mv0, mv1 := syntax.MotionVector{X: 8}, syntax.MotionVector{Y: 8}
			p.motion.applyDirectSpatial(1, 0, mb, 0, mv0, 0, mv1, p.frame)
			want0, want1 := mv0, mv1
			if raw == 0 {
				want0, want1 = syntax.MotionVector{}, syntax.MotionVector{}
			}
			for part := 0; part < 4; part++ {
				if mb.SubMVL0[part*4] != want0 || mb.SubMVL1[part*4] != want1 {
					t.Fatalf("spatial direct used remapped index: raw=%d part=%d L0=%v L1=%v", raw, part, mb.SubMVL0[part*4], mb.SubMVL1[part*4])
				}
			}
			p.motion.applyDirectTemporal(1, 0, mb, p.frame, 8, []*frame.Frame{a, b}, 12)
			if mb.RefIdxL0 != [4]int8{1 - raw, 1 - raw, 1 - raw, 1 - raw} {
				t.Fatalf("temporal direct lost reference identity: raw=%d refs=%v", raw, mb.RefIdxL0)
			}
		})
	}
}

func TestBMotionCacheApplyDirectSpatialInitializesDirect16x16(t *testing.T) {
	c := newBMotionCache(4, 1)
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16}
	mv0 := syntax.MotionVector{X: 1, Y: 2}
	mv1 := syntax.MotionVector{X: -3, Y: 4}
	c.applyDirectSpatial(0, 0, mb, 1, mv0, 0, mv1, nil)
	if mb.RefIdxL0[0] != 1 || mb.MVL0[0] != mv0 || mb.MVL1[0] != mv1 {
		t.Fatalf("direct spatial init mismatch: %+v", mb)
	}
}

func TestBMotionCacheInitDirect16x16(t *testing.T) {
	c := newBMotionCache(4, 1)
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16}
	mv0 := syntax.MotionVector{X: 1, Y: 2}
	mv1 := syntax.MotionVector{X: -3, Y: 4}
	c.initDirect16x16(mb, 1, mv0, 0, mv1)
	if mb.RefIdxL0[0] != 1 || mb.MVL0[0] != mv0 || mb.MVL1[0] != mv1 {
		t.Fatalf("direct init L0/L1 mismatch: %+v", mb)
	}
	for i, ref := range mb.RefIdxL1 {
		if ref != 0 {
			t.Fatalf("RefIdxL1[%d]=%d want 0", i, ref)
		}
	}
}

func TestBMotionCacheApplyInterMVPredictors(t *testing.T) {
	c := newBMotionCache(8, 1)
	// Left neighbour at x4=3,y4=0 predicts the current MB at x4=4,y4=0.
	c.ref4(0)[3] = 0
	c.mv4(0)[3] = syntax.MotionVector{X: 2, Y: 3}
	mb := &syntax.MBInter{MBType: syntax.PMBTypeP16x16, RefIdx: [4]int8{0}}
	c.applyInterMVPredictors(mb, 1, 0, -1)
	if mb.MV[0] != (syntax.MotionVector{X: 2, Y: 3}) {
		t.Fatalf("predicted MV=%+v want {2,3}", mb.MV[0])
	}
}

func TestBMotionCacheWriteBackInterL0(t *testing.T) {
	c := newBMotionCache(4, 1)
	mb := &syntax.MBInter{MBType: syntax.PMBTypeP16x16, RefIdx: [4]int8{0}, MV: [4]syntax.MotionVector{{X: 2, Y: -1}}}
	c.writeBackInterL0(0, 0, mb)
	for i := 0; i < 16; i++ {
		if c.mv4(0)[i] != mb.MV[0] || c.ref4(0)[i] != 0 {
			t.Fatalf("L0 idx=%d mv=%+v ref=%d", i, c.mv4(0)[i], c.ref4(0)[i])
		}
		if c.ref4(1)[i] != -2 {
			t.Fatalf("L1 idx=%d ref=%d should remain unavailable", i, c.ref4(1)[i])
		}
	}
}

func TestBMotionCacheWriteBackIntraMarksBothLists(t *testing.T) {
	c := newBMotionCache(4, 1)
	for list := 0; list < 2; list++ {
		for i := range c.ref4(list) {
			c.ref4(list)[i] = 0
		}
	}
	c.writeBackIntra(0, 0)
	for list := 0; list < 2; list++ {
		for i, ref := range c.ref4(list) {
			if ref != -1 {
				t.Fatalf("list=%d idx=%d ref=%d want -1", list, i, ref)
			}
		}
	}
}

func TestBMotionCacheWriteBackKeepsListsSeparate(t *testing.T) {
	c := newBMotionCache(4, 1)
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeBi16x16, RefIdxL0: [4]int8{0}, RefIdxL1: [4]int8{1}}
	mb.MVL0[0] = syntax.MotionVector{X: 3, Y: 4}
	mb.MVL1[0] = syntax.MotionVector{X: -2, Y: 1}
	c.writeBackBidi(0, 0, 6, mb)
	for i := 0; i < 16; i++ {
		if c.mv4(0)[i] != mb.MVL0[0] || c.ref4(0)[i] != 0 {
			t.Fatalf("L0 idx=%d mv=%+v ref=%d", i, c.mv4(0)[i], c.ref4(0)[i])
		}
		if c.mv4(1)[i] != mb.MVL1[0] || c.ref4(1)[i] != 1 {
			t.Fatalf("L1 idx=%d mv=%+v ref=%d", i, c.mv4(1)[i], c.ref4(1)[i])
		}
	}
}
