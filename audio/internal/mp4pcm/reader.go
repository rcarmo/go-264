// Package mp4pcm composes the public MP4 demux and AAC-LC decoder. It keeps
// source PCM bounded to one access unit and applies the explicit edit plan.
package mp4pcm

import (
	"context"
	"errors"
	"fmt"
	"github.com/rcarmo/go-264/audio/aac"
	"github.com/rcarmo/go-264/audio/mp4"
	"github.com/rcarmo/go-264/audio/pcm"
	"io"
)

type Reader struct {
	demux  *mp4.Reader
	codec  *aac.Decoder
	track  mp4.Track
	timing mp4.Timing
	info   pcm.Info
	packet []byte
	block  [2048]float64
	next   int
	loaded int
	pos    int64
}

func Open(ctx context.Context, src io.ReaderAt, size int64, limits pcm.Limits) (*Reader, error) {
	limits, e := limits.Validated()
	if e != nil {
		return nil, e
	}
	m, e := mp4.Open(ctx, src, size, mp4.Limits{MaxBytes: limits.MaxBytes, MaxDurationSeconds: limits.MaxDurationSeconds, MaxBoxes: limits.MaxChunks})
	if e != nil {
		return nil, e
	}
	track := m.Track()
	timing, e := track.TimingPlan(int64(track.SampleCount) * 1024)
	if e != nil {
		return nil, e
	}
	if timing.OutputFrames/int64(track.SampleRate) > limits.MaxDurationSeconds || (timing.OutputFrames/int64(track.SampleRate) == limits.MaxDurationSeconds && timing.OutputFrames%int64(track.SampleRate) != 0) {
		return nil, fmt.Errorf("%w: edited duration", pcm.ErrLimit)
	}
	maxPacket := 0
	for i := 0; i < track.SampleCount; i++ {
		_, p, e := m.ReadPacket(ctx, i, nil)
		if !errors.Is(e, io.ErrShortBuffer) {
			return nil, fmt.Errorf("%w: packet metadata", pcm.ErrMalformed)
		}
		if p.CompositionOffset != 0 {
			return nil, fmt.Errorf("%w: AAC composition offsets", pcm.ErrUnsupported)
		}
		// All but the last AAC AU span exactly1024 source frames. The last stts
		// duration may be shorter to express end padding; no interior gaps/overlap.
		ticks := uint64(p.Duration) * uint64(track.SampleRate)
		if ticks%uint64(track.MediaTimescale) != 0 {
			return nil, fmt.Errorf("%w: fractional AAC packet duration", pcm.ErrUnsupported)
		}
		frames := ticks / uint64(track.MediaTimescale)
		if frames > 1024 || frames == 0 || (i < track.SampleCount-1 && frames != 1024) {
			return nil, fmt.Errorf("%w: AAC packet timeline", pcm.ErrUnsupported)
		}
		maxPacket = max(maxPacket, p.Size)
	}
	codec, e := aac.NewDecoder(track.AudioSpecificConfig)
	if e != nil {
		return nil, e
	}
	return &Reader{demux: m, codec: codec, track: track, timing: timing, info: pcm.Info{SampleRate: track.SampleRate, Channels: track.Channels, Frames: timing.OutputFrames}, packet: make([]byte, maxPacket), loaded: -1}, nil
}
func (r *Reader) Info() pcm.Info { return r.info }
func (r *Reader) Metadata() pcm.Metadata {
	return pcm.Metadata{Source: pcm.Info{SampleRate: r.info.SampleRate, Channels: r.info.Channels, Frames: r.timing.DecodedFrames}, Output: r.info, PrimingFrames: r.timing.PrimingFrames, PaddingFrames: r.timing.PaddingFrames, LeadingSilenceFrames: r.timing.LeadingSilenceFrames}
}
func (r *Reader) load(ctx context.Context, index int) error {
	if index == r.loaded {
		return nil
	}
	if index < r.next {
		r.codec.Reset()
		r.next = 0
		r.loaded = -1
	}
	for r.next <= index {
		if e := ctx.Err(); e != nil {
			return e
		}
		n, _, e := r.demux.ReadPacket(ctx, r.next, r.packet)
		if e != nil {
			return e
		}
		frames, e := r.codec.Decode(ctx, r.packet[:n], r.block[:r.info.Channels*1024])
		if e != nil {
			return e
		}
		if frames != 1024 {
			return fmt.Errorf("%w: AAC output length", pcm.ErrMalformed)
		}
		r.loaded = r.next
		r.next++
	}
	return nil
}
func (r *Reader) finish(ctx context.Context) error {
	if r.next < r.track.SampleCount {
		return r.load(ctx, r.track.SampleCount-1)
	}
	return nil
}
func (r *Reader) ReadFrames(ctx context.Context, dst []float64) (int, error) {
	if ctx == nil {
		return 0, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	ch := r.info.Channels
	if len(dst)%ch != 0 {
		return 0, fmt.Errorf("%w: PCM shape", pcm.ErrMalformed)
	}
	if len(dst) == 0 {
		return 0, nil
	}
	want := int64(len(dst) / ch)
	want = min(want, r.info.Frames-r.pos)
	n := 0
	for int64(n) < want {
		if e := ctx.Err(); e != nil {
			return n, e
		}
		if r.pos < r.timing.LeadingSilenceFrames {
			count := min(want-int64(n), r.timing.LeadingSilenceFrames-r.pos)
			clear(dst[n*ch : (n+int(count))*ch])
			n += int(count)
			r.pos += count
			continue
		}
		raw := r.pos - r.timing.LeadingSilenceFrames + r.timing.PrimingFrames
		index := int(raw / 1024)
		offset := int(raw % 1024)
		if e := r.load(ctx, index); e != nil {
			return n, e
		}
		count := min(int(want)-n, 1024-offset)
		copy(dst[n*ch:(n+count)*ch], r.block[offset*ch:(offset+count)*ch])
		n += count
		r.pos += int64(count)
	}
	if r.pos == r.info.Frames {
		if e := r.finish(ctx); e != nil {
			return n, e
		}
	}
	if n < len(dst)/ch {
		return n, io.EOF
	}
	return n, nil
}

// SeekFrame changes logical output position only. The next read replays earlier
// AUs when necessary so PNS and overlap state equal a sequential decode. It is
// exact but may be O(stream length); cancellation is checked per replayed AU.
func (r *Reader) SeekFrame(ctx context.Context, frame int64) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if frame < 0 || frame > r.info.Frames {
		return fmt.Errorf("%w: seek range", pcm.ErrMalformed)
	}
	r.pos = frame
	return nil
}
