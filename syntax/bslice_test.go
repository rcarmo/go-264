package syntax

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

func TestBiPredBlend(t *testing.T) {
	l0 := []uint8{100, 200, 50, 0}
	l1 := []uint8{200, 100, 50, 255}
	out := make([]uint8, 4)
	BiPredBlend(out, l0, l1, 4)

	want := []uint8{150, 150, 50, 128}
	for i, v := range out {
		if v != want[i] {
			t.Errorf("out[%d]=%d want %d", i, v, want[i])
		}
	}
}

func TestBiPredBlendClipsMalformedInputs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BiPredBlend panicked on malformed inputs: %v", r)
		}
	}()
	out := []uint8{0, 0, 99}
	BiPredBlend(out, []uint8{10, 20}, []uint8{30}, 10)
	if out[0] != 20 || out[1] != 0 || out[2] != 99 {
		t.Fatalf("clipped blend wrote unexpected output: %v", out)
	}
	BiPredBlend(nil, []uint8{1}, []uint8{2}, 1)
	BiPredBlend(out, nil, []uint8{2}, 1)
	BiPredBlend(out, []uint8{1}, nil, 1)
	BiPredBlend(out, []uint8{1}, []uint8{2}, -1)
}

func TestUsesL0L1(t *testing.T) {
	// L0 16x16: uses L0 only
	if !usesL0(BMBTypeL016x16, 0) {
		t.Error("L016x16 should use L0")
	}
	if usesL1(BMBTypeL016x16, 0) {
		t.Error("L016x16 should not use L1")
	}

	// L1 16x16: uses L1 only
	if usesL0(BMBTypeL116x16, 0) {
		t.Error("L116x16 should not use L0")
	}
	if !usesL1(BMBTypeL116x16, 0) {
		t.Error("L116x16 should use L1")
	}

	// Bi 16x16: uses both
	if !usesL0(BMBTypeBi16x16, 0) {
		t.Error("Bi16x16 should use L0")
	}
	if !usesL1(BMBTypeBi16x16, 0) {
		t.Error("Bi16x16 should use L1")
	}
}

func TestUsesL0L1MatchesFFmpegBMBTypeTable(t *testing.T) {
	cases := []struct {
		mbType uint32
		part   int
		wantL0 bool
		wantL1 bool
	}{
		{4, 0, true, false}, {4, 1, true, false}, // L0/L0 16x8
		{6, 0, false, true}, {6, 1, false, true}, // L1/L1 16x8
		{8, 0, true, false}, {8, 1, false, true}, // L0/L1 16x8
		{10, 0, false, true}, {10, 1, true, false}, // L1/L0 16x8
		{12, 0, true, false}, {12, 1, true, true}, // L0/Bi 16x8
		{14, 0, false, true}, {14, 1, true, true}, // L1/Bi 16x8
		{16, 0, true, true}, {16, 1, true, false}, // Bi/L0 16x8
		{18, 0, true, true}, {18, 1, false, true}, // Bi/L1 16x8
		{20, 0, true, true}, {20, 1, true, true}, // Bi/Bi 16x8
	}
	for _, c := range cases {
		if got := usesL0(c.mbType, c.part); got != c.wantL0 {
			t.Fatalf("usesL0(type=%d, part=%d) got %v want %v", c.mbType, c.part, got, c.wantL0)
		}
		if got := usesL1(c.mbType, c.part); got != c.wantL1 {
			t.Fatalf("usesL1(type=%d, part=%d) got %v want %v", c.mbType, c.part, got, c.wantL1)
		}
	}
}

func TestUsesL0L1RejectsInvalidInputs(t *testing.T) {
	if usesL0(99, 0) || usesL1(99, 0) || usesL0(1, -1) || usesL1(1, 2) {
		t.Fatal("invalid B-slice list-use query returned true")
	}
}

func TestBSubMBListUseMatchesFFmpegTable(t *testing.T) {
	cases := []struct {
		subType uint32
		wantL0  bool
		wantL1  bool
	}{
		{0, false, false}, // direct
		{1, true, false}, {2, false, true}, {3, true, true},
		{4, true, false}, {5, true, false},
		{6, false, true}, {7, false, true},
		{8, true, true}, {9, true, true},
		{10, true, false}, {11, false, true}, {12, true, true},
		{13, false, false},
	}
	for _, c := range cases {
		if got := usesBSubL0(c.subType); got != c.wantL0 {
			t.Fatalf("usesBSubL0(%d) got %v want %v", c.subType, got, c.wantL0)
		}
		if got := usesBSubL1(c.subType); got != c.wantL1 {
			t.Fatalf("usesBSubL1(%d) got %v want %v", c.subType, got, c.wantL1)
		}
	}
}

