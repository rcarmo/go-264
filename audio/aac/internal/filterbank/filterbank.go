// Package filterbank implements the AAC-LC 1024/128 synthesis filterbank with
// amd64 SSE2 kernels and a scalar operation-order reference/purego fallback.
//
// Ported from the MIT-licensed oxideav-aac reference:
//
//	tmp/go264-mit/oxideav-aac/src/filterbank.rs
//	commit 7dcb2f4a9e6f7ccfa6b199342aeb95861dc57885
//
// Copyright (c) 2026 Karpelès Lab Inc.
// SPDX-License-Identifier: MIT
package filterbank

import (
	"fmt"
	"math"

	"github.com/rcarmo/go-264/audio/pcm"
)

const (
	// SequenceOnlyLong maps AAC window_sequence ONLY_LONG_SEQUENCE.
	SequenceOnlyLong = 0
	// SequenceLongStart maps AAC window_sequence LONG_START_SEQUENCE.
	SequenceLongStart = 1
	// SequenceEightShort maps AAC window_sequence EIGHT_SHORT_SEQUENCE.
	SequenceEightShort = 2
	// SequenceLongStop maps AAC window_sequence LONG_STOP_SEQUENCE.
	SequenceLongStop = 3

	// ShapeSine maps AAC window_shape SINE.
	ShapeSine = 0
	// ShapeKBD maps AAC window_shape KBD.
	ShapeKBD = 1
)

const (
	coeffCount      = 1024
	outputSamples   = 1024
	longTransform   = 2048
	shortTransform  = 256
	shortCoeffCount = 128
	shortWindows    = 8
	shortStart      = 448
	shortHop        = 128
)

var (
	sineLongHalf  = makeSineHalf(longTransform / 2)
	sineShortHalf = makeSineHalf(shortTransform / 2)
	kbdLongHalf   = makeKBDHalf(longTransform/2, 4.0)
	kbdShortHalf  = makeKBDHalf(shortTransform/2, 6.0)
)

// Bank is a stateful per-channel synthesis filterbank.
//
// Calls are sequential and stateful: each synthesis keeps the previous overlap
// tail and previous window shape for the next frame.
type Bank struct {
	overlap   [outputSamples]float64
	prevShape int
}

// New returns a fresh AAC-LC 1024/128 synthesis bank.
func New() *Bank {
	return &Bank{prevShape: -1}
}

// Reset clears overlap and previous-shape state.
func (b *Bank) Reset() {
	if b == nil {
		return
	}
	for i := range b.overlap {
		b.overlap[i] = 0
	}
	b.prevShape = -1
}

// Synthesize writes one 1024-sample AAC-LC output frame into dst.
//
// coeff must contain exactly 1024 spectral coefficients. dst must provide at
// least 1024 samples; any dst tail beyond 1024 is left untouched.
//
// sequence values: ONLY_LONG=0, LONG_START=1, EIGHT_SHORT=2, LONG_STOP=3.
// shape values: SINE=0, KBD=1.
//
// Normalization follows the pinned MIT reference and ISO/IEC 14496-3 §4.6.11:
// the IMDCT itself carries the full 2/N factor, and the synthesis window and
// overlap-add apply after the transform. Validation completes before dst or the
// bank state are mutated.
func (b *Bank) Synthesize(coeff []float64, sequence int, shape int, dst []float64) error {
	if b == nil {
		return fmt.Errorf("%w: nil AAC filterbank", pcm.ErrMalformed)
	}
	if len(coeff) != coeffCount {
		return fmt.Errorf("%w: AAC filterbank coeff length %d, want %d", pcm.ErrMalformed, len(coeff), coeffCount)
	}
	if len(dst) < outputSamples {
		return fmt.Errorf("%w: AAC filterbank dst length %d, want at least %d", pcm.ErrMalformed, len(dst), outputSamples)
	}
	if sequence < SequenceOnlyLong || sequence > SequenceLongStop {
		return fmt.Errorf("%w: AAC filterbank sequence %d", pcm.ErrMalformed, sequence)
	}
	if shape < ShapeSine || shape > ShapeKBD {
		return fmt.Errorf("%w: AAC filterbank shape %d", pcm.ErrMalformed, shape)
	}
	for i, v := range coeff {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: AAC filterbank coeff[%d] is not finite", pcm.ErrMalformed, i)
		}
	}

	leftShape := shape
	if b.prevShape >= 0 {
		leftShape = b.prevShape
	}

	var z [longTransform]float64
	switch sequence {
	case SequenceOnlyLong:
		b.synthesizeLong(coeff, leftShape, shape, sequence, &z)
	case SequenceLongStart:
		b.synthesizeLong(coeff, leftShape, shape, sequence, &z)
	case SequenceEightShort:
		b.synthesizeEightShort(coeff, leftShape, shape, &z)
	case SequenceLongStop:
		b.synthesizeLong(coeff, leftShape, shape, sequence, &z)
	}

	var out [outputSamples]float64
	var nextOverlap [outputSamples]float64
	addOverlap(out[:], z[:outputSamples], b.overlap[:])
	for i := 0; i < outputSamples; i++ {
		if math.IsNaN(out[i]) || math.IsInf(out[i], 0) {
			return fmt.Errorf("%w: AAC filterbank output[%d] is not finite", pcm.ErrMalformed, i)
		}
		nextOverlap[i] = z[outputSamples+i]
		if math.IsNaN(nextOverlap[i]) || math.IsInf(nextOverlap[i], 0) {
			return fmt.Errorf("%w: non-finite AAC overlap", pcm.ErrMalformed)
		}
	}

	copy(dst[:outputSamples], out[:])
	b.overlap = nextOverlap
	b.prevShape = shape
	return nil
}

