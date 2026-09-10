package nal

import (
	"encoding/hex"
	"testing"
)

func TestCheckedAnnexBRejectsMalformedFramingAndHeaders(t *testing.T) {
	for _, data := range [][]byte{
		{0x67, 0x80}, {0, 0, 1}, {9, 0, 0, 1, 0x67, 0x80},
		{0, 0, 1, 0xe7, 0x80}, {0, 0, 1, 0x07, 0x80},
		{0, 0, 1, 0x05, 0x80}, // IDR with nal_ref_idc == 0
		{0, 0, 1, 0x65, 0x80, 0, 0, 1},
	} {
		if _, err := SplitNALUnitsChecked(data); err == nil {
			t.Fatalf("malformed Annex B accepted: %x", data)
		}
	}
	if units, err := SplitNALUnitsChecked([]byte{0, 0, 1, 0x09, 0x10}); err != nil || len(units) != 1 {
		t.Fatalf("valid AUD: %v", err)
	}
}

func TestCheckedAnnexBPreservesApplicationNALs(t *testing.T) {
	// H.264 7.4.1 leaves types 0 and 24..31 to the application. They are
	// opaque to decoding, but still subject to forbidden_zero_bit.
	for _, typ := range []byte{0, 24, 25, 26, 27, 28, 29, 30, 31} {
		for _, refIDC := range []byte{0, 3} {
			data := []byte{0, 0, 1, refIDC<<5 | typ, 0x81}
			units, err := SplitNALUnitsChecked(data)
			if err != nil || len(units) != 1 {
				t.Fatalf("type %d refIDC %d: units=%v err=%v", typ, refIDC, units, err)
			}
			if u := units[0]; u.Type != typ || u.RefIDC != refIDC || len(u.Payload) != 1 || u.Payload[0] != 0x81 {
				t.Fatalf("application NAL changed: %+v", u)
			}
			data[3] |= 0x80
			if _, err := SplitNALUnitsChecked(data); err == nil {
				t.Fatalf("type %d accepted forbidden_zero_bit", typ)
			}
		}
	}
}

func TestSplitNALUnits(t *testing.T) {
	// Minimal Annex B stream: start code + SPS + start code + PPS
	data := []byte{
		0x00, 0x00, 0x00, 0x01, // start code
		0x67, 0x42, 0x00, 0x1e, 0xab, 0x40, 0x50, // SPS (profile=66, level=30)
		0x00, 0x00, 0x00, 0x01, // start code
		0x68, 0xce, 0x38, 0x80, // PPS
	}

	units := SplitNALUnits(data)
	if len(units) != 2 {
		t.Fatalf("got %d NAL units, want 2", len(units))
	}

	if units[0].Type != TypeSPS {
		t.Fatalf("unit[0].Type=%d, want %d (SPS)", units[0].Type, TypeSPS)
	}
	if units[1].Type != TypePPS {
		t.Fatalf("unit[1].Type=%d, want %d (PPS)", units[1].Type, TypePPS)
	}
	t.Logf("NAL[0]: %s refIDC=%d payload=%s", units[0].TypeName(), units[0].RefIDC, hex.EncodeToString(units[0].Payload))
	t.Logf("NAL[1]: %s refIDC=%d payload=%s", units[1].TypeName(), units[1].RefIDC, hex.EncodeToString(units[1].Payload))
}

func TestParseSPS_Baseline(t *testing.T) {
	// Real SPS from ffmpeg libx264 Baseline 320x240 level 3.0
	payload, _ := hex.DecodeString("42c01ed90141fb011000000300100000030320f162e480")
	sps, err := ParseSPS(payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SPS: profile=%d level=%d %dx%d mbs=%dx%d",
		sps.ProfileIDC, sps.LevelIDC, sps.Width, sps.Height,
		sps.PicWidthInMbs, sps.PicHeightInMapUnits)

	if sps.ProfileIDC != 66 {
		t.Errorf("profile=%d want 66", sps.ProfileIDC)
	}
	if sps.LevelIDC != 30 {
		t.Errorf("level=%d want 30", sps.LevelIDC)
	}
	if sps.Width != 320 || sps.Height != 240 {
		t.Errorf("resolution=%dx%d want 320x240", sps.Width, sps.Height)
	}
}

