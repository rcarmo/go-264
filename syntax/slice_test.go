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

func TestParseHeaderConsumesMMCO5WithoutOperand(t *testing.T) {
	var w testBitWriter
	w.ue(0)          // first_mb_in_slice
	w.ue(SliceTypeP) // slice_type
	w.ue(0)          // pic_parameter_set_id
	w.bit(0)         // frame_num bit 3
	w.bit(0)         // frame_num bit 2
	w.bit(0)         // frame_num bit 1
	w.bit(0)         // frame_num bit 0
	w.bit(0)         // num_ref_idx_active_override_flag
	w.bit(0)         // ref_pic_list_modification_flag_l0
	w.bit(1)         // adaptive_ref_pic_marking_mode_flag
	w.ue(5)          // MMCO 5: no operands
	w.ue(0)          // end MMCO list
	w.ue(2)          // cabac_init_idc
	w.se(0)          // slice_qp_delta
	h, _ := ParseHeader(w.bytes(), nal.TypeSliceNonIDR, &nal.SPS{Log2MaxFrameNum: 4, PicOrderCntType: 2, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1}, &nal.PPS{EntropyCodingMode: 1, NumRefIdxL0Active: 1})
	if h.CabacInitIDC != 2 {
		t.Fatalf("cabac_init_idc got %d want 2", h.CabacInitIDC)
	}
}

func TestParseHeaderHandlesNilParameterSets(t *testing.T) {
	var w testBitWriter
	w.ue(0)          // first_mb_in_slice
	w.ue(SliceTypeI) // slice_type
	w.ue(0)          // pic_parameter_set_id
	w.se(0)          // slice_qp_delta
	h, r := ParseHeader(w.bytes(), nal.TypeSliceNonIDR, nil, nil)
	if h == nil || r == nil {
		t.Fatalf("ParseHeader returned nil header/reader")
	}
	if h.SliceType != SliceTypeI || h.FirstMbInSlice != 0 || h.PPSID != 0 {
		t.Fatalf("unexpected header: %+v", h)
	}
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
