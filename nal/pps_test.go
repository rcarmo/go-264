package nal

import (
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

func TestParameterSetsRejectTruncation(t *testing.T) {
	cases := []struct {
		name, hex string
		parse     func([]byte) error
	}{
		{"SPS", "42c01ed90141fb011000000300100000030320f162e480", func(b []byte) error { _, err := ParseSPS(b); return err }},
		{"PPS", "cb83cb20", func(b []byte) error { _, err := ParsePPS(b); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.parse(data); err != nil {
				t.Fatalf("valid parameter set: %v", err)
			}
			for n := 0; n < len(data); n++ {
				if err := tc.parse(data[:n]); err == nil {
					t.Fatalf("accepted prefix %d/%d", n, len(data))
				}
			}
		})
	}
}

func validationSPSPayload(s SPS) []byte {
	return validationSPSPayloadWithVUI(s, nil)
}

func validationSPSPayloadWithVUI(s SPS, writeVUI func(*ppsBitWriter)) []byte {
	var w ppsBitWriter
	for _, b := range []byte{66, 0, 10} {
		for bit := 7; bit >= 0; bit-- {
			w.bit(b >> uint(bit))
		}
	}
	w.ue(s.SPSID)
	w.ue(s.Log2MaxFrameNum - 4)
	w.ue(s.PicOrderCntType)
	if s.PicOrderCntType == 0 {
		w.ue(s.Log2MaxPocLsb - 4)
	}
	w.ue(s.MaxNumRefFrames)
	if s.GapsInFrameNumValueAllowedFlag {
		w.bit(1)
	} else {
		w.bit(0)
	}
	w.ue(s.PicWidthInMbs - 1)
	w.ue(s.PicHeightInMapUnits - 1)
	w.bit(1)
	w.bit(1)
	if s.FrameCropping {
		w.bit(1)
		w.ue(s.CropLeft)
		w.ue(s.CropRight)
		w.ue(s.CropTop)
		w.ue(s.CropBottom)
	} else {
		w.bit(0)
	}
	if writeVUI == nil {
		w.bit(0) // no VUI
	} else {
		w.bit(1)
		writeVUI(&w)
	}
	w.rbspTrailingBits()
	return w.bytes()
}

func TestSPSIgnoresReservedConstraintBits(t *testing.T) {
	payload, err := hex.DecodeString("42c01ed90141fb011000000300100000030320f162e480")
	if err != nil {
		t.Fatal(err)
	}
	want, err := ParseSPS(payload)
	if err != nil {
		t.Fatal(err)
	}
	for reserved := uint8(1); reserved <= 3; reserved++ {
		payload[1] = want.ConstraintFlags | reserved
		got, err := ParseSPS(payload)
		if err != nil {
			t.Fatalf("reserved_zero_2bits=%d: %v", reserved, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reserved_zero_2bits=%d changed parsed SPS: got %+v, want %+v", reserved, got, want)
		}
	}
}

func TestSPSVUIMotionVectorBounds(t *testing.T) {
	base := SPS{Log2MaxFrameNum: 4, PicOrderCntType: 0, Log2MaxPocLsb: 4, MaxNumRefFrames: 1, PicWidthInMbs: 1, PicHeightInMapUnits: 1}
	for _, tc := range []struct {
		name                 string
		horizontal, vertical uint32
		present, invalid     bool
	}{
		{"omitted", 0, 0, false, false},
		{"zero", 0, 0, true, false},
		{"maximum", 15, 15, true, false},
		{"horizontal_overflow", 16, 15, true, true},
		{"vertical_overflow", 15, 16, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := validationSPSPayloadWithVUI(base, func(w *ppsBitWriter) {
				for i := 0; i < 8; i++ {
					w.bit(0) // optional VUI fields through pic_struct_present_flag
				}
				if !tc.present {
					w.bit(0) // no bitstream restrictions; MV limits are inferred
					return
				}
				w.bit(1) // bitstream_restriction_flag
				w.bit(1) // motion_vectors_over_pic_boundaries_flag
				w.ue(0)  // max_bytes_per_pic_denom
				w.ue(0)  // max_bits_per_mb_denom
				w.ue(tc.horizontal)
				w.ue(tc.vertical)
				w.ue(0) // max_num_reorder_frames
				w.ue(1) // max_dec_frame_buffering
			})
			_, err := ParseSPS(payload)
			if tc.invalid {
				if !errors.Is(err, ErrInvalidSyntax) {
					t.Fatalf("got %v, want invalid syntax", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSPSHRDValueBounds(t *testing.T) {
	base := SPS{Log2MaxFrameNum: 4, PicOrderCntType: 0, Log2MaxPocLsb: 4, MaxNumRefFrames: 1, PicWidthInMbs: 1, PicHeightInMapUnits: 1}
	for _, tc := range []struct {
		name             string
		bitRate, cpbSize uint32
		invalid          bool
	}{
		{"zero", 0, 0, false},
		{"maximum", 1<<32 - 2, 1<<32 - 2, false},
		{"bit_rate_overflow", 1<<32 - 1, 0, true},
		{"cpb_size_overflow", 0, 1<<32 - 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := validationSPSPayloadWithVUI(base, func(w *ppsBitWriter) {
				for i := 0; i < 5; i++ {
					w.bit(0) // optional VUI fields before NAL HRD
				}
				w.bit(1) // nal_hrd_parameters_present_flag
				w.ue(0)  // cpb_cnt_minus1
				for i := 0; i < 8; i++ {
					w.bit(0) // bit_rate_scale and cpb_size_scale
				}
				w.ue(tc.bitRate)
				w.ue(tc.cpbSize)
				w.bit(0) // cbr_flag
				for i := 0; i < 20; i++ {
					w.bit(0) // HRD delay field lengths
				}
				w.bit(0) // vcl_hrd_parameters_present_flag
				w.bit(0) // low_delay_hrd_flag
				w.bit(0) // pic_struct_present_flag
				w.bit(0) // bitstream_restriction_flag
			})
			_, err := ParseSPS(payload)
			if tc.invalid {
				if !errors.Is(err, ErrInvalidSyntax) {
					t.Fatalf("got %v, want invalid syntax", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSPSRejectsOutOfRangeFields(t *testing.T) {
	base := SPS{SPSID: 0, Log2MaxFrameNum: 4, PicOrderCntType: 0, Log2MaxPocLsb: 4, MaxNumRefFrames: 1, PicWidthInMbs: 1, PicHeightInMapUnits: 1}
	for _, tc := range []struct {
		name string
		edit func(*SPS)
	}{
		{"id", func(s *SPS) { s.SPSID = 32 }},
		{"frame_num_bits", func(s *SPS) { s.Log2MaxFrameNum = 17 }},
		{"poc_type", func(s *SPS) { s.PicOrderCntType = 3 }},
		{"poc_bits", func(s *SPS) { s.Log2MaxPocLsb = 17 }},
		{"refs", func(s *SPS) { s.MaxNumRefFrames = 17 }},
		{"width", func(s *SPS) { s.PicWidthInMbs = maxSyntaxMacroblocks + 1 }},
		{"area", func(s *SPS) { s.PicWidthInMbs = 2048; s.PicHeightInMapUnits = 2048 }},
		{"crop", func(s *SPS) { s.FrameCropping = true; s.CropRight = 8 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.edit(&s)
			if _, err := ParseSPS(validationSPSPayload(s)); !errors.Is(err, ErrInvalidSyntax) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := ParseSPS(validationSPSPayload(base)); err != nil {
		t.Fatal(err)
	}
}

func TestSPSFrameNumGapFlag(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		s := SPS{Log2MaxFrameNum: 5, PicOrderCntType: 0, Log2MaxPocLsb: 4,
			MaxNumRefFrames: 4, PicWidthInMbs: 1, PicHeightInMapUnits: 1,
			GapsInFrameNumValueAllowedFlag: allowed}
		got, err := ParseSPS(validationSPSPayload(s))
		if err != nil {
			t.Fatal(err)
		}
		if got.GapsInFrameNumValueAllowedFlag != allowed {
			t.Fatalf("gap flag = %v, want %v", got.GapsInFrameNumValueAllowedFlag, allowed)
		}
	}
}

func validationPPSPayload(p PPS) []byte {
	var w ppsBitWriter
	w.ue(p.PPSID)
	w.ue(p.SPSID)
	w.bit(0)
	w.bit(0)
	w.ue(p.NumSliceGroups - 1)
	w.ue(p.NumRefIdxL0Active - 1)
	w.ue(p.NumRefIdxL1Active - 1)
	w.bit(0)
	w.bit(uint8(p.WeightedBipredIDC >> 1))
	w.bit(uint8(p.WeightedBipredIDC))
	w.se(p.PicInitQP - 26)
	w.se(p.PicInitQS - 26)
	w.se(p.ChromaQPIndexOffset)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.bit(0)
	w.se(p.SecondChromaQPIndexOffset)
	w.rbspTrailingBits()
	return w.bytes()
}

func TestPPSRejectsOutOfRangeFields(t *testing.T) {
	base := PPS{NumSliceGroups: 1, NumRefIdxL0Active: 1, NumRefIdxL1Active: 1, PicInitQP: 26, PicInitQS: 26}
	for _, tc := range []struct {
		name string
		edit func(*PPS)
	}{
		{"id", func(p *PPS) { p.PPSID = 256 }},
		{"sps_id", func(p *PPS) { p.SPSID = 32 }},
		{"groups", func(p *PPS) { p.NumSliceGroups = 9 }},
		{"refs", func(p *PPS) { p.NumRefIdxL0Active = 33 }},
		{"weighted_bipred", func(p *PPS) { p.WeightedBipredIDC = 3 }},
		{"qp_high", func(p *PPS) { p.PicInitQP = 52 }},
		{"qp_low", func(p *PPS) { p.PicInitQP = -37 }},
		{"qs", func(p *PPS) { p.PicInitQS = 52 }},
		{"chroma_qp", func(p *PPS) { p.ChromaQPIndexOffset = 13 }},
		{"second_chroma_qp", func(p *PPS) { p.SecondChromaQPIndexOffset = -13 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.edit(&p)
			if _, err := ParsePPS(validationPPSPayload(p)); !errors.Is(err, ErrInvalidSyntax) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := ParsePPS(validationPPSPayload(base)); err != nil {
		t.Fatal(err)
	}
}

func TestParameterDrivenLoopBounds(t *testing.T) {
	var w ppsBitWriter
	w.ue(0)
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.ue(1)
	w.ue(6)
	w.ue(maxSyntaxMacroblocks) // pic_size_in_map_units_minus1 is over the work budget
	if _, err := ParsePPS(w.bytes()); !errors.Is(err, ErrInvalidSyntax) {
		t.Fatalf("map size: %v", err)
	}
	w = ppsBitWriter{}
	w.ue(32)
	r := NewReader(w.bytes())
	parseHRD(r)
	if !errors.Is(r.Err(), ErrInvalidSyntax) {
		t.Fatalf("HRD cpb count: %v", r.Err())
	}
}

type ppsBitWriter struct {
	bits []uint8
}

func (w *ppsBitWriter) bit(v uint8) { w.bits = append(w.bits, v&1) }

func (w *ppsBitWriter) ue(v uint32) {
	codeNum := uint64(v) + 1
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

func TestDeriveSPSDimensionsClampsMalformedCropping(t *testing.T) {
	s := &SPS{PicWidthInMbs: 1, PicHeightInMapUnits: 1, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1, FrameCropping: true, CropLeft: 100, CropRight: 100, CropTop: 100, CropBottom: 100}
	deriveSPSDimensions(s)
	if s.Width != 0 || s.Height != 0 {
		t.Fatalf("malformed crop dimensions got %dx%d want 0x0", s.Width, s.Height)
	}
	s = &SPS{PicWidthInMbs: 2, PicHeightInMapUnits: 2, FrameMbsOnlyFlag: true, ChromaFormatIDC: 1, FrameCropping: true, CropLeft: 1, CropRight: 1, CropTop: 1, CropBottom: 1}
	deriveSPSDimensions(s)
	if s.Width != 28 || s.Height != 28 {
		t.Fatalf("valid crop dimensions got %dx%d want 28x28", s.Width, s.Height)
	}
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

func TestParsePPSStoresChangingSliceGroupParameters(t *testing.T) {
	var w ppsBitWriter
	w.ue(0)  // pic_parameter_set_id
	w.ue(0)  // seq_parameter_set_id
	w.bit(0) // entropy_coding_mode_flag
	w.bit(0) // bottom_field_pic_order_in_frame_present_flag
	w.ue(1)  // num_slice_groups_minus1 -> two groups
	w.ue(3)  // slice_group_map_type
	w.bit(1) // slice_group_change_direction_flag
	w.ue(4)  // slice_group_change_rate_minus1 -> 5
	w.ue(0)  // num_ref_idx_l0_default_active_minus1
	w.ue(0)  // num_ref_idx_l1_default_active_minus1
	w.bit(0) // weighted_pred_flag
	w.bit(0)
	w.bit(0) // weighted_bipred_idc
	w.se(0)
	w.se(0)
	w.se(0)
	w.bit(0) // deblocking_filter_control_present_flag
	w.bit(0) // constrained_intra_pred_flag
	w.bit(0) // redundant_pic_cnt_present_flag
	w.rbspTrailingBits()
	pps, err := ParsePPS(w.bytes())
	if err != nil {
		t.Fatal(err)
	}
	if pps.SliceGroupMapType != 3 || pps.SliceGroupChangeRate != 5 {
		t.Fatalf("slice group params got type=%d rate=%d want 3/5", pps.SliceGroupMapType, pps.SliceGroupChangeRate)
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