func TestBSubMBPartCountsMatchFFmpegTable(t *testing.T) {
	want := []int{1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 4, 4, 4, 0}
	for subType, count := range want {
		if got := bSubMBPartCountForType(uint32(subType)); got != count {
			t.Fatalf("bSubMBPartCountForType(%d) got %d want %d", subType, got, count)
		}
	}
}

func TestDecodeMBBidiB8x8UsesSubMBListUse(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeB8x8)
	w.ue(0) // direct: no refs or MVD
	w.ue(1) // L0: L0 ref/MVD only
	w.ue(2) // L1: L1 ref/MVD only
	w.ue(3) // Bi: both lists
	w.ue(1) // L0 ref for part 1
	w.ue(2) // L0 ref for part 3
	w.ue(3) // L1 ref for part 2
	w.ue(4) // L1 ref for part 3
	w.se(5)
	w.se(-6) // L0 MVD part 1
	w.se(7)
	w.se(-8) // L0 MVD part 3
	w.se(9)
	w.se(-10) // L1 MVD part 2
	w.se(11)
	w.se(-12) // L1 MVD part 3
	w.ue(0)   // CBP=0

	mb := DecodeMBBidi(nal.NewReader(w.bytes()), 26, 5, 5)
	if mb.RefIdxL0[0] != 0 || mb.MVL0[0] != (MotionVector{}) || mb.RefIdxL1[0] != 0 || mb.MVL1[0] != (MotionVector{}) {
		t.Fatal("direct B sub-MB consumed explicit list syntax")
	}
	if mb.RefIdxL0[1] != 1 || mb.MVL0[1] != (MotionVector{X: 5, Y: -6}) || mb.RefIdxL1[1] != 0 || mb.MVL1[1] != (MotionVector{}) {
		t.Fatalf("L0 sub-MB decoded as %+v", mb)
	}
	if mb.RefIdxL1[2] != 3 || mb.MVL1[2] != (MotionVector{X: 9, Y: -10}) || mb.RefIdxL0[2] != 0 || mb.MVL0[2] != (MotionVector{}) {
		t.Fatalf("L1 sub-MB decoded as %+v", mb)
	}
	if mb.RefIdxL0[3] != 2 || mb.RefIdxL1[3] != 4 || mb.MVL0[3] != (MotionVector{X: 7, Y: -8}) || mb.MVL1[3] != (MotionVector{X: 11, Y: -12}) {
		t.Fatalf("Bi sub-MB decoded as %+v", mb)
	}
}

func TestDecodeMBBidiWithOptsPassesTransform8x8ToIntraPayload(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeIntra) // B-slice I_NxN
	for i := 0; i < 16; i++ {
		w.bit(1) // current intra helper still consumes I4x4 prediction flags before the transform flag
	}
	w.ue(0)  // chroma intra pred mode
	w.ue(4)  // intra CBP table code 4 => cbp=23, luma coded
	w.bit(1) // transform_size_8x8_flag
	w.se(0)
	for i := 0; i < 16; i++ {
		w.bit(1) // zero-coeff AC blocks consumed by intra residual path
	}
	for comp := 0; comp < 2; comp++ {
		w.bit(1) // zero chroma DC
	}

	mb := DecodeMBBidiWithOpts(nal.NewReader(w.bytes()), BidiDecodeOpts{Transform8x8: true})
	if mb.Intra == nil || !mb.Intra.Use8x8Transform {
		t.Fatalf("B-intra did not receive transform8x8 option: %+v", mb.Intra)
	}
}

func TestDecodeMBBidiConsumesIntraPayload(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeIntra) // B-slice I_NxN
	for i := 0; i < 16; i++ {
		w.bit(1) // prev_intra_pred_mode_flag
	}
	w.ue(0) // chroma intra pred mode
	w.ue(3) // intra CBP table code 3 => cbp=0

	mb := DecodeMBBidi(nal.NewReader(w.bytes()), 26, 1, 1)
	if mb.Intra == nil || mb.Intra.MBType != 0 || mb.Intra.ChromaPredMode != 0 || mb.Intra.CodedBlockPattern != 0 {
		t.Fatalf("B intra payload not consumed: %+v", mb.Intra)
	}
	for i, mode := range mb.Intra.IntraPredMode {
		if mode != -1 {
			t.Fatalf("intra pred mode %d got %d want -1", i, mode)
		}
	}
}

