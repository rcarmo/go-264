// Package audio exposes a pure-Go, audio-only PCM frontend.
// Current implementation supports PCM RIFF/WAVE and a narrow progressive
// MP4/AAC-LC mono/stereo subset with explicit integral edit-list handling.
package audio

import (
	"context"
	"fmt"
	"io"

	"github.com/rcarmo/go-264/audio/convert"
	"github.com/rcarmo/go-264/audio/internal/mp4pcm"
	"github.com/rcarmo/go-264/audio/pcm"
	"github.com/rcarmo/go-264/audio/resample"
	"github.com/rcarmo/go-264/audio/spool"
	"github.com/rcarmo/go-264/audio/wav"
)

// Options configure canonical signed 16-bit output. Zero selects 16 kHz mono.
// Source is caller-owned and must remain immutable and readable until Close.
type Options struct {
	TargetRate     int
	TargetChannels int
	Limits         pcm.Limits
}

// Decoder owns bounded internal scratch, but not the source or caller buffers.
// Reads/seeks/Close are sequential and must not run concurrently.
type directS16Source interface {
	convert.Source
	ReadS16Frames(context.Context, []int16) (int, error)
}

type Decoder struct {
	source    convert.Source
	directS16 directS16Source
	meta      pcm.Metadata
	pos       int64
	closed    bool
	owned     io.Closer // only OpenStream's temporary spool; never the caller's input
	scratch   []float64
}

// Probe validates container metadata without decoding PCM. MP4 returns the
// edited source-rate frame count derived from validated packet tables; actual
// AAC payload validity is checked during reads.
// Unsupported container signatures return a typed error, independent of names.
func Probe(ctx context.Context, src io.ReaderAt, size int64, limits pcm.Limits) (pcm.Info, error) {
	r, err := openSource(ctx, src, size, limits)
	if err != nil {
		return pcm.Info{}, err
	}
	return r.Info(), nil
}

func openSource(ctx context.Context, src io.ReaderAt, size int64, limits pcm.Limits) (convert.Source, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if src == nil || size < 12 {
		return nil, fmt.Errorf("%w: missing source/header", pcm.ErrMalformed)
	}
	limits, err := limits.Validated()
	if err != nil {
		return nil, err
	}
	if size > limits.MaxBytes {
		return nil, fmt.Errorf("%w: source size", pcm.ErrLimit)
	}
	var b [12]byte
	if _, err = io.ReadFull(io.NewSectionReader(src, 0, 12), b[:]); err != nil {
		return nil, fmt.Errorf("%w: header: %w", pcm.ErrMalformed, err)
	}
	if string(b[:4]) == "RIFF" && string(b[8:]) == "WAVE" {
		return wav.Open(ctx, src, size, limits)
	}
	switch string(b[4:8]) {
	case "ftyp", "moov", "mdat", "free", "wide", "skip":
		return mp4pcm.Open(ctx, src, size, limits)
	}
	return nil, fmt.Errorf("%w: container", pcm.ErrUnsupported)
}

func Open(ctx context.Context, src io.ReaderAt, size int64, opts Options) (*Decoder, error) {
	raw, err := openSource(ctx, src, size, opts.Limits)
	if err != nil {
		return nil, err
	}
	if opts.TargetRate == 0 {
		opts.TargetRate = 16000
	}
	if opts.TargetChannels == 0 {
		opts.TargetChannels = 1
	}
	if opts.TargetRate < 8000 || opts.TargetRate > 48000 || (opts.TargetChannels != 1 && opts.TargetChannels != 2) {
		return nil, fmt.Errorf("%w: output format", pcm.ErrUnsupported)
	}
	meta := pcm.Metadata{Source: raw.Info()}
	if provider, ok := raw.(interface{ Metadata() pcm.Metadata }); ok {
		meta = provider.Metadata()
	}
	if direct, ok := raw.(directS16Source); ok && raw.Info().BitsPerSample == 16 &&
		raw.Info().SampleRate == opts.TargetRate && raw.Info().Channels == opts.TargetChannels {
		meta.Output = raw.Info()
		meta.Output.BitsPerSample = 16
		return &Decoder{source: raw, directS16: direct, meta: meta}, nil
	}
	if raw.Info().SampleRate == opts.TargetRate && raw.Info().Channels == opts.TargetChannels {
		meta.Output = raw.Info()
		meta.Output.BitsPerSample = 16
		return &Decoder{source: raw, meta: meta, scratch: make([]float64, 4096)}, nil
	}
	mix, err := convert.New(raw, opts.TargetChannels)
	if err != nil {
		return nil, err
	}
	rs, err := resample.New(mix, opts.TargetRate)
	if err != nil {
		return nil, err
	}
	meta.Output = rs.Info()
	meta.Output.BitsPerSample = 16
	return &Decoder{source: rs, meta: meta, scratch: make([]float64, 4096)}, nil
}

