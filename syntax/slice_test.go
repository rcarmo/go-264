package syntax

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

type testBitWriter struct {
	bits []uint8
}

func (w *testBitWriter) bit(v uint8) {
	w.bits = append(w.bits, v&1)
}

func (w *testBitWriter) ue(v uint32) {
	codeNum := v + 1
	bits := 0
	for tmp := codeNum; tmp > 0; tmp >>= 1 {
		bits++
	}
	for i := 0; i < bits-1; i++ {
		w.bit(0)
	}
	for i := bits - 1; i >= 0; i-- {
		w.bit(uint8((codeNum >> uint(i)) & 1))
	}
}

func (w *testBitWriter) se(v int32) {
	codeNum := uint32(0)
	if v > 0 {
		codeNum = uint32(v*2 - 1)
	} else {
		codeNum = uint32(-v * 2)
	}
	w.ue(codeNum)
}

func (w *testBitWriter) bytes() []byte {
	out := make([]byte, (len(w.bits)+7)/8)
	for i, b := range w.bits {
		if b != 0 {
			out[i/8] |= 1 << uint(7-(i%8))
		}
	}
	return out
}

func TestSkipPredWeightTableConsumesWeightedPredictionSyntax(t *testing.T) {
	var w testBitWriter
	w.ue(0)  // luma_log2_weight_denom
	w.ue(0)  // chroma_log2_weight_denom
	w.bit(1) // luma_weight_l0_flag[0]
	w.se(2)  // luma_weight_l0[0]
	w.se(-1) // luma_offset_l0[0]
	w.bit(1) // chroma_weight_l0_flag[0]
	w.se(1)  // chroma_weight_l0[0][0]
	w.se(0)  // chroma_offset_l0[0][0]
	w.se(-2) // chroma_weight_l0[0][1]
	w.se(3)  // chroma_offset_l0[0][1]
	w.bit(1) // sentinel: next field after pred_weight_table

	r := nal.NewReader(w.bytes())
	h := &Header{SliceType: SliceTypeP, NumRefIdxL0Active: 1}
	sps := &nal.SPS{ChromaFormatIDC: 1}
	skipPredWeightTable(r, h, sps)
	if got := r.ReadBit(); got != 1 {
		t.Fatalf("sentinel after pred_weight_table got %d want 1", got)
	}
}
