package aac

import (
	"context"
	"fmt"
	"math"

	"github.com/rcarmo/go-264/audio/aac/internal/filterbank"
	"github.com/rcarmo/go-264/audio/aac/internal/lc"
	"github.com/rcarmo/go-264/audio/pcm"
)

// Options selects the decoded PCM layout.
type Options struct {
	// OutputChannels zero, or Config.Channels, keeps every source channel in
	// native Layout order. Sources with three or more channels also accept 2,
	// a fixed ITU-R BS.775 stereo downmix, or 1, its (L+R)/2 fold-down. The
	// downmix adds front and outside-front channels at unity, centre and
	// surround channels at -3 dB (1/sqrt(2)), drops LFE
	// and scales by 1/max(1, sum of gains per output) so it cannot clip, as
	// AC-3 does with its default mix levels. AAC channel configurations 1–7
	// carry no downmix metadata outside a PCE. Mono/stereo sources keep their
	// native layout; audio/convert owns mono/stereo conversion.
	OutputChannels int
}

// Decoder reconstructs a narrow AAC-LC raw access-unit subset. It is sequential;
// one successful Decode produces 1024 frames. Source/container priming, padding
// and edit lists are caller responsibilities. This API is pre-release.
type Decoder struct {
	cfg         Config
	output      int
	slots       [MaxChannels]int // coded channel -> native Layout index
	left, right [MaxChannels]float64
	scale       float64
	banks       []filterbank.Bank
	random      uint32
}

// NewDecoder returns a decoder producing the native channel layout.
func NewDecoder(config []byte) (*Decoder, error) {
	return NewDecoderWithOptions(config, Options{})
}

// NewDecoderWithOptions returns a decoder producing the selected layout.
func NewDecoderWithOptions(config []byte, opts Options) (*Decoder, error) {
	c, e := ParseConfig(config)
	if e != nil {
		return nil, e
	}
	output := opts.OutputChannels
	if output == 0 {
		output = c.Channels
	}
	if output != c.Channels && (c.Channels < 3 || (output != 1 && output != 2)) {
		return nil, fmt.Errorf("%w: AAC output channels %d for %d-channel source", pcm.ErrUnsupported, opts.OutputChannels, c.Channels)
	}
	coded := codedLayouts[c.ChannelConfig]
	d := &Decoder{cfg: c, output: output, slots: outputSlots(coded), banks: make([]filterbank.Bank, c.Channels)}
	d.left, d.right, d.scale = downmixGains(coded)
	d.Reset()
	return d, nil
}

// downmixGains returns per-coded-channel stereo gains and their normalisation.
func downmixGains(coded []Channel) (left, right [MaxChannels]float64, scale float64) {
	const minus3dB = 1 / math.Sqrt2
	var sumLeft, sumRight float64
	for c, role := range coded {
		switch role {
		case FrontLeft, FrontLeftOfCenter:
			left[c] = 1
		case FrontRight, FrontRightOfCenter:
			right[c] = 1
		case FrontCenter, BackCenter:
			left[c], right[c] = minus3dB, minus3dB
		case BackLeft, SideLeft:
			left[c] = minus3dB
		case BackRight, SideRight:
			right[c] = minus3dB
		}
		sumLeft += left[c]
		sumRight += right[c]
	}
	return left, right, 1 / math.Max(1, math.Max(sumLeft, sumRight))
}

func (d *Decoder) Config() Config { return d.cfg }

// OutputChannels returns the interleaved channel count written per frame.
func (d *Decoder) OutputChannels() int { return d.output }

func (d *Decoder) Reset() {
	for i := range d.banks {
		d.banks[i].Reset()
	}
	d.random = 0x1f2e3d4c
}

