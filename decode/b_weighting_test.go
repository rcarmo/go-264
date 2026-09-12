package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func weightedBFixture() (*Decoder, *frame.Frame, *frame.Frame, *frame.Frame) {
	d := &Decoder{DPB: frame.NewDPB(4), weightedBipredIDC: 1, lumaWeightDenom: 2, chromaWeightDenom: 1}
	past, future := frame.NewFrame(16, 16), frame.NewFrame(16, 16)
	past.POC, past.FullPOC, past.IsRef = 10, 10, true
	future.POC, future.FullPOC, future.IsRef = 30, 30, true
	for i := range past.Y {
		past.Y[i], future.Y[i] = 40, 100
	}
	for i := range past.U {
		past.U[i], future.U[i] = 20, 60
		past.V[i], future.V[i] = 80, 120
	}
	d.DPB.Frames = []*frame.Frame{past, future}
	d.lumaWeightL0[0], d.lumaOffsetL0[0] = 3, -4
	d.lumaWeightL1[0], d.lumaOffsetL1[0] = 5, 7
	d.chromaWeightL0[0] = [2]int32{2, -1}
	d.chromaOffsetL0[0] = [2]int32{-3, 8}
	d.chromaWeightL1[0] = [2]int32{6, 3}
	d.chromaOffsetL1[0] = [2]int32{4, -5}
	out := frame.NewFrame(16, 16)
	out.POC, out.FullPOC = 20, 20
	return d, past, future, out
}

func assertConstantPlane(t *testing.T, plane []byte, stride, width, height int, want byte) {
	t.Helper()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if got := plane[y*stride+x]; got != want {
				t.Fatalf("pixel (%d,%d)=%d want %d", x, y, got, want)
			}
		}
	}
}

func TestExplicitWeightedBReconstructionBi(t *testing.T) {
	d, _, _, out := weightedBFixture()
	d.reconstructMBBidi(out, &syntax.MBBidi{MBType: syntax.BMBTypeBi16x16}, 0, 0, 26)
	// Luma: ((40*3 + 100*5 + 4) >> 3) + ((-4+7+1)>>1) = 80.
	assertConstantPlane(t, out.Y, out.StrideY, 16, 16, 80)
	// U: ((20*2 + 60*6 + 2) >> 2) + ((-3+4+1)>>1) = 101.
	assertConstantPlane(t, out.U, out.StrideC, 8, 8, 101)
	// V: ((80*-1 + 120*3 + 2) >> 2) + ((8-5+1)>>1) = 72.
	assertConstantPlane(t, out.V, out.StrideC, 8, 8, 72)
}

func TestExplicitWeightedBReconstructionUniLists(t *testing.T) {
	t.Run("L0", func(t *testing.T) {
		d, _, _, out := weightedBFixture()
		d.reconstructMBBidi(out, &syntax.MBBidi{MBType: syntax.BMBTypeL016x16}, 0, 0, 26)
		// (40*3 + 2)>>2 - 4 = 26.
		assertConstantPlane(t, out.Y, out.StrideY, 16, 16, 26)
		// (20*2 + 1)>>1 - 3 = 17; (80*-1 + 1)>>1 + 8 = 0 after clipping.
		assertConstantPlane(t, out.U, out.StrideC, 8, 8, 17)
		assertConstantPlane(t, out.V, out.StrideC, 8, 8, 0)
	})
	t.Run("L1", func(t *testing.T) {
		d, _, _, out := weightedBFixture()
		d.reconstructMBBidi(out, &syntax.MBBidi{MBType: syntax.BMBTypeL116x16}, 0, 0, 26)
		// (100*5 + 2)>>2 + 7 = 132.
		assertConstantPlane(t, out.Y, out.StrideY, 16, 16, 132)
		// (60*6 + 1)>>1 + 4 = 184; (120*3 + 1)>>1 - 5 = 175.
		assertConstantPlane(t, out.U, out.StrideC, 8, 8, 184)
		assertConstantPlane(t, out.V, out.StrideC, 8, 8, 175)
	})
}

func TestImplicitWeightedBReconstruction(t *testing.T) {
	d, past, future, out := weightedBFixture()
	d.weightedBipredIDC = 2
	d.currentFullPOC = out.FullPOC
	past.FullPOC, future.FullPOC = 0, 30
	out.FullPOC, d.currentFullPOC = 10, 10
	w0, w1 := implicitBipredWeights(10, 0, 30)
	if w0 == 32 && w1 == 32 {
		t.Fatal("fixture did not produce implicit weighting")
	}
	d.reconstructMBBidi(out, &syntax.MBBidi{MBType: syntax.BMBTypeBi16x16}, 0, 0, 26)
	wantY := clipWeightedSample((40*w0 + 100*w1 + 32) >> 6)
	wantU := clipWeightedSample((20*w0 + 60*w1 + 32) >> 6)
	wantV := clipWeightedSample((80*w0 + 120*w1 + 32) >> 6)
	assertConstantPlane(t, out.Y, out.StrideY, 16, 16, wantY)
	assertConstantPlane(t, out.U, out.StrideC, 8, 8, wantU)
	assertConstantPlane(t, out.V, out.StrideC, 8, 8, wantV)
}
