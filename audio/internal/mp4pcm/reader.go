// Package mp4pcm composes MP4 demux with AAC-LC or AC-3 decoding. It keeps
// source PCM bounded to one access unit and applies the explicit edit plan.
package mp4pcm

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rcarmo/go-264/audio/aac"
	"github.com/rcarmo/go-264/audio/ac3"
	"github.com/rcarmo/go-264/audio/mp4"
	"github.com/rcarmo/go-264/audio/pcm"
)

type frameDecoder interface {
	Decode(context.Context, []byte, []float64) (int, error)
	Reset()
}

type aacDecoder struct{ *aac.Decoder }

func (d aacDecoder) Decode(ctx context.Context, packet []byte, dst []float64) (int, error) {
	return d.Decoder.Decode(ctx, packet, dst)
}

type ac3Decoder struct{ *ac3.Decoder }

func (d ac3Decoder) Decode(ctx context.Context, packet []byte, dst []float64) (int, error) {
	frames, _, err := d.Decoder.Decode(ctx, packet, dst)
	return frames, err
}

type Reader struct {
	demux        *mp4.Reader
	codec        frameDecoder
	track        mp4.Track
	timing       mp4.Timing
	sourceInfo   pcm.Info
	info         pcm.Info
	packet       []byte
	block        [ac3.FrameSamples * 2]float64
	frameSamples int
	next         int
	loaded       int
	pos          int64
}

type Options struct {
	TrackIndex     *int
	OutputChannels int
	SourceChannels []int
}

func Open(ctx context.Context, src io.ReaderAt, size int64, limits pcm.Limits, options ...Options) (*Reader, error) {
	limits, err := limits.Validated()
	if err != nil {
		return nil, err
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("%w: duplicate MP4 options", pcm.ErrMalformed)
	}
	var option Options
	if len(options) == 1 {
		option = options[0]
	}
	mp4Limits := mp4.Limits{MaxBytes: limits.MaxBytes, MaxDurationSeconds: limits.MaxDurationSeconds, MaxBoxes: limits.MaxChunks}
	var demux *mp4.Reader
	if option.TrackIndex == nil {
		demux, err = mp4.Open(ctx, src, size, mp4Limits)
	} else {
		demux, err = mp4.OpenTrack(ctx, src, size, mp4Limits, *option.TrackIndex)
	}
	if err != nil {
		return nil, err
	}
	track := demux.Track()
	frameSamples := 1024
	if track.Codec == "ac-3" {
		frameSamples = ac3.FrameSamples
	}
	timing, err := track.TimingPlan(int64(track.SampleCount) * int64(frameSamples))
	if err != nil {
		return nil, err
	}
	if timing.OutputFrames/int64(track.SampleRate) > limits.MaxDurationSeconds || (timing.OutputFrames/int64(track.SampleRate) == limits.MaxDurationSeconds && timing.OutputFrames%int64(track.SampleRate) != 0) {
		return nil, fmt.Errorf("%w: edited duration", pcm.ErrLimit)
	}
	maxPacket := 0
	for i := 0; i < track.SampleCount; i++ {
		_, packet, err := demux.ReadPacket(ctx, i, nil)
		if !errors.Is(err, io.ErrShortBuffer) {
			return nil, fmt.Errorf("%w: packet metadata", pcm.ErrMalformed)
		}
		if packet.CompositionOffset != 0 {
			return nil, fmt.Errorf("%w: audio composition offsets", pcm.ErrUnsupported)
		}
		ticks := uint64(packet.Duration) * uint64(track.SampleRate)
		if ticks%uint64(track.MediaTimescale) != 0 {
			return nil, fmt.Errorf("%w: fractional audio packet duration", pcm.ErrUnsupported)
		}
		frames := ticks / uint64(track.MediaTimescale)
		if frames > uint64(frameSamples) || frames == 0 || (i < track.SampleCount-1 && frames != uint64(frameSamples)) {
			return nil, fmt.Errorf("%w: audio packet timeline", pcm.ErrUnsupported)
		}
		maxPacket = max(maxPacket, packet.Size)
	}
	var codec frameDecoder
	channels := track.Channels
	switch track.Codec {
	case "mp4a":
		if option.SourceChannels != nil {
			return nil, fmt.Errorf("%w: AAC source channel extraction", pcm.ErrUnsupported)
		}
		decoder, err := aac.NewDecoder(track.AudioSpecificConfig)
		if err != nil {
			return nil, err
		}
		codec = aacDecoder{decoder}
	case "ac-3":
		channels = option.OutputChannels
		if channels == 0 {
			channels = 1
		}
		decoder, err := ac3.NewDecoder(ac3.Config{OutputChannels: channels, SourceChannels: option.SourceChannels})
		if err != nil {
			return nil, err
		}
		codec = ac3Decoder{decoder}
	default:
		return nil, fmt.Errorf("%w: MP4 audio codec", pcm.ErrUnsupported)
	}
	return &Reader{
		demux: demux, codec: codec, track: track, timing: timing,
		sourceInfo: pcm.Info{SampleRate: track.SampleRate, Channels: track.Channels, Frames: timing.DecodedFrames},
		info:       pcm.Info{SampleRate: track.SampleRate, Channels: channels, Frames: timing.OutputFrames},
		packet:     make([]byte, maxPacket), frameSamples: frameSamples, loaded: -1,
	}, nil
}

