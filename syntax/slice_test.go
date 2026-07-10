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

func TestSliceGroupChangeCycleBitsHandlesEdges(t *testing.T) {
	if got := sliceGroupChangeCycleBits(&nal.SPS{PicWidthInMbs: 4, PicHeightInMapUnits: 4}, &nal.PPS{SliceGroupChangeRate: 5}); got != 2 {
		t.Fatalf("cycle bits got %d want 2", got)
	}
	if got := sliceGroupChangeCycleBits(&nal.SPS{PicWidthInMbs: 1, PicHeightInMapUnits: 1}, &nal.PPS{SliceGroupChangeRate: 99}); got != 0 {
		t.Fatalf("large change rate bits got %d want 0", got)
	}
	if got := sliceGroupChangeCycleBits(&nal.SPS{PicWidthInMbs: 1 << 31, PicHeightInMapUnits: 2}, &nal.PPS{SliceGroupChangeRate: 1}); got != 0 {
		t.Fatalf("overflowed pic size should clamp to 0 bits, got %d", got)
	}
}

func TestParseHeaderConsumesSliceGroupChangeCycle(t *testing.T) {
	var w testBitWriter
	w.ue(0)                             // first_mb_in_slice
	w.ue(SliceTypeP)                    // slice_type
	w.ue(0)                             // pic_parameter_set_id
	w.bits = append(w.bits, 0, 0, 0, 0) // frame_num
	w.bit(0)                            // num_ref_idx_active_override_flag
	w.bit(0)                            // ref_pic_list_modification_flag_l0
	w.bit(0)                            // adaptive_ref_pic_marking_mode_flag
	w.se(0)                             // slice_qp_delta
	w.ue(1)                             // disable_deblocking_filter_idc
	w.bits = append(w.bits, 1, 0)       // slice_group_change_cycle (2 bits)
	w.bit(1)                            // sentinel after slice_group_change_cycle
	sps := &nal.SPS{Log2MaxFrameNum: 4, PicWidthInMbs: 4, PicHeightInMapUnits: 4, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1}
	pps := &nal.PPS{NumRefIdxL0Active: 1, NumRefIdxL1Active: 1, NumSliceGroups: 2, SliceGroupMapType: 3, SliceGroupChangeRate: 5, DeblockingFilterControl: true}
	_, r := ParseHeaderWithRefIDC(w.bytes(), nal.TypeSliceNonIDR, 1, sps, pps)
	if got := r.ReadBit(); got != 1 {
		t.Fatalf("sentinel after slice_group_change_cycle got %d want 1", got)
	}
}

func TestSkipRefPicListModificationConsumesOperands(t *testing.T) {
	var w testBitWriter
	w.bit(1) // ref_pic_list_modification_flag_l0
	w.ue(0)  // modification_of_pic_nums_idc: short-term subtract
	w.ue(4)  // abs_diff_pic_num_minus1
	w.ue(2)  // long-term pic num
	w.ue(7)  // long_term_pic_num
	w.ue(3)  // end
	w.bit(1) // sentinel
	r := nal.NewReader(w.bytes())
	skipRefPicListModification(r)
	if got := r.ReadBit(); got != 1 {
		t.Fatalf("sentinel after ref_pic_list_modification got %d want 1", got)
	}
}

func TestParseHeaderConsumesPOCType0BottomDelta(t *testing.T) {
	var w testBitWriter
	w.ue(0)                             // first_mb_in_slice
	w.ue(SliceTypeP)                    // slice_type
	w.ue(0)                             // pic_parameter_set_id
	w.bits = append(w.bits, 0, 0, 0, 0) // frame_num
	w.bits = append(w.bits, 1, 0, 1, 0) // pic_order_cnt_lsb
	w.se(-3)                            // delta_pic_order_cnt_bottom
	w.bit(0)                            // num_ref_idx_active_override_flag
	w.bit(0)                            // ref_pic_list_modification_flag_l0
	w.bit(0)                            // adaptive_ref_pic_marking_mode_flag
	w.se(0)                             // slice_qp_delta
	h, _ := ParseHeaderWithRefIDC(w.bytes(), nal.TypeSliceNonIDR, 1, &nal.SPS{Log2MaxFrameNum: 4, PicOrderCntType: 0, Log2MaxPocLsb: 4, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1}, &nal.PPS{BottomFieldPicOrderInFrame: true, NumRefIdxL0Active: 1})
	if h.PicOrderCntLsb != 10 || h.DeltaPicOrderCntBottom != -3 {
		t.Fatalf("POC type 0 fields got lsb=%d bottom=%d", h.PicOrderCntLsb, h.DeltaPicOrderCntBottom)
	}
}

func TestParseHeaderConsumesPOCType1Deltas(t *testing.T) {
	var w testBitWriter
	w.ue(0)                             // first_mb_in_slice
	w.ue(SliceTypeP)                    // slice_type
	w.ue(0)                             // pic_parameter_set_id
	w.bits = append(w.bits, 0, 0, 0, 0) // frame_num
	w.se(2)                             // delta_pic_order_cnt[0]
	w.se(-1)                            // delta_pic_order_cnt[1]
	w.bit(0)                            // num_ref_idx_active_override_flag
	w.bit(0)                            // ref_pic_list_modification_flag_l0
	w.bit(0)                            // adaptive_ref_pic_marking_mode_flag
	w.se(0)                             // slice_qp_delta
	h, _ := ParseHeaderWithRefIDC(w.bytes(), nal.TypeSliceNonIDR, 1, &nal.SPS{Log2MaxFrameNum: 4, PicOrderCntType: 1, DeltaPicOrderAlwaysZero: false, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1}, &nal.PPS{BottomFieldPicOrderInFrame: true, NumRefIdxL0Active: 1})
	if h.DeltaPicOrderCnt != [2]int32{2, -1} {
		t.Fatalf("POC type 1 deltas got %v", h.DeltaPicOrderCnt)
	}
}

