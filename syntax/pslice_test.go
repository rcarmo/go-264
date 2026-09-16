package syntax

import (
	"errors"
	"io"
	"testing"

	cavlc "github.com/rcarmo/go-264/entropy/cavlc"
	"github.com/rcarmo/go-264/nal"
)

func TestMedian3(t *testing.T) {
	tests := []struct{ a, b, c, want int16 }{
		{1, 2, 3, 2},
		{3, 1, 2, 2},
		{5, 5, 5, 5},
		{-1, 0, 1, 0},
		{10, -10, 0, 0},
	}
	for _, tt := range tests {
		got := median3(tt.a, tt.b, tt.c)
		if got != tt.want {
			t.Errorf("median3(%d,%d,%d)=%d want %d", tt.a, tt.b, tt.c, got, tt.want)
		}
	}
}

func TestPredictMV(t *testing.T) {
	// No neighbors available → (0,0)
	mv := PredictMV(MotionVector{}, MotionVector{}, MotionVector{}, false, false, false)
	if mv.X != 0 || mv.Y != 0 {
		t.Errorf("no neighbors: got (%d,%d) want (0,0)", mv.X, mv.Y)
	}

	// Only A available → use A
	a := MotionVector{4, 8}
	mv = PredictMV(a, MotionVector{}, MotionVector{}, true, false, false)
	if mv.X != 4 || mv.Y != 8 {
		t.Errorf("only A: got (%d,%d) want (4,8)", mv.X, mv.Y)
	}

	// All three → median
	b := MotionVector{2, 6}
	c := MotionVector{8, 2}
	mv = PredictMV(a, b, c, true, true, true)
	// median(4,2,8)=4, median(8,6,2)=6
	if mv.X != 4 || mv.Y != 6 {
		t.Errorf("median: got (%d,%d) want (4,6)", mv.X, mv.Y)
	}
}

func TestSubMBPartCount(t *testing.T) {
	if subMBPartCount(0) != 1 {
		t.Error("8x8 should be 1")
	}
	if subMBPartCount(1) != 2 {
		t.Error("8x4 should be 2")
	}
	if subMBPartCount(2) != 2 {
		t.Error("4x8 should be 2")
	}
	if subMBPartCount(3) != 4 {
		t.Error("4x4 should be 4")
	}
}

func TestStoreInter8x8ResidualSplitsQuadrants(t *testing.T) {
	var src cavlc.Block8x8
	src[0] = 1
	src[3] = 2
	src[4*8] = 3
	src[4*8+4] = 4
	var dst [16][16]int16
	storeInter8x8Residual(&dst, 1, src)
	if dst[4][0] != 1 || dst[4][3] != 2 || dst[6][0] != 3 || dst[7][0] != 4 {
		t.Fatalf("8x8 residual split mismatch: dst4=%v dst6=%v dst7=%v", dst[4], dst[6], dst[7])
	}
}

func TestDecodeInterResidualMasksMalformedChromaCBP(t *testing.T) {
	var coeffs [16][16]int16
	var coeffsChroma [2][4][16]int16
	var totalCoeff [16]int
	var chromaTotalCoeff [2][4]int
	decodeInterResidualCAVLC(nal.NewReader(nil), 0xF0, false, &coeffs, &coeffsChroma, &totalCoeff, &chromaTotalCoeff, nil, nil, nil, nil)
	if coeffsChroma != [2][4][16]int16{} || chromaTotalCoeff != [2][4]int{} {
		t.Fatalf("masked-out malformed chroma bits should not consume/store residuals")
	}
}

func TestDecodeMBInterConsumesTransform8x8Flag(t *testing.T) {
	var w testBitWriter
	w.ue(PMBTypeP16x16)
	w.se(0)
	w.se(0)
	w.ue(2)  // inter CBP table code 2 => cbp=1, luma coded
	w.bit(1) // transform_size_8x8_flag
	w.se(0)
	for i := 0; i < 4; i++ {
		w.bit(1) // zero coeff_token for covered 4x4 residuals
	}
	mb := DecodeMBInter(nal.NewReader(w.bytes()), InterDecodeOpts{Transform8x8: true})
	if !mb.Use8x8Transform || mb.CBP != 1 || mb.QPDelta != 0 {
		t.Fatalf("inter transform8x8 flag not consumed: use=%v cbp=%d qpd=%d", mb.Use8x8Transform, mb.CBP, mb.QPDelta)
	}
}

func TestReadTEClampsMalformedUE(t *testing.T) {
	var w testBitWriter
	w.ue(99)
	if got := readTE(nal.NewReader(w.bytes()), 3); got != 3 {
		t.Fatalf("readTE malformed UE got %d want clamp 3", got)
	}
}