func (r *Reader) Info() pcm.Info  { return r.info }
func (r *Reader) TrackIndex() int { return r.track.Index }
func (r *Reader) Metadata() pcm.Metadata {
	return pcm.Metadata{Source: r.sourceInfo, Output: r.info, PrimingFrames: r.timing.PrimingFrames, PaddingFrames: r.timing.PaddingFrames, LeadingSilenceFrames: r.timing.LeadingSilenceFrames}
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
		if err := ctx.Err(); err != nil {
			return err
		}
		n, _, err := r.demux.ReadPacket(ctx, r.next, r.packet)
		if err != nil {
			return err
		}
		frames, err := r.codec.Decode(ctx, r.packet[:n], r.block[:r.info.Channels*r.frameSamples])
		if err != nil {
			return err
		}
		if frames != r.frameSamples {
			return fmt.Errorf("%w: audio output length", pcm.ErrMalformed)
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
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	channels := r.info.Channels
	if len(dst)%channels != 0 {
		return 0, fmt.Errorf("%w: PCM shape", pcm.ErrMalformed)
	}
	if len(dst) == 0 {
		return 0, nil
	}
	want := min(int64(len(dst)/channels), r.info.Frames-r.pos)
	n := 0
	for int64(n) < want {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if r.pos < r.timing.LeadingSilenceFrames {
			count := min(want-int64(n), r.timing.LeadingSilenceFrames-r.pos)
			clear(dst[n*channels : (n+int(count))*channels])
			n += int(count)
			r.pos += count
			continue
		}
		raw := r.pos - r.timing.LeadingSilenceFrames + r.timing.PrimingFrames
		index := int(raw / int64(r.frameSamples))
		offset := int(raw % int64(r.frameSamples))
		if err := r.load(ctx, index); err != nil {
			return n, err
		}
		count := min(int(want)-n, r.frameSamples-offset)
		copy(dst[n*channels:(n+count)*channels], r.block[offset*channels:(offset+count)*channels])
		n += count
		r.pos += int64(count)
	}
	if r.pos == r.info.Frames {
		if err := r.finish(ctx); err != nil {
			return n, err
		}
	}
	if n < len(dst)/channels {
		return n, io.EOF
	}
	return n, nil
}

// SeekFrame changes logical output position only. The next read replays earlier
// access units when necessary so overlap/dither state equals sequential decode.
func (r *Reader) SeekFrame(ctx context.Context, frame int64) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if frame < 0 || frame > r.info.Frames {
		return fmt.Errorf("%w: seek range", pcm.ErrMalformed)
	}
	r.pos = frame
	return nil
}
