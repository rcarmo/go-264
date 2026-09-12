// Package resample implements a streaming rational polyphase windowed-sinc FIR.
// It uses zero extension at both boundaries and compensates its symmetric
// filter delay. Finite output length is ceil(inputFrames*outRate/inRate).
package resample

import (
	"context"
	"fmt"
	"io"
	"math"

	"github.com/rcarmo/go-264/audio/convert"
	"github.com/rcarmo/go-264/audio/pcm"
)

const sourceReadFrames = 1024

// Reader resamples one or two interleaved channels. Sequential use only.
// Output is aligned to source time zero. The filter has radius input frames
// of lookahead, but no retained delay in output timestamps. No dither/clipping.
type Reader struct {
	source                                                     convert.Source
	info                                                       pcm.Info
	inRate, outRate, channels, phaseStep, phases, radius, taps int
	ringFrames                                                 int
	ringMask                                                   int64
	coeff, ring, scratch                                       []float64
	sourceFrames                                               int64
	pos, loadedStart, loadedEnd                                int64
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// New accepts 8–192 kHz source rates and 8–48 kHz output rates. Ratios with
// more than 2048 phases are rejected to cap coefficient-table memory.
// Common 8/16/22.05/44.1/48/96/192 kHz ratios are supported.
// source must initially be positioned at frame zero. After New, only this
// Reader may read/seek the source until the Reader is discarded.
func New(source convert.Source, outRate int) (*Reader, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: nil source", pcm.ErrMalformed)
	}
	info := source.Info()
	if info.SampleRate < 8000 || info.SampleRate > 192000 || outRate < 8000 || outRate > 48000 || (info.Channels != 1 && info.Channels != 2) || info.Frames < 0 {
		return nil, fmt.Errorf("%w: resampler format", pcm.ErrUnsupported)
	}
	inRate := info.SampleRate
	g := gcd(inRate, outRate)
	phases := outRate / g
	if phases > 2048 {
		return nil, fmt.Errorf("%w: resampler phase limit", pcm.ErrUnsupported)
	}
	n, err := pcm.OutputFrames(info.Frames, inRate, outRate)
	if err != nil {
		return nil, err
	}
	sourceFrames := info.Frames
	info.SampleRate = outRate
	info.Frames = n
	radius := int(math.Ceil(24 * math.Max(1, float64(inRate)/float64(outRate))))
	taps := 2*radius + 1
	r := &Reader{source: source, info: info, inRate: inRate, outRate: outRate, channels: info.Channels, phases: phases, phaseStep: g, radius: radius, taps: taps, sourceFrames: sourceFrames}
	if inRate == outRate {
		return r, nil
	}
	// Downsampling needs a transition band below destination Nyquist.
	// Upsampling is interpolation: retain input Nyquist so integer phases
	// reproduce the original samples instead of low-passing them a second time.
	// Each exact rational phase has unit DC gain.
	cutoff := 1.0
	if outRate < inRate {
		cutoff = 0.94 * float64(outRate) / float64(inRate)
	}
	// Keep the whole FIR window plus one source-read overshoot. Round up to a
	// power of two; capacity is 2048 or 4096 frames for the supported rates.
	// The same-rate path above needs no coefficient/ring/scratch allocation.
	r.ringFrames = 1
	for r.ringFrames < taps+sourceReadFrames {
		r.ringFrames <<= 1
	}
	r.ringMask = int64(r.ringFrames - 1)
	coeffLen, ringLen := phases*taps, r.ringFrames*r.channels
	storage := make([]float64, coeffLen+ringLen+sourceReadFrames*r.channels)
	r.coeff = storage[:coeffLen:coeffLen]
	r.ring = storage[coeffLen : coeffLen+ringLen : coeffLen+ringLen]
	r.scratch = storage[coeffLen+ringLen:]
	for p := 0; p < phases; p++ {
		frac := float64(p) / float64(phases)
		sum := 0.0
		for j := -radius; j <= radius; j++ {
			x := float64(j) - frac
			a := math.Pi * cutoff * x
			sinc := 1.0
			if a != 0 {
				sinc = math.Sin(a) / a
			}
			w := 0.0
			if math.Abs(x) <= float64(radius) {
				w = 0.42 + 0.5*math.Cos(math.Pi*x/float64(radius)) + 0.08*math.Cos(2*math.Pi*x/float64(radius))
			}
			v := cutoff * sinc * w
			r.coeff[p*taps+j+radius] = v
			sum += v
		}
		for j := 0; j < taps; j++ {
			r.coeff[p*taps+j] /= sum
		}
	}
	return r, nil
}
func (r *Reader) Info() pcm.Info { return r.info }

