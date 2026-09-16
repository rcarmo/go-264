package decode

import (
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func (d *Decoder) loadPredictionWeights(hdr *syntax.Header, pps *nal.PPS) {
	if d == nil || hdr == nil || pps == nil {
		return
	}
	d.weightedPred = pps.WeightedPred && (hdr.SliceType == syntax.SliceTypeP || hdr.SliceType == syntax.SliceTypeSP) && hdr.WeightedTablePresent
	d.weightedBipredIDC = pps.WeightedBipredIDC
	d.lumaWeightDenom = hdr.LumaLog2WeightDenom
	d.lumaWeightL0 = hdr.LumaWeightL0
	d.lumaOffsetL0 = hdr.LumaOffsetL0
	d.lumaWeightL1 = hdr.LumaWeightL1
	d.lumaOffsetL1 = hdr.LumaOffsetL1
	d.chromaWeightDenom = hdr.ChromaLog2WeightDenom
	d.chromaWeightL0 = hdr.ChromaWeightL0
	d.chromaOffsetL0 = hdr.ChromaOffsetL0
	d.chromaWeightL1 = hdr.ChromaWeightL1
	d.chromaOffsetL1 = hdr.ChromaOffsetL1
}
