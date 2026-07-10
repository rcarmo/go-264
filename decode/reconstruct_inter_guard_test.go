package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestApplyWeightedChromaL0Rect(t *testing.T) {
	d := &Decoder{weightedPred: true, chromaWeightDenom: 1}
	d.chromaWeightL0[0] = [2]int32{1, 3}
	d.chromaOffsetL0[0] = [2]int32{-2, 4}
	predU := make([]uint8, 64)
	predV := make([]uint8, 64)
	for i := range predU {
		predU[i], predV[i] = 100, 100
	}
	d.applyWeightedChromaL0Rect(predU, 0, 0, 2, 3, 4, 2)
	d.applyWeightedChromaL0Rect(predV, 1, 0, 2, 3, 4, 2)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			wantU, wantV := uint8(100), uint8(100)
			if x >= 2 && x < 6 && y >= 3 && y < 5 {
				wantU, wantV = 48, 154
			}
			if predU[y*8+x] != wantU || predV[y*8+x] != wantV {
				t.Fatalf("sample (%d,%d) got U/V=%d/%d want %d/%d", x, y, predU[y*8+x], predV[y*8+x], wantU, wantV)
			}
		}
	}
}

func TestReconstructChromaInterHandlesNilInputs(t *testing.T) {
	d := &Decoder{}
	d.reconstructChromaInter(nil, nil, &syntax.MBInter{}, 0, 0, 26)
	d.reconstructChromaInter(frame.NewFrame(16, 16), nil, nil, 0, 0, 26)
}

func TestBPredictionHelpersHandleMalformedRects(t *testing.T) {
	d := &Decoder{}
	var dst [256]uint8
	ref := frame.NewFrame(16, 16)
	fillBPredBlock(dst[:], ref, 0, 0, -1, 0, 8, 8, syntax.MotionVector{})
	fillBPredBlock(dst[:], ref, 0, 0, 0, 0, 17, 1, syntax.MotionVector{})
	fillBPredBlock(dst[:], &frame.Frame{Width: 16, Height: 16}, 0, 0, 0, 0, 16, 16, syntax.MotionVector{})
	d.fillBPredByUse(dst[:], ref, 0, 0, 15, 15, 2, 2, 0, 0, syntax.MotionVector{}, syntax.MotionVector{}, true, true)
	d.fillBPredByUse(dst[:0], ref, 0, 0, 0, 0, 16, 16, 0, 0, syntax.MotionVector{}, syntax.MotionVector{}, true, true)
}

func TestReconstructMBBidiHandlesInvalidInputs(t *testing.T) {
	var nilDecoder *Decoder
	nilDecoder.reconstructMBBidi(frame.NewFrame(16, 16), &syntax.MBBidi{}, 0, 0, 26)
	d := &Decoder{}
	d.reconstructMBBidi(nil, &syntax.MBBidi{}, 0, 0, 26)
	d.reconstructMBBidi(frame.NewFrame(16, 16), nil, 0, 0, 26)
	d.reconstructMBBidi(frame.NewFrame(16, 16), &syntax.MBBidi{}, -1, 0, 26)
	d.reconstructMBBidi(frame.NewFrame(16, 16), &syntax.MBBidi{}, 2, 0, 26)
}

func TestReconstructMBBidiPartitionFallbackUsesCurrentFrame(t *testing.T) {
	d := &Decoder{}
	f := frame.NewFrame(16, 16)
	for i := range f.Y {
		f.Y[i] = 77
	}
	d.reconstructMBBidi(f, &syntax.MBBidi{MBType: 12}, 0, 0, 26) // B_L0_Bi_16x8
	for i, got := range f.Y {
		if got != 77 {
			t.Fatalf("partition fallback pixel %d got %d want current-frame prediction 77", i, got)
		}
	}
}

func TestReconstructMBBidiUsesPartitionListMapping(t *testing.T) {
	d := &Decoder{DPB: frame.NewDPB(4)}
	ref0 := frame.NewFrame(16, 16)
	ref1 := frame.NewFrame(16, 16)
	ref0.POC, ref1.POC = 10, 30
	ref0.IsRef, ref1.IsRef = true, true
	for i := range ref0.Y {
		ref0.Y[i] = 20
		ref1.Y[i] = 80
	}
	for i := range ref0.U {
		ref0.U[i], ref0.V[i] = 40, 90
		ref1.U[i], ref1.V[i] = 100, 150
	}
	d.DPB.Frames = []*frame.Frame{ref0, ref1}
	f := frame.NewFrame(16, 16)
	f.POC = 20
	d.reconstructMBBidi(f, &syntax.MBBidi{MBType: 12}, 0, 0, 26) // B_L0_Bi_16x8
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			if got := f.PixelY(x, y); got != 20 {
				t.Fatalf("top L0 partition pixel (%d,%d) got %d want past-reference value 20", x, y, got)
			}
		}
	}
	for y := 8; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if got := f.PixelY(x, y); got != 50 {
				t.Fatalf("bottom Bi partition pixel (%d,%d) got %d want blend 50", x, y, got)
			}
		}
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			if got := f.U[y*f.StrideC+x]; got != 40 {
				t.Fatalf("top L0 chroma U (%d,%d) got %d want past-reference value 40", x, y, got)
			}
		}
	}
	for y := 4; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if got := f.U[y*f.StrideC+x]; got != 70 {
				t.Fatalf("bottom Bi chroma U (%d,%d) got %d want blend 70", x, y, got)
			}
		}
	}
}

