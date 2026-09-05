package decode

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
	"github.com/rcarmo/go-264/transform"
)

func TestReconstructMBHandlesNilInputs(t *testing.T) {
	d := &Decoder{}
	d.reconstructMB(nil, &syntax.MBIntra{MBType: syntax.MBTypeINxN}, 0, 0, 26, nil)
	d.reconstructMB(frame.NewFrame(16, 16), nil, 0, 0, 26, nil)
}

func TestIntraLumaReconstructorsHandleInvalidInputs(t *testing.T) {
	d := &Decoder{}
	f := frame.NewFrame(16, 16)
	mb := &syntax.MBIntra{MBType: syntax.MBTypeINxN}
	d.reconstruct16x16(nil, mb, 0, 0, 26)
	d.reconstruct16x16(f, nil, 0, 0, 26)
	d.reconstruct16x16(f, mb, -1, 0, 26)
	d.reconstruct16x16(f, mb, 2, 0, 26)
	d.reconstruct4x4(nil, mb, 0, 0, 26)
	d.reconstruct4x4(f, nil, 0, 0, 26)
	d.reconstruct4x4(f, mb, -1, 0, 26)
	d.reconstruct4x4(f, mb, 2, 0, 26)
	d.reconstruct8x8(nil, mb, 0, 0, 26)
	d.reconstruct8x8(f, nil, 0, 0, 26)
	d.reconstruct8x8(f, mb, -1, 0, 26)
	d.reconstruct8x8(f, mb, 2, 0, 26)
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

func TestPredictChroma8x8HandlesInvalidFrames(t *testing.T) {
	d := &Decoder{}
	cases := []struct {
		name string
		f    *frame.Frame
		mbX  int
		mbY  int
	}{
		{"nil", nil, 1, 1},
		{"negative", frame.NewFrame(16, 16), -1, 0},
		{"outside", frame.NewFrame(16, 16), 2, 0},
		{"short-chroma-plane", &frame.Frame{Width: 16, Height: 16, StrideC: 8, U: make([]uint8, 1), V: make([]uint8, 1)}, 0, 0},
		{"bad-chroma-stride", &frame.Frame{Width: 16, Height: 16, StrideC: 4, U: make([]uint8, 64), V: make([]uint8, 64)}, 0, 0},
	}
	for _, tc := range cases {
		got := d.predictChroma8x8(tc.f, 0, tc.mbX, tc.mbY, 0)
		for i, v := range got {
			if v != 128 {
				t.Fatalf("%s pred[%d] got %d want 128", tc.name, i, v)
			}
		}
	}
}

func intraResidualTestState() (*Decoder, *frame.Frame) {
	d, f := intraNeighborTestState()
	// Give every plane a distinct padded stride and no trailing row padding.
	// Reconstructing the final MB must not touch the intervening padding.
	f.StrideY, f.StrideC = 53, 27
	f.Y = make([]byte, 47*f.StrideY+48)
	f.U = make([]byte, 23*f.StrideC+24)
	f.V = make([]byte, 23*f.StrideC+24)
	for component, plane := range [][]byte{f.Y, f.U, f.V} {
		for i := range plane {
			plane[i] = byte(i*31 + component*47)
		}
	}
	return d, f
}

func intraResidualTestMB(cbp uint32) syntax.MBIntra {
	mb := syntax.MBIntra{Intra16x16PredMode: pred.Intra16x16DC, CodedBlockPattern: cbp}
	for blk := range mb.Coeffs {
		mb.IntraPredMode[blk] = -1
		for i := range mb.Coeffs[blk] {
			if cbp != 0 || i == 0 {
				mb.Coeffs[blk][i] = int16((blk*3+i*7)%11 - 5)
			}
		}
	}
	for component := range mb.CoeffsChroma {
		for blk := range mb.CoeffsChroma[component] {
			for i := range mb.CoeffsChroma[component][blk] {
				mb.CoeffsChroma[component][blk][i] = int16((component*5+blk*3+i*7)%11 - 5)
			}
		}
	}
	return mb
}

// Keep the expected integration independent of the reused dequant/add helpers:
// retain the former widened multiplication and perform scalar sample addition.
func intraResidualACReference(block *[16]int16, qp int) {
	for i := 1; i < 16; i++ {
		v := int32(transform.DequantVTable()[qp%6][transform.PosToVTable()[i]])
		block[i] = int16(int32(block[i]) * v << uint(qp/6))
	}
}

func TestIntra16x16ResidualMatchesScalarComposition(t *testing.T) {
	for qp := 0; qp <= 51; qp++ {
		for _, cbp := range []uint32{0, 15} {
			for _, origin := range [][2]int{{0, 0}, {2, 2}} {
				d, f := intraResidualTestState()
				mb := intraResidualTestMB(cbp)
				before := mb
				want := append([]byte(nil), f.Y...)
				x0, y0 := origin[0]*16, origin[1]*16
				prediction := 128
				if x0 > 0 && y0 > 0 {
					sum := 0
					for i := 0; i < 16; i++ {
						sum += int(f.PixelY(x0+i, y0-1)) + int(f.PixelY(x0-1, y0+i))
					}
					prediction = (sum + 16) >> 5
				}
				var dc [16]int16
				for blk := range mb.Coeffs {
					pos := blk4x4Y[blk] + blk4x4X[blk]/4
					dc[pos] = mb.Coeffs[blk][0]
				}
				transform.Hadamard4x4DC(dc[:], qp)
				for blk := range mb.Coeffs {
					var residual [16]int16
					if cbp != 0 {
						residual = mb.Coeffs[blk]
						intraResidualACReference(&residual, qp)
					}
					bx, by := blk4x4X[blk], blk4x4Y[blk]
					residual[0] = dc[by+bx/4]
					transform.IDCT4x4(residual[:])
					for y := 0; y < 4; y++ {
						for x := 0; x < 4; x++ {
							want[(y0+by+y)*f.StrideY+x0+bx+x] = clip8(prediction + int(residual[y*4+x]))
						}
					}
				}
				d.reconstruct16x16(f, &mb, origin[0], origin[1], qp)
				if !bytes.Equal(f.Y, want) || mb != before {
					t.Fatalf("qp=%d cbp=%d origin=%v: residual pixels, DC, padding, or input changed", qp, cbp, origin)
				}
			}
		}
	}
}

func TestChromaIntraResidualMatchesScalarComposition(t *testing.T) {
	t.Setenv("GO264_RECON_TRACE", "")
	for qp := 0; qp <= 51; qp++ {
		for mode := 0; mode < 4; mode++ {
			d, f := intraResidualTestState()
			mb := intraResidualTestMB(15)
			mb.ChromaPredMode = int8(mode)
			before := mb
			var expected [2][]byte
			for component, plane := range [][]byte{f.U, f.V} {
				expected[component] = append([]byte(nil), plane...)
				prediction := d.predictChroma8x8(f, component, 2, 2, mode)
				chromaQP := frame.ChromaQP(qp, d.chromaQPOffset)
				var dc [4]int16
				for blk := range dc {
					dc[blk] = mb.CoeffsChroma[component][blk][0]
				}
				transform.Hadamard2x2DC(dc[:], chromaQP)
				for blk := 0; blk < 4; blk++ {
					residual := mb.CoeffsChroma[component][blk]
					intraResidualACReference(&residual, chromaQP)
					residual[0] = dc[blk]
					transform.IDCT4x4(residual[:])
					bx, by := (blk&1)*4, (blk>>1)*4
					for y := 0; y < 4; y++ {
						for x := 0; x < 4; x++ {
							expected[component][(16+by+y)*f.StrideC+16+bx+x] = clip8(int(prediction[(by+y)*8+bx+x]) + int(residual[y*4+x]))
						}
					}
				}
			}
			d.reconstructChromaIntra(f, &mb, 2, 2, qp)
			if !bytes.Equal(f.U, expected[0]) || !bytes.Equal(f.V, expected[1]) || mb != before {
				t.Fatalf("qp=%d mode=%d: chroma residual pixels, plane selection, padding, or input changed", qp, mode)
			}
		}
	}
}

func captureIntraReconTrace(t *testing.T, run func()) string {
	t.Helper()
	output, err := os.CreateTemp(t.TempDir(), "intra-trace-")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = output
	defer func() { os.Stderr = original; output.Close() }()
	run()
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestIntraResidualFastPathMatchesTraceSamples(t *testing.T) {
	for _, qp := range []int{0, 26, 51} {
		for _, cbp := range []uint32{0, 15} {
			d, got := intraResidualTestState()
			tracedDecoder, traced := intraResidualTestState()
			mb := intraResidualTestMB(cbp)
			// Unlike Intra16 DC, uncoded I4x4 blocks have no residual.
			if cbp == 0 {
				mb.Coeffs = [16][16]int16{}
			}
			t.Setenv("GO264_RECON_TRACE", "")
			d.trace = snapshotTraceConfig()
			d.reconstruct4x4(got, &mb, 2, 2, qp)
			d.reconstructChromaIntra(got, &mb, 2, 2, qp)
			t.Setenv("GO264_RECON_TRACE", "1")
			tracedDecoder.trace = snapshotTraceConfig()
			trace := captureIntraReconTrace(t, func() {
				tracedDecoder.reconstruct4x4(traced, &mb, 2, 2, qp)
				tracedDecoder.reconstructChromaIntra(traced, &mb, 2, 2, qp)
			})
			if trace == "" {
				t.Fatal("traced reconstruction did not run")
			}
			if !bytes.Equal(got.Y, traced.Y) || !bytes.Equal(got.U, traced.U) || !bytes.Equal(got.V, traced.V) ||
				!reflect.DeepEqual(d.intraModes, tracedDecoder.intraModes) || d.traceIntra4x4PredMode != tracedDecoder.traceIntra4x4PredMode {
				t.Fatalf("qp=%d cbp=%d: fast and traced reconstruction differ", qp, cbp)
			}
		}
	}
}
