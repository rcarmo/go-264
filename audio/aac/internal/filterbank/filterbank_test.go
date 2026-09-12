package filterbank

import (
	"errors"
	"math"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

type refState struct {
	overlap   [outputSamples]float64
	prevShape int
}

func TestSynthesizeMatchesIndependentReference(t *testing.T) {
	bank := New()
	ref := refState{prevShape: -1}
	frames := []struct {
		name     string
		sequence int
		shape    int
		coeff    []float64
	}{
		{"only-long-kbd", SequenceOnlyLong, ShapeKBD, coeffPattern(1)},
		{"long-start-sine", SequenceLongStart, ShapeSine, coeffPattern(2)},
		{"eight-short-kbd", SequenceEightShort, ShapeKBD, coeffPattern(3)},
		{"long-stop-sine", SequenceLongStop, ShapeSine, coeffPattern(4)},
	}
	for _, frame := range frames {
		t.Run(frame.name, func(t *testing.T) {
			want, nextRef := refSynthesize(ref, frame.coeff, frame.sequence, frame.shape)
			dst := make([]float64, outputSamples)
			if err := bank.Synthesize(frame.coeff, frame.sequence, frame.shape, dst); err != nil {
				t.Fatalf("Synthesize() error = %v", err)
			}
			assertCloseSlice(t, dst, want[:], 1e-12)
			assertCloseSlice(t, bank.overlap[:], nextRef.overlap[:], 1e-12)
			if bank.prevShape != nextRef.prevShape {
				t.Fatalf("prevShape=%d want %d", bank.prevShape, nextRef.prevShape)
			}
			ref = nextRef
		})
	}
}

func TestSynthesizeSilence(t *testing.T) {
	zero := make([]float64, coeffCount)
	cases := []struct {
		name     string
		sequence int
		shape    int
	}{
		{"only-long-sine", SequenceOnlyLong, ShapeSine},
		{"long-start-kbd", SequenceLongStart, ShapeKBD},
		{"eight-short-sine", SequenceEightShort, ShapeSine},
		{"long-stop-kbd", SequenceLongStop, ShapeKBD},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bank := New()
			dst := make([]float64, outputSamples)
			if err := bank.Synthesize(zero, tc.sequence, tc.shape, dst); err != nil {
				t.Fatalf("Synthesize() error = %v", err)
			}
			for i, v := range dst {
				if v != 0 {
					t.Fatalf("dst[%d]=%g want 0", i, v)
				}
			}
			for i, v := range bank.overlap {
				if v != 0 {
					t.Fatalf("overlap[%d]=%g want 0", i, v)
				}
			}
		})
	}
}

func TestSynthesizeImpulseMatchesReference(t *testing.T) {
	cases := []struct {
		name     string
		sequence int
		shape    int
		index    int
	}{
		{"only-long", SequenceOnlyLong, ShapeSine, 0},
		{"long-start", SequenceLongStart, ShapeKBD, 17},
		{"eight-short", SequenceEightShort, ShapeSine, 256},
		{"long-stop", SequenceLongStop, ShapeKBD, 777},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			coeff := make([]float64, coeffCount)
			coeff[tc.index] = 1
			bank := New()
			want, nextRef := refSynthesize(refState{prevShape: -1}, coeff, tc.sequence, tc.shape)
			dst := make([]float64, outputSamples)
			if err := bank.Synthesize(coeff, tc.sequence, tc.shape, dst); err != nil {
				t.Fatalf("Synthesize() error = %v", err)
			}
			assertCloseSlice(t, dst, want[:], 1e-12)
			assertCloseSlice(t, bank.overlap[:], nextRef.overlap[:], 1e-12)
			for i, v := range dst {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("dst[%d] not finite: %g", i, v)
				}
			}
		})
	}
}

func TestResetMatchesFreshBank(t *testing.T) {
	coeff := coeffPattern(7)
	bank := New()
	if err := bank.Synthesize(coeff, SequenceOnlyLong, ShapeKBD, make([]float64, outputSamples)); err != nil {
		t.Fatalf("prime error = %v", err)
	}
	bank.Reset()

	dstReset := make([]float64, outputSamples)
	if err := bank.Synthesize(coeff, SequenceLongStart, ShapeSine, dstReset); err != nil {
		t.Fatalf("reset bank synth error = %v", err)
	}

	fresh := New()
	dstFresh := make([]float64, outputSamples)
	if err := fresh.Synthesize(coeff, SequenceLongStart, ShapeSine, dstFresh); err != nil {
		t.Fatalf("fresh bank synth error = %v", err)
	}
	assertCloseSlice(t, dstReset, dstFresh, 1e-12)
	assertCloseSlice(t, bank.overlap[:], fresh.overlap[:], 1e-12)
	if bank.prevShape != fresh.prevShape {
		t.Fatalf("prevShape=%d want %d", bank.prevShape, fresh.prevShape)
	}
}