// Decode writes normalised interleaved float64 PCM to dst and returns FRAMES.
// dst must hold 1024*OutputChannels scalars. Native output interleaves the
// channels in Layout(Config().ChannelConfig) order; a stereo downmix writes
// FL, FR. Errors leave dst and decoder state intact;
// all parsed/synthesised work commits only after validation and cancellation.
// No implicit final overlap frame is emitted: a container's sample count/trim
// plan determines which coded frames and padding are required.
func (d *Decoder) Decode(ctx context.Context, packet []byte, dst []float64) (int, error) {
	if d == nil || ctx == nil || d.cfg.FrameSamples != 1024 || d.cfg.Channels < 1 || d.cfg.Channels > MaxChannels || len(d.banks) != d.cfg.Channels || d.output < 1 {
		return 0, fmt.Errorf("%w: decoder/context", pcm.ErrMalformed)
	}
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if len(dst) < 1024*d.output {
		return 0, fmt.Errorf("%w: AAC output buffer", pcm.ErrMalformed)
	}
	// Separate frames prevent the compiler reserving eight-channel scratch
	// for every mono/stereo call despite the conditional declarations.
	if d.cfg.Channels <= 2 {
		return d.decodeMonoStereo(ctx, packet, dst)
	}
	return d.decodeMultichannel(ctx, packet, dst)
}

func (d *Decoder) decodeMonoStereo(ctx context.Context, packet []byte, dst []float64) (int, error) {
	var banks [2]filterbank.Bank
	var planar [2][1024]float64
	return d.decode(ctx, packet, dst, banks[:d.cfg.Channels], planar[:d.cfg.Channels])
}

func (d *Decoder) decodeMultichannel(ctx context.Context, packet []byte, dst []float64) (int, error) {
	var banks [MaxChannels]filterbank.Bank
	var planar [MaxChannels][1024]float64
	return d.decode(ctx, packet, dst, banks[:d.cfg.Channels], planar[:d.cfg.Channels])
}

// decode parses, reconstructs and synthesises one element at a time into
// working copies of the filterbanks, committing only after the whole block.
func (d *Decoder) decode(ctx context.Context, packet []byte, dst []float64, banks []filterbank.Bank, planar [][1024]float64) (int, error) {
	block, e := lc.NewBlock(packet, d.cfg.SampleRate, d.cfg.ChannelConfig)
	if e != nil {
		return 0, e
	}
	copy(banks, d.banks)
	random := d.random
	var element lc.Frame
	for first := 0; block.Remaining() > 0; first += element.Count {
		if e = block.Next(&element); e != nil {
			return 0, e
		}
		// Validate END before reconstructing the final element, preserving the
		// parse-before-reconstruct error order of single-element blocks.
		if block.Remaining() == 0 {
			if e = block.Finish(); e != nil {
				return 0, e
			}
		}
		if first+element.Count > len(banks) {
			return 0, fmt.Errorf("%w: AAC channel layout", pcm.ErrMalformed)
		}
		spec, e := lc.Reconstruct(&element, d.cfg.SampleRate, &random)
		if e != nil {
			return 0, e
		}
		for c := 0; c < element.Count; c++ {
			ch := &element.Channels[c]
			if e = banks[first+c].Synthesize(spec[c][:], int(ch.Sequence), int(ch.Shape), planar[first+c][:]); e != nil {
				return 0, e
			}
		}
	}
	if e = ctx.Err(); e != nil {
		return 0, e
	}
	for c := range planar {
		for i, v := range planar[c] {
			v /= 32768
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0, fmt.Errorf("%w: non-finite PCM", pcm.ErrMalformed)
			}
			planar[c][i] = v
		}
	}
	if n := len(planar); d.output == n {
		for c := range planar {
			for i, v := range planar[c] {
				dst[i*n+d.slots[c]] = v
			}
		}
	} else {
		for i := 0; i < 1024; i++ {
			var left, right float64
			for c := range planar {
				v := planar[c][i]
				left += v * d.left[c]
				right += v * d.right[c]
			}
			left *= d.scale
			right *= d.scale
			if d.output == 1 {
				dst[i] = (left + right) * 0.5
			} else {
				dst[2*i], dst[2*i+1] = left, right
			}
		}
	}
	copy(d.banks, banks)
	d.random = random
	return 1024, nil
}
