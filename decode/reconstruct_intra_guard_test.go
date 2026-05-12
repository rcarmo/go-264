package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestReconstructMBHandlesNilInputs(t *testing.T) {
	d := &Decoder{}
	d.reconstructMB(nil, &syntax.MBIntra{MBType: syntax.MBTypeINxN}, 0, 0, 26, nil)
	d.reconstructMB(frame.NewFrame(16, 16), nil, 0, 0, 26, nil)
}

func TestReconstructIPCMHandlesOutOfFrameInputs(t *testing.T) {
	d := &Decoder{}
	f := frame.NewFrame(16, 16)
	mb := &syntax.MBIntra{MBType: syntax.MBTypeIPCM}
	d.reconstructIPCM(nil, mb, 0, 0)
	d.reconstructIPCM(f, nil, 0, 0)
	d.reconstructIPCM(f, mb, -1, 0)
	d.reconstructIPCM(f, mb, 2, 0)
}

func TestReconstructChromaIntraHandlesInvalidInputs(t *testing.T) {
	d := &Decoder{}
	d.reconstructChromaIntra(nil, &syntax.MBIntra{}, 0, 0, 26)
	d.reconstructChromaIntra(frame.NewFrame(16, 16), nil, 0, 0, 26)
	d.reconstructChromaIntra(frame.NewFrame(16, 16), &syntax.MBIntra{}, -1, 0, 26)
	d.reconstructChromaIntra(frame.NewFrame(16, 16), &syntax.MBIntra{}, 2, 0, 26)
}

func TestPredictChroma8x8HandlesNilFrame(t *testing.T) {
	d := &Decoder{}
	got := d.predictChroma8x8(nil, 0, 1, 1, 0)
	for i, v := range got {
		if v != 128 {
			t.Fatalf("nil frame pred[%d] got %d want 128", i, v)
		}
	}
}