func TestSynthesizeInvalidDoesNotMutate(t *testing.T) {
	prime := coeffPattern(9)
	cases := []struct {
		name     string
		coeff    []float64
		sequence int
		shape    int
		dstLen   int
	}{
		{"short-coeff", prime[:coeffCount-1], SequenceOnlyLong, ShapeSine, outputSamples},
		{"short-dst", prime, SequenceOnlyLong, ShapeSine, outputSamples - 1},
		{"bad-sequence", prime, 4, ShapeSine, outputSamples},
		{"bad-shape", prime, SequenceOnlyLong, 2, outputSamples},
		{"nan", withBadValue(prime, 123, math.NaN()), SequenceOnlyLong, ShapeSine, outputSamples},
		{"inf", withBadValue(prime, 456, math.Inf(1)), SequenceOnlyLong, ShapeSine, outputSamples},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bank := New()
			primeDst := make([]float64, outputSamples)
			if err := bank.Synthesize(prime, SequenceOnlyLong, ShapeKBD, primeDst); err != nil {
				t.Fatalf("prime error = %v", err)
			}
			beforeOverlap := bank.overlap
			beforePrev := bank.prevShape
			dst := make([]float64, tc.dstLen)
			for i := range dst {
				dst[i] = -17.5
			}
			beforeDst := append([]float64(nil), dst...)
			err := bank.Synthesize(tc.coeff, tc.sequence, tc.shape, dst)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, pcm.ErrMalformed) {
				t.Fatalf("error = %v, want malformed", err)
			}
			assertCloseSlice(t, bank.overlap[:], beforeOverlap[:], 0)
			if bank.prevShape != beforePrev {
				t.Fatalf("prevShape=%d want %d", bank.prevShape, beforePrev)
			}
			assertCloseSlice(t, dst, beforeDst, 0)
		})
	}
}