func TestDecodeMBBidiConsumesTransform8x8Flag(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeL016x16)
	w.se(0)
	w.se(0)
	w.ue(2)  // inter CBP table code 2 => cbp=1, luma coded
	w.bit(1) // transform_size_8x8_flag
	w.se(0)
	for i := 0; i < 4; i++ {
		w.bit(1) // zero coeff_token for covered 4x4 residuals
	}
	mb := DecodeMBBidiWithOpts(nal.NewReader(w.bytes()), BidiDecodeOpts{Transform8x8: true})
	if !mb.Use8x8Transform || mb.CBP != 1 || mb.QPDelta != 0 {
		t.Fatalf("B transform8x8 flag not consumed: use=%v cbp=%d qpd=%d", mb.Use8x8Transform, mb.CBP, mb.QPDelta)
	}
}

func TestDecodeMBBidiConsumesLumaResidualSyntax(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeL016x16)
	w.se(0)
	w.se(0)
	w.ue(2) // inter CBP table code 2 => cbp=1, four luma 4x4 blocks follow
	w.se(0)
	for i := 0; i < 4; i++ {
		w.bit(1) // nC=0 coeff_token (0 coeffs, 0 trailing ones)
	}

	mb := DecodeMBBidi(nal.NewReader(w.bytes()), 26, 1, 1)
	if mb.CBP != 1 || mb.QPDelta != 0 || mb.TotalCoeff[0] != 0 || mb.TotalCoeff[3] != 0 {
		t.Fatalf("B residual syntax was not consumed/stored correctly: cbp=%d qpd=%d tc=%v", mb.CBP, mb.QPDelta, mb.TotalCoeff)
	}
}

func TestDecodeMBBidiDirectConsumesCBPAndQPDelta(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeDirect16x16)
	w.ue(1) // inter CBP table code 1 => cbp=16, so mb_qp_delta follows
	w.se(-2)

	mb := DecodeMBBidi(nal.NewReader(w.bytes()), 26, 1, 1)
	if mb.MBType != BMBTypeDirect16x16 || mb.CBP != 16 || mb.QPDelta != -2 {
		t.Fatalf("direct B MB did not consume CBP/QP syntax: type=%d cbp=%d qpd=%d", mb.MBType, mb.CBP, mb.QPDelta)
	}
}

func TestDecodeMBBidiUsesTruncatedExpGolombRefs(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeL016x16)
	w.bit(0) // TE(1) ref_idx=1; plain UE would consume following MVD bits too
	w.se(0)
	w.se(0)
	w.ue(0) // CBP=0

	mb := DecodeMBBidi(nal.NewReader(w.bytes()), 26, 2, 1)
	if mb.RefIdxL0[0] != 1 || mb.MVL0[0] != (MotionVector{}) || mb.CBP != 0 || mb.QPDelta != 0 {
		t.Fatalf("B ref_idx TE decode drifted: ref=%v mv=%v cbp=%d qpd=%d", mb.RefIdxL0, mb.MVL0, mb.CBP, mb.QPDelta)
	}
}

func TestDecodeMBBidiB8x8ConsumesAllSubPartitionMVDs(t *testing.T) {
	var w testBitWriter
	w.ue(BMBTypeB8x8)
	for i := 0; i < 4; i++ {
		w.ue(10) // B_L0_8x8: four L0 sub-partitions in FFmpeg's table
	}
	for i := int32(1); i <= 16; i++ {
		w.se(i)
		w.se(-i)
	}
	w.ue(0) // CBP=0; if extra MVDs are not consumed this is read from the wrong bit position

	mb := DecodeMBBidi(nal.NewReader(w.bytes()), 26, 1, 1)
	want := [4]MotionVector{{X: 1, Y: -1}, {X: 5, Y: -5}, {X: 9, Y: -9}, {X: 13, Y: -13}}
	if mb.MVL0 != want || mb.CBP != 0 || mb.QPDelta != 0 {
		t.Fatalf("B_8x8 sub-partition MVD consumption drifted: mv=%v cbp=%d qpd=%d", mb.MVL0, mb.CBP, mb.QPDelta)
	}
}
