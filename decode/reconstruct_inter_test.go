package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestReconstructMBInterHandlesNilInputs(t *testing.T) {
	var d Decoder
	d.reconstructMBInter(nil, nil, 0, 0, 26)
	d.reconstructMBInter(frame.NewFrame(16, 16), nil, 0, 0, 26)
}

func TestReconstructMBInterNoReferenceFillsLumaAndLeavesNeutralChroma(t *testing.T) {
	var d Decoder
	f := frame.NewFrame(16, 16)
	mb := &syntax.MBInter{MBType: syntax.PMBTypeP16x16}
	d.reconstructMBInter(f, mb, 0, 0, 26)
	for i, got := range f.Y[:16*16] {
		if got != 128 {
			t.Fatalf("Y[%d] got %d want 128", i, got)
		}
	}
	for i, got := range f.U[:8*8] {
		if got != 128 {
			t.Fatalf("U[%d] got %d want neutral 128", i, got)
		}
	}
	for i, got := range f.V[:8*8] {
		if got != 128 {
			t.Fatalf("V[%d] got %d want neutral 128", i, got)
		}
	}
}
