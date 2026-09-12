// Package convert provides deterministic PCM conversion without model policy.
package convert

import (
	"context"
	"fmt"
	"math"

	"github.com/rcarmo/go-264/audio/pcm"
)

// Source yields interleaved normalised PCM and returns frame counts.
// Implementations are sequential, not safe for concurrent reads/seeks.
type Source interface {
	Info() pcm.Info
	ReadFrames(context.Context, []float64) (int, error)
	SeekFrame(context.Context, int64) error
}

// S16 clips to [-32768,32767] and rounds ties away from zero. No dither is
// applied. Non-finite input is rejected, never silently converted to silence.
func S16(dst []int16, src []float64) error {
	if len(dst) < len(src) {
		return fmt.Errorf("%w: short conversion buffer", pcm.ErrMalformed)
	}
	for _, v := range src {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: non-finite PCM", pcm.ErrMalformed)
		}
	}
	for i, v := range src {
		v = math.Round(v * 32768)
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		dst[i] = int16(v)
	}
	return nil
}

// Interleave converts planar samples with equal plane lengths to interleaved
// PCM. Buffers must not overlap. Only explicit mono/stereo layouts are accepted.
func Interleave(dst []float64, planes [][]float64) error {
	if len(planes) < 1 || len(planes) > 2 {
		return fmt.Errorf("%w: channel layout", pcm.ErrUnsupported)
	}
	n := len(planes[0])
	if len(dst)/len(planes) != n || len(dst)%len(planes) != 0 {
		return fmt.Errorf("%w: buffer shape", pcm.ErrMalformed)
	}
	for _, p := range planes {
		if len(p) != n {
			return fmt.Errorf("%w: plane length", pcm.ErrMalformed)
		}
	}
	for f := 0; f < n; f++ {
		for c, p := range planes {
			dst[f*len(planes)+c] = p[f]
		}
	}
	return nil
}

// Reader preserves a source layout or applies stereo-to-mono (L+R)/2.
// Mono-to-stereo duplicates the sample. Other layouts are rejected.
type Reader struct {
	source  Source
	info    pcm.Info
	scratch [4096]float64
}

func New(source Source, channels int) (*Reader, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: nil source", pcm.ErrMalformed)
	}
	info := source.Info()
	if (info.Channels != 1 && info.Channels != 2) || (channels != 1 && channels != 2) {
		return nil, fmt.Errorf("%w: channel layout", pcm.ErrUnsupported)
	}
	info.Channels = channels
	return &Reader{source: source, info: info}, nil
}
func (r *Reader) Info() pcm.Info                               { return r.info }
func (r *Reader) SeekFrame(ctx context.Context, n int64) error { return r.source.SeekFrame(ctx, n) }
func (r *Reader) ReadFrames(ctx context.Context, dst []float64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(dst)%r.info.Channels != 0 {
		return 0, fmt.Errorf("%w: buffer shape", pcm.ErrMalformed)
	}
	inCh := r.source.Info().Channels
	if inCh == r.info.Channels {
		return r.source.ReadFrames(ctx, dst)
	}
	total := 0
	for total < len(dst)/r.info.Channels {
		want := min(len(dst)/r.info.Channels-total, len(r.scratch)/inCh)
		n, err := r.source.ReadFrames(ctx, r.scratch[:want*inCh])
		if n < 0 || n > want {
			return total, fmt.Errorf("%w: source frame count", pcm.ErrMalformed)
		}
		for i := 0; i < n; i++ {
			if inCh == 2 {
				dst[total+i] = (r.scratch[2*i] + r.scratch[2*i+1]) * 0.5
			} else {
				dst[2*(total+i)] = r.scratch[i]
				dst[2*(total+i)+1] = r.scratch[i]
			}
		}
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, fmt.Errorf("%w: source made no progress", pcm.ErrMalformed)
		}
	}
	return total, nil
}