// OpenStream makes a bounded temporary random-access copy, then opens it.
// opts.Limits.MaxBytes also caps spool disk usage. dir must be caller-owned.
// Close removes the spool; the original reader is never closed or removed.
func OpenStream(ctx context.Context, src io.Reader, dir string, opts Options) (*Decoder, error) {
	limits, err := opts.Limits.Validated()
	if err != nil {
		return nil, err
	}
	retained, err := spool.Copy(ctx, src, dir, limits.MaxBytes)
	if err != nil {
		return nil, err
	}
	d, err := Open(ctx, retained, retained.Size(), opts)
	if err != nil {
		_ = retained.Close()
		return nil, err
	}
	d.owned = retained
	return d, nil
}

func (d *Decoder) Metadata() pcm.Metadata { return d.meta }

// ReadPCM fills caller-owned interleaved S16 samples and returns a scalar sample
// count (NOT a frame count). span uses frame units. n>0 with EOF/error is valid;
// callers must consume these n samples before handling err. A cancelled read
// preserves all emitted output and can continue with a fresh context.
func (d *Decoder) ReadPCM(ctx context.Context, dst []int16) (n int, span pcm.Span, err error) {
	span.StartFrame = d.pos
	span.SourceStartFrame, _ = pcm.ScaleFrames(d.pos, d.meta.Source.SampleRate, d.meta.Output.SampleRate)
	if span.SourceStartFrame < d.meta.LeadingSilenceFrames {
		span.SourceStartFrame = -1
		span.SourcePadding = true
	} else {
		span.SourceStartFrame += d.meta.PrimingFrames - d.meta.LeadingSilenceFrames
	}
	if d.closed {
		return 0, span, pcm.ErrClosed
	}
	if ctx == nil {
		return 0, span, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err = ctx.Err(); err != nil {
		return 0, span, err
	}
	ch := d.meta.Output.Channels
	if len(dst)%ch != 0 {
		return 0, span, fmt.Errorf("%w: output buffer shape", pcm.ErrMalformed)
	}
	for n < len(dst) {
		want := len(dst) - n
		if d.directS16 == nil {
			want = min(want, len(d.scratch))
		}
		want -= want % ch
		if d.directS16 != nil {
			frames, e := d.directS16.ReadS16Frames(ctx, dst[n:n+want])
			if frames < 0 || frames > want/ch {
				return n, span, fmt.Errorf("%w: invalid source count", pcm.ErrMalformed)
			}
			count := frames * ch
			n += count
			d.pos += int64(frames)
			span.Frames += int64(frames)
			if e != nil {
				return n, span, e
			}
			if frames == 0 {
				return n, span, io.ErrNoProgress
			}
			continue
		}
		frames, e := d.source.ReadFrames(ctx, d.scratch[:want])
		if frames < 0 || frames > want/ch {
			return n, span, fmt.Errorf("%w: invalid source count", pcm.ErrMalformed)
		}
		count := frames * ch
		if ce := convert.S16(dst[n:n+count], d.scratch[:count]); ce != nil {
			return n, span, ce
		}
		n += count
		d.pos += int64(frames)
		span.Frames += int64(frames)
		if e != nil {
			return n, span, e
		}
		if frames == 0 {
			return n, span, io.ErrNoProgress
		}
	}
	return n, span, nil
}

// Seek selects an exact canonical output frame, recreating filter history from
// source samples. EOF frame is valid. The source must support exact seeking.
func (d *Decoder) Seek(ctx context.Context, frame int64) error {
	if d.closed {
		return pcm.ErrClosed
	}
	if ctx == nil {
		return fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := d.source.SeekFrame(ctx, frame); err != nil {
		return err
	}
	d.pos = frame
	return nil
}

// Close releases internal references and any OpenStream-owned spool.
// It never closes the caller's source. A failed spool unlink can be retried.
func (d *Decoder) Close() error {
	d.closed = true
	d.source = nil
	d.directS16 = nil
	if d.owned != nil {
		if err := d.owned.Close(); err != nil {
			return err
		}
		d.owned = nil
	}
	return nil
}