func TestParseSPSOutputBufferingRestrictions(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		vui, restricted         bool
		reorder, frameBuffering uint32
	}{
		{"no VUI", false, false, 0, 0},
		{"VUI without restrictions", true, false, 0, 0},
		{"reordering", true, true, 2, 5},
		{"no reordering", true, true, 0, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w ppsBitWriter
			for _, b := range []byte{66, 0xc0, 31} {
				for bit := 7; bit >= 0; bit-- {
					w.bit(b >> uint(bit))
				}
			}
			w.ue(0)  // seq_parameter_set_id
			w.ue(0)  // log2_max_frame_num_minus4
			w.ue(2)  // pic_order_cnt_type
			w.ue(1)  // max_num_ref_frames
			w.bit(0) // gaps_in_frame_num_value_allowed_flag
			w.ue(79) // 1280x720
			w.ue(44)
			w.bit(1) // frame_mbs_only_flag
			w.bit(1) // direct_8x8_inference_flag
			w.bit(0) // frame_cropping_flag
			if tc.vui {
				w.bit(1) // vui_parameters_present_flag
				for i := 0; i < 7; i++ {
					w.bit(0) // aspect ratio through vcl_hrd_parameters_present_flag
				}
				w.bit(1) // pic_struct_present_flag
				if tc.restricted {
					w.bit(1) // bitstream_restriction_flag
					w.bit(1) // motion_vectors_over_pic_boundaries_flag
					w.ue(2)  // max_bytes_per_pic_denom
					w.ue(1)  // max_bits_per_mb_denom
					w.ue(15) // log2_max_mv_length_horizontal
					w.ue(15) // log2_max_mv_length_vertical
					w.ue(tc.reorder)
					w.ue(tc.frameBuffering)
				} else {
					w.bit(0)
				}
			} else {
				w.bit(0)
			}
			w.rbspTrailingBits()
			sps, err := ParseSPS(w.bytes())
			if err != nil {
				t.Fatal(err)
			}
			if sps.BitstreamRestriction != tc.restricted || sps.MaxNumReorderFrames != tc.reorder || sps.MaxDecFrameBuffering != tc.frameBuffering {
				t.Fatalf("restrictions=%v reorder=%d buffering=%d; want %v/%d/%d", sps.BitstreamRestriction, sps.MaxNumReorderFrames, sps.MaxDecFrameBuffering, tc.restricted, tc.reorder, tc.frameBuffering)
			}
		})
	}
}

func TestParseSPS_High(t *testing.T) {
	// Real SPS from ffmpeg output (High profile, 1920x1080)
	// 67 64 00 28 ac d1 00 78 02 27 e5 c0 44 00 00 03 00 04 00 00 03 00 c8 3c 60 c6 58
	hex_sps := "640028acd100780227e5c04400000300040000030" + "0c83c60c658"
	payload, _ := hex.DecodeString(hex_sps)
	sps, err := ParseSPS(payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SPS High: profile=%d level=%d %dx%d chroma=%d bitdepth=%d/%d",
		sps.ProfileIDC, sps.LevelIDC, sps.Width, sps.Height,
		sps.ChromaFormatIDC, sps.BitDepthLuma, sps.BitDepthChroma)

	if sps.ProfileIDC != 100 {
		t.Errorf("profile=%d want 100 (High)", sps.ProfileIDC)
	}
	if sps.Width != 1920 || sps.Height != 1080 {
		t.Errorf("resolution=%dx%d want 1920x1080", sps.Width, sps.Height)
	}
}

func TestParsePPS(t *testing.T) {
	// Real PPS from ffmpeg libx264 Baseline
	payload, _ := hex.DecodeString("cb83cb20")
	pps, err := ParsePPS(payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PPS: pps_id=%d sps_id=%d entropy=%d qp=%d deblock=%v",
		pps.PPSID, pps.SPSID, pps.EntropyCodingMode, pps.PicInitQP,
		pps.DeblockingFilterControl)
}
