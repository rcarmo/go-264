// Package pcm defines audio-only sample metadata, resource limits and errors.
// Frame counts count one sample per channel; buffer lengths count scalar samples.
package pcm

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrUnsupported = errors.New("audio: unsupported")
	ErrMalformed   = errors.New("audio: malformed")
	ErrLimit       = errors.New("audio: resource limit")
	ErrClosed      = errors.New("audio: closed")
)

// Info describes interleaved PCM. Samples are signed normalised float64 in
// low-level decoder APIs; the high-level audio API returns signed 16-bit PCM.
type Info struct {
	SampleRate    int
	Channels      int
	Frames        int64
	BitsPerSample int
}

// Metadata preserves source and output frame counts. Source.Frames is the
// decoded count before priming and padding removal. Rates define exact rational
// time mapping; neither timestamp smoothing nor model features belong here.
type Metadata struct {
	Source               Info
	Output               Info
	PrimingFrames        int64
	PaddingFrames        int64
	ResamplerDelayFrames int64
	LeadingSilenceFrames int64 // source-rate silence inserted by leading empty edit
}

// Span describes a contiguous output range. StartFrame is inclusive.
// SourceStartFrame is the floor of its rational source-frame coordinate.
type Span struct {
	StartFrame       int64
	Frames           int64
	SourceStartFrame int64
	SourcePadding    bool // true when span starts in leading edit silence; SourceStartFrame is -1
}

// Limits are checked before allocations or expansion. Zero selects defaults.
// Negative limits are invalid, rather than disabling a limit.
type Limits struct {
	MaxBytes           int64
	MaxDurationSeconds int64
	MaxChunks          int
}

func (l Limits) Validated() (Limits, error) {
	if l.MaxBytes < 0 || l.MaxDurationSeconds < 0 || l.MaxChunks < 0 {
		return l, fmt.Errorf("%w: negative limits", ErrLimit)
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = 512 << 20
	}
	if l.MaxDurationSeconds == 0 {
		l.MaxDurationSeconds = 4 * 60 * 60
	}
	if l.MaxChunks == 0 {
		l.MaxChunks = 65536
	}
	return l, nil
}

// ScaleFrames returns floor(frames*num/den) with checked integer arithmetic.
func ScaleFrames(frames int64, num, den int) (int64, error) {
	if frames < 0 || num <= 0 || den <= 0 {
		return 0, fmt.Errorf("%w: invalid frame ratio", ErrMalformed)
	}
	q, r := frames/int64(den), frames%int64(den)
	if q > math.MaxInt64/int64(num) || r > math.MaxInt64/int64(num) {
		return 0, fmt.Errorf("%w: frame ratio overflow", ErrLimit)
	}
	a, b := q*int64(num), r*int64(num)/int64(den)
	if a > math.MaxInt64-b {
		return 0, fmt.Errorf("%w: frame count overflow", ErrLimit)
	}
	return a + b, nil
}

// OutputFrames returns ceil(frames*outRate/inRate), the finite-signal length
// convention used by the delay-compensated resampler.
func OutputFrames(frames int64, inRate, outRate int) (int64, error) {
	n, err := ScaleFrames(frames, outRate, inRate)
	if err != nil {
		return 0, err
	}
	// Remainder is bounded because accepted rates are positive int values.
	r := frames % int64(inRate)
	if r > math.MaxInt64/int64(outRate) {
		return 0, fmt.Errorf("%w: frame remainder overflow", ErrLimit)
	}
	if r*int64(outRate)%int64(inRate) != 0 {
		if n == math.MaxInt64 {
			return 0, fmt.Errorf("%w: frame ceiling overflow", ErrLimit)
		}
		n++
	}
	return n, nil
}
