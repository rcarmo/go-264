package nal

import "testing"

type ppsBitWriter struct {
	bits []uint8
}

func (w *ppsBitWriter) bit(v uint8) { w.bits = append(w.bits, v&1) }

func (w *ppsBitWriter) ue(v uint32) {
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

func (w *ppsBitWriter) se(v int32) {
	if v > 0 {
		w.ue(uint32(v*2 - 1))
		return
	}
	w.ue(uint32(-v * 2))
}

func (w *ppsBitWriter) rbspTrailingBits() {
	w.bit(1)
	for len(w.bits)%8 != 0 {
		w.bit(0)
	}
}

func (w *ppsBitWriter) bytes() []byte {
	out := make([]byte, (len(w.bits)+7)/8)
	for i, b := range w.bits {
		if b != 0 {
			out[i/8] |= 1 << uint(7-(i%8))
		}
	}
	return out
}

func TestWrapScale256NormalizesArbitraryDeltas(t *testing.T) {
	cases := []struct {
		in, want int32
	}{
		{8, 8},
		{264, 8},
		{-1, 255},
		{-300, 212},
	}
	for _, tc := range cases {
		if got := wrapScale256(tc.in); got != tc.want {
			t.Fatalf("wrapScale256(%d) got %d want %d", tc.in, got, tc.want)
		}
	}
}

func TestParsePPSSkipsSliceGroupMapAndContinues(t *testing.T) {
	var w ppsBitWriter
	w.ue(0)  // pic_parameter_set_id
	w.ue(0)  // seq_parameter_set_id
	w.bit(1) // entropy_coding_mode_flag
	w.bit(0) // bottom_field_pic_order_in_frame_present_flag
	w.ue(1)  // num_slice_groups_minus1 -> two groups
	w.ue(0)  // slice_group_map_type
	w.ue(2)  // run_length_minus1[0]
	w.ue(3)  // run_length_minus1[1]
	w.ue(4)  // num_ref_idx_l0_default_active_minus1 -> 5
	w.ue(1)  // num_ref_idx_l1_default_active_minus1 -> 2
	w.bit(1) // weighted_pred_flag
	w.bit(1) // weighted_bipred_idc bit 1
	w.bit(0) // weighted_bipred_idc bit 0 -> 2
	w.se(2)  // pic_init_qp_minus26 -> 28
	w.se(0)  // pic_init_qs_minus26
	w.se(-1) // chroma_qp_index_offset
	w.bit(1) // deblocking_filter_control_present_flag
	w.bit(0) // constrained_intra_pred_flag
	w.bit(1) // redundant_pic_cnt_present_flag
	w.rbspTrailingBits()

	pps, err := ParsePPS(w.bytes())
	if err != nil {
		t.Fatal(err)
	}
	if pps.NumSliceGroups != 2 || pps.NumRefIdxL0Active != 5 || pps.NumRefIdxL1Active != 2 {
		t.Fatalf("parsed groups/refs = groups %d L0 %d L1 %d", pps.NumSliceGroups, pps.NumRefIdxL0Active, pps.NumRefIdxL1Active)
	}
	if pps.EntropyCodingMode != 1 || !pps.WeightedPred || pps.WeightedBipredIDC != 2 || pps.PicInitQP != 28 || pps.ChromaQPIndexOffset != -1 || !pps.DeblockingFilterControl || !pps.RedundantPicCntPresent {
		t.Fatalf("PPS fields after slice groups not parsed correctly: %+v", pps)
	}
}