func TestReconstructMBBidiUsesB8x8SubListMapping(t *testing.T) {
	d := &Decoder{DPB: frame.NewDPB(4)}
	ref0 := frame.NewFrame(16, 16)
	ref1 := frame.NewFrame(16, 16)
	for i := range ref0.Y {
		ref0.Y[i] = 30
		ref1.Y[i] = 90
	}
	for i := range ref0.U {
		ref0.U[i], ref0.V[i] = 50, 90
		ref1.U[i], ref1.V[i] = 110, 150
	}
	d.DPB.Frames = []*frame.Frame{ref0, ref1}
	f := frame.NewFrame(16, 16)
	mb := &syntax.MBBidi{MBType: syntax.BMBTypeB8x8, SubMBType: [4]uint32{1, 2, 3, 0}}
	d.reconstructMBBidi(f, mb, 0, 0, 26)
	if got := f.PixelY(0, 0); got != 90 {
		t.Fatalf("B8x8 L0 sub block got %d want 90", got)
	}
	if got := f.PixelY(8, 0); got != 30 {
		t.Fatalf("B8x8 L1 sub block got %d want 30", got)
	}
	if got := f.PixelY(0, 8); got != 60 {
		t.Fatalf("B8x8 Bi sub block got %d want blend 60", got)
	}
	if got := f.PixelY(8, 8); got != 60 {
		t.Fatalf("B8x8 direct fallback sub block got %d want blend 60", got)
	}
	if got := f.U[0]; got != 110 {
		t.Fatalf("B8x8 L0 chroma got %d want 110", got)
	}
	if got := f.U[4]; got != 50 {
		t.Fatalf("B8x8 L1 chroma got %d want 50", got)
	}
	if got := f.U[4*f.StrideC]; got != 80 {
		t.Fatalf("B8x8 Bi chroma got %d want blend 80", got)
	}
	if got := f.U[4*f.StrideC+4]; got != 80 {
		t.Fatalf("B8x8 direct chroma got %d want blend 80", got)
	}
}

func TestReconstructMBBidiUsesParsedReferenceIndices(t *testing.T) {
	d := &Decoder{DPB: frame.NewDPB(4)}
	ref0 := frame.NewFrame(16, 16)
	ref1 := frame.NewFrame(16, 16)
	ref2 := frame.NewFrame(16, 16)
	ref0.POC = 10
	ref1.POC = 20
	ref2.POC = 30
	ref0.IsRef = true
	ref1.IsRef = true
	ref2.IsRef = true
	for i := range ref0.Y {
		ref0.Y[i] = 10
		ref1.Y[i] = 50
		ref2.Y[i] = 90
	}
	d.DPB.Frames = []*frame.Frame{ref0, ref1, ref2}
	f := frame.NewFrame(16, 16)
	f.POC = 26
	// L0 list for currentPOC=26: past frames sorted descending POC = [ref2(30)... wait, 30>26 is future]
	// currentPOC=26: L0 past = [ref1(20), ref0(10)] desc = [ref1, ref0]; L0[1]=ref0(Y=10)... no wait
	// L0 list: POC < 26 = ref0(10), ref1(20) sorted desc = [ref1(50), ref0(10)]
	// L0[0]=ref1(Y=50), L0[1]=ref0(Y=10)
	d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeL016x16, RefIdxL0: [4]int8{1}}, 0, 0, 26)
	for i, got := range f.Y[:16] {
		if got != 10 {
			t.Fatalf("L0 ref index not applied at pixel %d: got %d want 10", i, got)
		}
	}
	f = frame.NewFrame(16, 16)
	f.POC = 26
	// L1 = [ref2(90), ref1(50), ref0(10)]; L1[1]=ref1(Y=50)
	d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeL116x16, RefIdxL1: [4]int8{1}}, 0, 0, 26)
	for i, got := range f.Y[:16] {
		if got != 50 {
			t.Fatalf("L1 ref index not applied at pixel %d: got %d want 50", i, got)
		}
	}
}

func TestReconstructMBBidiAppliesZeroResidualPrediction(t *testing.T) {
	d := &Decoder{}
	f := frame.NewFrame(16, 16)
	for i := range f.Y {
		f.Y[i] = 90
	}
	d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16}, 0, 0, 26)
	for i, got := range f.Y {
		if got != 90 {
			t.Fatalf("pixel %d got %d want blended self prediction 90", i, got)
		}
	}
}

func TestInterResidualWritersHandleOutOfFrameInputs(t *testing.T) {
	d := &Decoder{}
	f := frame.NewFrame(16, 16)
	mb := &syntax.MBInter{MBType: syntax.PMBTypeP16x16}
	var predLuma [256]uint8
	var predChroma [64]uint8
	d.writeInterResidual(f, mb, predLuma[:], -1, 0, 26)
	d.writeInterResidual(f, mb, predLuma[:], 2, 0, 26)
	d.writeChromaInterResidual(f, mb, predChroma[:], 0, -1, 0, 26)
	d.writeChromaInterResidual(f, mb, predChroma[:], 0, 2, 0, 26)
}
