package syntax

import (
	"testing"

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