func (b *Bank) synthesizeLong(coeff []float64, leftShape int, rightShape int, sequence int, z *[longTransform]float64) {
	var x [longTransform]float64
	imdct(coeff, longTransform, x[:])

	leftLong := halfWindow(leftShape, false)
	rightLong := halfWindow(rightShape, false)
	switch sequence {
	case SequenceOnlyLong, SequenceLongStart:
		applyWindow(z[:outputSamples], x[:outputSamples], leftLong, false, false)
	case SequenceLongStop:
		end := shortStart + shortCoeffCount
		applyWindow(z[shortStart:end], x[shortStart:end], halfWindow(leftShape, true), false, false)
		copy(z[end:outputSamples], x[end:outputSamples])
	}

	switch sequence {
	case SequenceOnlyLong, SequenceLongStop:
		applyWindow(z[outputSamples:], x[outputSamples:], rightLong, true, false)
	case SequenceLongStart:
		start := outputSamples + shortStart
		copy(z[outputSamples:start], x[outputSamples:start])
		applyWindow(z[start:start+shortCoeffCount], x[start:start+shortCoeffCount], halfWindow(rightShape, true), true, false)
	}
}

func (b *Bank) synthesizeEightShort(coeff []float64, leftShape int, rightShape int, z *[longTransform]float64) {
	rightShort := halfWindow(rightShape, true)
	leftFirst := halfWindow(leftShape, true)
	for j := 0; j < shortWindows; j++ {
		var x [shortTransform]float64
		start := j * shortCoeffCount
		imdct(coeff[start:start+shortCoeffCount], shortTransform, x[:])
		leftShort := rightShort
		if j == 0 {
			leftShort = leftFirst
		}
		base := shortStart + j*shortHop
		applyWindow(z[base:base+shortCoeffCount], x[:shortCoeffCount], leftShort, false, true)
		applyWindow(z[base+shortCoeffCount:base+shortTransform], x[shortCoeffCount:], rightShort, true, true)
	}
}

func halfWindow(shape int, short bool) []float64 {
	switch shape {
	case ShapeSine:
		if short {
			return sineShortHalf
		}
		return sineLongHalf
	case ShapeKBD:
		if short {
			return kbdShortHalf
		}
		return kbdLongHalf
	default:
		return nil
	}
}

func makeSineHalf(half int) []float64 {
	out := make([]float64, half)
	nTransform := float64(half * 2)
	step := math.Pi / nTransform
	for i := 0; i < half; i++ {
		out[i] = math.Sin(step * (float64(i) + 0.5))
	}
	return out
}

func makeKBDHalf(half int, alpha float64) []float64 {
	kernel := makeKBDKernel(half, alpha)
	total := 0.0
	for i := 0; i < len(kernel); i++ {
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

func makeKBDKernel(half int, alpha float64) []float64 {
	out := make([]float64, half+1)
	quarter := float64(half) / 2.0
	denom := besselI0(math.Pi * alpha)
	for i := 0; i <= half; i++ {
		t := (float64(i) - quarter) / quarter
		rad := 1.0 - t*t
		if rad < 0 {
			rad = 0
		}
		out[i] = besselI0(math.Pi*alpha*math.Sqrt(rad)) / denom
	}
	return out
}

func besselI0(x float64) float64 {
	halfX := x / 2.0
	term := 1.0
	sum := 1.0
	for k := 1.0; k <= 256.0; k++ {
		step := halfX / k
		term *= step * step
		sum += term
		if term <= sum*1e-18 {
			break
		}
	}
	return sum
}