func TestDecodeMBInterIntoClearsOmittedFields(t *testing.T) {
	var rich testBitWriter
	rich.ue(PMBTypeP16x16)
	rich.ue(1) // second of three references
	rich.se(5)
	rich.se(-3)
	rich.ue(2) // CBP 1: first four luma blocks
	rich.se(2)
	for _, bit := range []uint8{0, 1, 0, 1, 1, 1, 1} {
		rich.bit(bit) // one +1 coefficient, then three empty blocks
	}
	want := MBInter{RefIdx: [4]int8{1}, MV: [4]MotionVector{{5, -3}}, CBP: 1, QPDelta: 2}
	want.Coeffs[0][0], want.TotalCoeff[0] = 1, 1
	var reused MBInter
	r := nal.NewReader(rich.bytes())
	DecodeMBInterInto(r, InterDecodeOpts{NumRefFrames: 3}, &reused)
	if reused != want || r.Err() != nil || r.Position() != len(rich.bits) {
		t.Fatalf("nonzero macroblock: got=%+v position=%d/%d err=%v", reused, r.Position(), len(rich.bits), r.Err())
	}
	// Motion prediction and reconstruction may fill fields omitted by entropy
	// decoding. All of them, not only coefficients, must reset on the next MB.
	reused.Use8x8Transform = true
	reused.DecodedMVDX, reused.DecodedMVDY = 7, -9
	for i := range reused.RefIdx {
		reused.RefIdx[i], reused.SubMBType[i] = 2, 3
	}
	for i := range reused.SubMV {
		reused.SubMV[i] = MotionVector{11, -13}
	}
	for comp := range reused.CoeffsChroma {
		for block := range reused.CoeffsChroma[comp] {
			reused.ChromaTotalCoeff[comp][block] = 1
			reused.CoeffsChroma[comp][block][5] = -17
		}
	}
	var empty testBitWriter
	empty.ue(PMBTypeP16x16)
	empty.se(0)
	empty.se(0)
	empty.ue(0) // no reference index, transform flag, QP delta or residual
	r = nal.NewReader(empty.bytes())
	DecodeMBInterInto(r, InterDecodeOpts{}, &reused)
	if reused != (MBInter{}) || r.Err() != nil || r.Position() != len(empty.bits) {
		t.Fatalf("omitted fields leaked: got=%+v position=%d/%d err=%v", reused, r.Position(), len(empty.bits), r.Err())
	}
}

func TestDecodeMBInterIntoHeaderAndErrorsMatchValueAPI(t *testing.T) {
	var intra testBitWriter
	intra.ue(PMBTypeIntra + 12)
	headerBits := len(intra.bits)
	intra.bit(1) // intra payload must remain unread
	intra.bit(0)
	var invalid testBitWriter
	invalid.ue(31)
	var reused MBInter
	for _, tc := range []struct {
		name     string
		input    []byte
		want     MBInter
		position int
		err      error
	}{
		{"intra", intra.bytes(), MBInter{MBType: PMBTypeIntra + 12}, headerBits, nil},
		// With a latched syntax error, the following MVDx/MVDy/CBP readers
		// each consume one zero bit before observing the existing error.
		{"invalid type", invalid.bytes(), MBInter{}, len(invalid.bits) + 3, nal.ErrInvalidSyntax},
		{"truncated type", []byte{0}, MBInter{}, 8, io.ErrUnexpectedEOF},
		{"empty", nil, MBInter{}, 0, io.ErrUnexpectedEOF},
	} {
		reused = MBInter{MBType: 2, CBP: 47, Use8x8Transform: true, QPDelta: 13}
		reused.Coeffs[15][15], reused.SubMV[15] = 123, MotionVector{7, 9}
		r, baseline := nal.NewReader(tc.input), nal.NewReader(tc.input)
		value := DecodeMBInter(baseline, InterDecodeOpts{})
		DecodeMBInterInto(r, InterDecodeOpts{}, &reused)
		if reused != tc.want || value != tc.want || r.Position() != tc.position || baseline.Position() != tc.position || !errors.Is(r.Err(), tc.err) || !errors.Is(baseline.Err(), tc.err) {
			t.Fatalf("%s: got=%+v value=%+v position=%d/%d want=%d err=%v/%v want=%v", tc.name, reused, value, r.Position(), baseline.Position(), tc.position, r.Err(), baseline.Err(), tc.err)
		}
		if tc.name == "intra" {
			if r.ReadBit() != 1 {
				t.Fatal("intra dispatch consumed payload or retained previous inter fields")
			}
		}
	}
	DecodeMBInterInto(nil, InterDecodeOpts{}, &reused)
	if reused != (MBInter{}) || DecodeMBInter(nil, InterDecodeOpts{}) != reused {
		t.Fatal("nil reader did not reset the caller-owned macroblock")
	}
}