func TestDestinationTailUntouched(t *testing.T) {
	bank := New()
	coeff := coeffPattern(11)
	dst := make([]float64, outputSamples+9)
	for i := range dst {
		dst[i] = -3.25
	}
	if err := bank.Synthesize(coeff, SequenceOnlyLong, ShapeSine, dst); err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	changed := false
	for i := 0; i < outputSamples; i++ {
		if dst[i] != -3.25 {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("dst prefix did not change")
	}
	for i := outputSamples; i < len(dst); i++ {
		if dst[i] != -3.25 {
			t.Fatalf("dst tail modified at %d: %g", i, dst[i])
		}
	}
}

func BenchmarkSynthesize(b *testing.B) {
	b.Run("only-long", func(b *testing.B) {
		bank := New()
		coeff := coeffPattern(13)
		dst := make([]float64, outputSamples)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			bank.Reset()
			if err := bank.Synthesize(coeff, SequenceOnlyLong, ShapeKBD, dst); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("eight-short", func(b *testing.B) {
		bank := New()
		coeff := coeffPattern(14)
		dst := make([]float64, outputSamples)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			bank.Reset()
			if err := bank.Synthesize(coeff, SequenceEightShort, ShapeSine, dst); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func coeffPattern(seed int) []float64 {
	out := make([]float64, coeffCount)
	for i := range out {
		j := float64(i + 1 + seed)
		out[i] = 0.35*math.Sin(j*0.071) + 0.2*math.Cos(j*0.013) + float64((i+seed)%7-3)/50.0
	}
	return out
}

func withBadValue(src []float64, idx int, bad float64) []float64 {
	out := append([]float64(nil), src...)
	out[idx] = bad
	return out
}

func assertCloseSlice(t *testing.T, got []float64, want []float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length=%d want %d", len(got), len(want))
	}
	for i := range got {
		d := math.Abs(got[i] - want[i])
		if d > tol {
			t.Fatalf("idx=%d got=%0.16g want=%0.16g diff=%g tol=%g", i, got[i], want[i], d, tol)
		}
	}
}

func refSynthesize(state refState, coeff []float64, sequence int, shape int) ([outputSamples]float64, refState) {
	leftShape := shape
	if state.prevShape >= 0 {
		leftShape = state.prevShape
	}
	var z [longTransform]float64
	switch sequence {
	case SequenceOnlyLong, SequenceLongStart, SequenceLongStop:
		x := refIMDCT(coeff, longTransform)
		w := refLongWindow(sequence, leftShape, shape)
		for i := 0; i < longTransform; i++ {
			z[i] = x[i] * w[i]
		}
	case SequenceEightShort:
		for j := 0; j < shortWindows; j++ {
			base := j * shortCoeffCount
			x := refIMDCT(coeff[base:base+shortCoeffCount], shortTransform)
			thisLeft := shape
			if j == 0 {
				thisLeft = leftShape
			}
			w := refShortWindow(thisLeft, shape)
			pos := shortStart + j*shortHop
			for i := 0; i < shortTransform; i++ {
				z[pos+i] += x[i] * w[i]
			}
		}
	}
	var out [outputSamples]float64
	var next refState
	next.prevShape = shape
	for i := 0; i < outputSamples; i++ {
		out[i] = z[i] + state.overlap[i]
		next.overlap[i] = z[outputSamples+i]
	}
	return out, next
}

func refIMDCT(coeff []float64, nTransform int) []float64 {
	half := nTransform / 2
	n0 := float64(half+1) / 2.0
	scale := 2.0 / float64(nTransform)
	step := 2.0 * math.Pi / float64(nTransform)
	out := make([]float64, nTransform)
	for n := 0; n < nTransform; n++ {
		nf := float64(n) + n0
		acc := 0.0
		for k := 0; k < half; k++ {
			acc += coeff[k] * math.Cos(step*nf*(float64(k)+0.5))
		}
		out[n] = scale * acc
	}
	return out
}

func refLongWindow(sequence int, leftShape int, rightShape int) []float64 {
	w := make([]float64, longTransform)
	leftLong := refHalfWindow(leftShape, longTransform)
	rightLong := refHalfWindow(rightShape, longTransform)
	half := longTransform / 2
	switch sequence {
	case SequenceOnlyLong:
		copy(w[:half], leftLong)
		for i := 0; i < half; i++ {
			w[half+i] = rightLong[half-1-i]
		}
	case SequenceLongStart:
		copy(w[:half], leftLong)
		for i := half; i < half+shortStart; i++ {
			w[i] = 1
		}
		rightShort := refHalfWindow(rightShape, shortTransform)
		for i := 0; i < shortCoeffCount; i++ {
			w[half+shortStart+i] = rightShort[shortCoeffCount-1-i]
		}
	case SequenceLongStop:
		leftShort := refHalfWindow(leftShape, shortTransform)
		for i := 0; i < shortCoeffCount; i++ {
			w[shortStart+i] = leftShort[i]
		}
		for i := shortStart + shortCoeffCount; i < half; i++ {
			w[i] = 1
		}
		for i := 0; i < half; i++ {
			w[half+i] = rightLong[half-1-i]
		}
	}
	return w
}

func refShortWindow(leftShape int, rightShape int) []float64 {
	w := make([]float64, shortTransform)
	left := refHalfWindow(leftShape, shortTransform)
	right := refHalfWindow(rightShape, shortTransform)
	for i := 0; i < shortCoeffCount; i++ {
		w[i] = left[i]
		w[shortCoeffCount+i] = right[shortCoeffCount-1-i]
	}
	return w
}

func refHalfWindow(shape int, nTransform int) []float64 {
	half := nTransform / 2
	switch shape {
	case ShapeSine:
		out := make([]float64, half)
		step := math.Pi / float64(nTransform)
		for i := 0; i < half; i++ {
			out[i] = math.Sin(step * (float64(i) + 0.5))
		}
		return out
	case ShapeKBD:
		alpha := 4.0
		if nTransform == shortTransform {
			alpha = 6.0
		}
		return refKBDHalf(half, alpha)
	default:
		return nil
	}
}

func refKBDHalf(half int, alpha float64) []float64 {
	kernel := make([]float64, half+1)
	quarter := float64(half) / 2.0
	denom := refBesselI0(math.Pi * alpha)
	total := 0.0
	for i := 0; i <= half; i++ {
		x := (float64(i) - quarter) / quarter
		rad := 1 - x*x
		if rad < 0 {
			rad = 0
		}
		kernel[i] = refBesselI0(math.Pi*alpha*math.Sqrt(rad)) / denom
		total += kernel[i]
	}
	out := make([]float64, half)
	running := 0.0
	for i := 0; i < half; i++ {
		running += kernel[i]
		out[i] = math.Sqrt(running / total)
	}
	return out
}

func refBesselI0(x float64) float64 {
	sum := 1.0
	term := 1.0
	halfX := x / 2.0
	for k := 1.0; k <= 256.0; k++ {
		factor := halfX / k
		term *= factor * factor
		sum += term
		if term < 1e-18*sum {
			break
		}
	}
	return sum
}

func TestFFTDirectAcrossSizesAndScales(t *testing.T) {
	for _, n := range []int{shortTransform, longTransform} {
		for _, scale := range []float64{1e-9, 1, 1e6} {
			coeff := make([]float64, n/2)
			for i := range coeff {
				coeff[i] = scale * (math.Sin(float64(i)*0.37) + math.Cos(float64(i)*1.17))
			}
			got := make([]float64, n)
			imdct(coeff, n, got)
			want := refIMDCT(coeff, n)
			assertCloseSlice(t, got, want, math.Max(1e-15, scale*2e-12))
		}
	}
}

func BenchmarkIMDCTReferenceAndFFT(b *testing.B) {
	coeff := coeffPattern(17)
	dst := make([]float64, longTransform)
	b.Run("direct", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = refIMDCT(coeff, longTransform)
		}
	})
	b.Run("FFT", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			imdct(coeff, longTransform, dst)
		}
	})
}
