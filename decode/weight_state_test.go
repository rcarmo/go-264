package decode

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func TestLoadPredictionWeightsCopiesBothBLists(t *testing.T) {
	h := &syntax.Header{SliceType: syntax.SliceTypeB, WeightedTablePresent: true, LumaLog2WeightDenom: 3, ChromaLog2WeightDenom: 2}
	h.LumaWeightL0[0], h.LumaOffsetL0[0] = 5, -7
	h.LumaWeightL1[0], h.LumaOffsetL1[0] = -3, 9
	h.ChromaWeightL0[0], h.ChromaOffsetL0[0] = [2]int32{1, 2}, [2]int32{-4, 6}
	h.ChromaWeightL1[0], h.ChromaOffsetL1[0] = [2]int32{-5, 7}, [2]int32{8, -9}
	pps := &nal.PPS{WeightedBipredIDC: 1}
	var d Decoder
	d.loadPredictionWeights(h, pps)
	if d.weightedBipredIDC != 1 || d.lumaWeightDenom != 3 || d.chromaWeightDenom != 2 {
		t.Fatalf("mode/denominators = %d/%d/%d", d.weightedBipredIDC, d.lumaWeightDenom, d.chromaWeightDenom)
	}
	if d.lumaWeightL0[0] != 5 || d.lumaOffsetL0[0] != -7 || d.lumaWeightL1[0] != -3 || d.lumaOffsetL1[0] != 9 {
		t.Fatal("luma list weights not copied")
	}
	if d.chromaWeightL0[0] != [2]int32{1, 2} || d.chromaOffsetL0[0] != [2]int32{-4, 6} ||
		d.chromaWeightL1[0] != [2]int32{-5, 7} || d.chromaOffsetL1[0] != [2]int32{8, -9} {
		t.Fatal("chroma list weights not copied")
	}
}