// LookaheadFrames reports the symmetric filter's source-frame lookahead.
func (r *Reader) LookaheadFrames() int {
	if r.inRate == r.outRate {
		return 0
	}
	return r.radius
}
func (r *Reader) SeekFrame(ctx context.Context, frame int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if frame < 0 || frame > r.info.Frames {
		return fmt.Errorf("%w: seek range", pcm.ErrMalformed)
	}
	center, err := pcm.ScaleFrames(frame, r.inRate, r.outRate)
	if err != nil {
		return err
	}
	start := max(int64(0), center-int64(r.radius))
	if r.inRate == r.outRate {
		start = frame
	}
	if err = r.source.SeekFrame(ctx, start); err != nil {
		return err
	}
	r.pos = frame
	r.loadedStart = start
	r.loadedEnd = start
	return nil
}
func (r *Reader) fill(ctx context.Context, through int64) error {
	through = min(through, r.sourceFrames-1)
	for r.loadedEnd <= through {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.source.ReadFrames(ctx, r.scratch)
		if n < 0 || n > sourceReadFrames || int64(n) > r.sourceFrames-r.loadedEnd {
			return fmt.Errorf("%w: invalid source count", pcm.ErrMalformed)
		}
		start := int(r.loadedEnd&r.ringMask) * r.channels
		first := min(n*r.channels, len(r.ring)-start)
		copy(r.ring[start:start+first], r.scratch[:first])
		copy(r.ring[:n*r.channels-first], r.scratch[first:n*r.channels])
		r.loadedEnd += int64(n)
		r.loadedStart = max(r.loadedStart, r.loadedEnd-int64(r.ringFrames))
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 || err == io.EOF {
			if r.loadedEnd <= through {
				return fmt.Errorf("%w: truncated source PCM", pcm.ErrMalformed)
			}
			break
		}
	}
	return nil
}
func (r *Reader) ReadFrames(ctx context.Context, dst []float64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(dst)%r.channels != 0 {
		return 0, fmt.Errorf("%w: buffer shape", pcm.ErrMalformed)
	}
	if len(dst) == 0 {
		return 0, nil
	}
	if r.pos == r.info.Frames {
		return 0, io.EOF
	}
	if r.inRate == r.outRate {
		n, err := r.source.ReadFrames(ctx, dst)
		r.pos += int64(n)
		return n, err
	}
	want := min(int64(len(dst)/r.channels), r.info.Frames-r.pos)
	for i := 0; i < int(want); i++ {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		center, err := pcm.ScaleFrames(r.pos, r.inRate, r.outRate)
		if err != nil {
			return i, err
		}
		phase := int((r.pos%int64(r.outRate))*int64(r.inRate)%int64(r.outRate)) / r.phaseStep
		if err = r.fill(ctx, center+int64(r.radius)); err != nil {
			return i, err
		}
		// Common mono FIR window: contiguous ring slice, one bounds check,
		// then ordered SIMD products. Keep edges/wrap/stereo on the scalar oracle.
		first, last := center-int64(r.radius), center+int64(r.radius)
		if r.channels == 1 && first >= r.loadedStart && last < r.loadedEnd && first >= 0 && last < r.sourceFrames && first & ^r.ringMask == last & ^r.ringMask {
			start := int(first & r.ringMask)
			dst[i] = dot(r.ring[start:start+r.taps], r.coeff[phase*r.taps:(phase+1)*r.taps])
			r.pos++
			continue
		}
		for c := 0; c < r.channels; c++ {
			sum := 0.0
			for j := -r.radius; j <= r.radius; j++ {
				idx := center + int64(j)
				if idx < 0 || idx >= r.sourceFrames {
					continue
				}
				if idx < r.loadedStart || idx >= r.loadedEnd {
					return i, fmt.Errorf("%w: resampler cache bounds", pcm.ErrMalformed)
				}
				sum += r.ring[int(idx&r.ringMask)*r.channels+c] * r.coeff[phase*r.taps+j+r.radius]
			}
			dst[i*r.channels+c] = sum
		}
		r.pos++
	}
	if want < int64(len(dst)/r.channels) {
		return int(want), io.EOF
	}
	return int(want), nil
}