func TestParseHeaderConsumesSPSIFieldsBeforeDeblocking(t *testing.T) {
	var w testBitWriter
	w.ue(0)                             // first_mb_in_slice
	w.ue(SliceTypeSP)                   // slice_type
	w.ue(0)                             // pic_parameter_set_id
	w.bits = append(w.bits, 0, 0, 0, 0) // frame_num
	w.bit(0)                            // num_ref_idx_active_override_flag
	w.bit(0)                            // ref_pic_list_modification_flag_l0
	w.bit(0)                            // adaptive_ref_pic_marking_mode_flag
	w.se(1)                             // slice_qp_delta
	w.bit(1)                            // sp_for_switch_flag
	w.se(-2)                            // slice_qs_delta
	w.ue(2)                             // disable_deblocking_filter_idc
	w.se(3)                             // slice_alpha_c0_offset_div2
	w.se(-1)                            // slice_beta_offset_div2
	h, _ := ParseHeaderWithRefIDC(w.bytes(), nal.TypeSliceNonIDR, 1, &nal.SPS{Log2MaxFrameNum: 4, PicOrderCntType: 2, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1}, &nal.PPS{DeblockingFilterControl: true, NumRefIdxL0Active: 1})
	if h.SliceQPDelta != 1 || !h.SPForSwitchFlag || h.SliceQSDelta != -2 {
		t.Fatalf("SP fields got qp=%d switch=%v qs=%d", h.SliceQPDelta, h.SPForSwitchFlag, h.SliceQSDelta)
	}
	if h.DisableDeblocking != 2 || h.SliceAlphaC0Offset != 6 || h.SliceBetaOffset != -2 {
		t.Fatalf("deblock fields got idc=%d alpha=%d beta=%d", h.DisableDeblocking, h.SliceAlphaC0Offset, h.SliceBetaOffset)
	}
}

func TestParseHeaderSkipsRefMarkingForNonReferenceSlices(t *testing.T) {
	var w testBitWriter
	w.ue(0)                             // first_mb_in_slice
	w.ue(SliceTypeP)                    // slice_type
	w.ue(0)                             // pic_parameter_set_id
	w.bits = append(w.bits, 0, 0, 0, 0) // frame_num
	w.bit(0)                            // num_ref_idx_active_override_flag
	w.bit(0)                            // ref_pic_list_modification_flag_l0
	// No dec_ref_pic_marking is present when nal_ref_idc == 0.
	w.ue(2) // cabac_init_idc
	w.se(0) // slice_qp_delta
	h, _ := ParseHeaderWithRefIDC(w.bytes(), nal.TypeSliceNonIDR, 0, &nal.SPS{Log2MaxFrameNum: 4, PicOrderCntType: 2, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1}, &nal.PPS{EntropyCodingMode: 1, NumRefIdxL0Active: 1})
	if h.CabacInitIDC != 2 {
		t.Fatalf("cabac_init_idc got %d want 2", h.CabacInitIDC)
	}
}

func TestParseHeaderReadsDeblockingIDCAsUE(t *testing.T) {
	var w testBitWriter
	w.ue(0)                             // first_mb_in_slice
	w.ue(SliceTypeI)                    // slice_type
	w.ue(0)                             // pic_parameter_set_id
	w.bits = append(w.bits, 0, 0, 0, 0) // frame_num
	w.bit(0)                            // adaptive_ref_pic_marking_mode_flag
	w.se(0)                             // slice_qp_delta
	w.ue(2)                             // disable_deblocking_filter_idc, ue(v) not se(v)
	w.se(3)                             // slice_alpha_c0_offset_div2
	w.se(-2)                            // slice_beta_offset_div2
	h, _ := ParseHeader(w.bytes(), nal.TypeSliceNonIDR, &nal.SPS{Log2MaxFrameNum: 4, PicOrderCntType: 2, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1}, &nal.PPS{DeblockingFilterControl: true})
	if h.DisableDeblocking != 2 || h.SliceAlphaC0Offset != 6 || h.SliceBetaOffset != -4 {
		t.Fatalf("deblock fields got idc=%d alpha=%d beta=%d", h.DisableDeblocking, h.SliceAlphaC0Offset, h.SliceBetaOffset)
	}
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
	if h.LumaWeightL0[0] != 2 || h.LumaOffsetL0[0] != -1 {
		t.Fatalf("luma weights got %d/%d want 2/-1", h.LumaWeightL0[0], h.LumaOffsetL0[0])
	}
	if h.ChromaWeightL0[0] != [2]int32{1, -2} || h.ChromaOffsetL0[0] != [2]int32{0, 3} {
		t.Fatalf("chroma weights/offsets got %v/%v", h.ChromaWeightL0[0], h.ChromaOffsetL0[0])
	}
	if got := r.ReadBit(); got != 1 {
		t.Fatalf("sentinel after pred_weight_table got %d want 1", got)
	}
}
