package decode

import (
	"testing"

	"github.com/rcarmo/go-264/entropy/cabac"
	"github.com/rcarmo/go-264/syntax"
)

func TestStoreCABACChromaDCDistributesAcrossBlocks(t *testing.T) {
	mb := &syntax.MBInter{}
	storeCABACChromaDC(mb, 0, [4]int16{11, 22, 33, 44})
	for blk, want := range []int16{11, 22, 33, 44} {
		if got := mb.CoeffsChroma[0][blk][0]; got != want {
			t.Fatalf("block %d DC got %d want %d", blk, got, want)
		}
	}
}

func TestStoreCABACChromaACPreservesDC(t *testing.T) {
	mb := &syntax.MBInter{}
	mb.CoeffsChroma[1][2][0] = 99
	var ac [16]int16
	for i := range ac {
		ac[i] = int16(i + 1)
	}
	storeCABACChromaAC(mb, 1, 2, ac)
	if got := mb.CoeffsChroma[1][2][0]; got != 99 {
		t.Fatalf("DC overwritten: got %d want 99", got)
	}
	for i := 1; i < 16; i++ {
		if got, want := mb.CoeffsChroma[1][2][i], ac[i]; got != want {
			t.Fatalf("AC[%d] got %d want %d", i, got, want)
		}
	}
}

func TestCABACChromaPredModeCtxMatchesFFmpeg(t *testing.T) {
	cases := []struct {
		left, top int8
		want      int
	}{
		{0, 0, 0},
		{1, 0, 1},
		{0, 2, 1},
		{3, 2, 2},
	}
	for _, c := range cases {
		if got := cabacChromaPredModeCtx(c.left, c.top); got != c.want {
			t.Fatalf("ctx(left=%d, top=%d) got %d want %d", c.left, c.top, got, c.want)
		}
	}
}

func TestStoreCABACIntraChromaResidualsMatchDCSplit(t *testing.T) {
	mb := &syntax.MBIntra{}
	storeCABACIntraChromaDC(mb, 1, [4]int16{5, 6, 7, 8})
	for blk, want := range []int16{5, 6, 7, 8} {
		if got := mb.CoeffsChroma[1][blk][0]; got != want {
			t.Fatalf("intra block %d DC got %d want %d", blk, got, want)
		}
	}
	var ac [16]int16
	for i := range ac {
		ac[i] = int16(100 + i)
	}
	storeCABACIntraChromaAC(mb, 1, 2, ac)
	if got := mb.CoeffsChroma[1][2][0]; got != 7 {
		t.Fatalf("intra AC overwrote DC: got %d want 7", got)
	}
	for i := 1; i < 16; i++ {
		if got, want := mb.CoeffsChroma[1][2][i], ac[i]; got != want {
			t.Fatalf("intra AC[%d] got %d want %d", i, got, want)
		}
	}
}

func TestDecodeCABACPInterMBHandlesShortContextTable(t *testing.T) {
	mb, intra, skipped := decodeCABACPInterMB(nil, make([]cabac.CABACCtx, 24), 1, nil, nil, nil, nil, 0, 0, false, false, [4]int{}, nil, 0, 0, 0, true, 0, 0, 0, 0, 0, [2]int8{}, [2]int8{})
	if mb == nil || intra != nil || !skipped {
		t.Fatalf("short context table got mb=%v intra=%v skipped=%v", mb, intra, skipped)
	}
}

func TestCABACInter8x8TransformAllowedMatchesFFmpegP8x8Gate(t *testing.T) {
	for _, mbType := range []uint32{syntax.PMBTypeP16x16, syntax.PMBTypeP16x8, syntax.PMBTypeP8x16} {
		if !cabacInter8x8TransformAllowed(&syntax.MBInter{MBType: mbType}) {
			t.Fatalf("mb_type %d unexpectedly disallows transform_size_8x8_flag", mbType)
		}
	}
	if !cabacInter8x8TransformAllowed(&syntax.MBInter{MBType: syntax.PMBTypeP8x8}) {
		t.Fatal("P8x8 with four 8x8 sub partitions should allow transform_size_8x8_flag")
	}
	if !cabacInter8x8TransformAllowed(&syntax.MBInter{MBType: syntax.PMBTypeP8x8ref0}) {
		t.Fatal("P8x8ref0 with four 8x8 sub partitions should allow transform_size_8x8_flag")
	}
	for _, subType := range []uint32{1, 2, 3} {
		mb := &syntax.MBInter{MBType: syntax.PMBTypeP8x8}
		mb.SubMBType[2] = subType
		if cabacInter8x8TransformAllowed(mb) {
			t.Fatalf("P8x8 sub_mb_type %d unexpectedly allows transform_size_8x8_flag", subType)
		}
	}
	if cabacInter8x8TransformAllowed(nil) {
		t.Fatal("nil macroblock allowed transform_size_8x8_flag")
	}
}

func TestDecodeCABACTransform8x8FlagHandlesInvalidInputs(t *testing.T) {
	if decodeCABACTransform8x8Flag(nil, nil, 0) {
		t.Fatal("nil decoder/models decoded a transform-size flag")
	}
}

func TestCABACTransform8x8CtxClampsToSpecRange(t *testing.T) {
	cases := map[int]int{-3: 0, -1: 0, 0: 0, 1: 1, 2: 2, 3: 2, 99: 2}
	for in, want := range cases {
		if got := cabacTransform8x8Ctx(in); got != want {
			t.Fatalf("cabacTransform8x8Ctx(%d) got %d want %d", in, got, want)
		}
	}
}

func TestStoreCABACChromaHelpersIgnoreInvalidInputs(t *testing.T) {
	storeCABACChromaDC(nil, 0, [4]int16{1, 2, 3, 4})
	storeCABACChromaAC(nil, 0, 0, [16]int16{})
	mb := &syntax.MBInter{}
	storeCABACChromaDC(mb, -1, [4]int16{1, 2, 3, 4})
	storeCABACChromaDC(mb, 2, [4]int16{1, 2, 3, 4})
	storeCABACChromaAC(mb, -1, 0, [16]int16{1})
	storeCABACChromaAC(mb, 2, 0, [16]int16{1})
	storeCABACChromaAC(mb, 0, -1, [16]int16{1})
	storeCABACChromaAC(mb, 0, 4, [16]int16{1})
	if mb.CoeffsChroma != [2][4][16]int16{} {
		t.Fatalf("invalid helper inputs mutated MB: %+v", mb.CoeffsChroma)
	}
}
