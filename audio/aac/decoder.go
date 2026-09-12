package aac

import (
	"context"
	"fmt"
	"math"

	"github.com/rcarmo/go-264/audio/aac/internal/filterbank"
	"github.com/rcarmo/go-264/audio/aac/internal/lc"
	"github.com/rcarmo/go-264/audio/pcm"
)

// Decoder reconstructs a narrow AAC-LC raw access-unit subset. It is sequential;
// one successful Decode produces 1024 frames. Source/container priming, padding
// and edit lists are caller responsibilities. This API is pre-release.
type Decoder struct {
	cfg    Config
	banks  [2]filterbank.Bank
	random uint32
}

func NewDecoder(config []byte) (*Decoder, error) {
	c, e := ParseConfig(config)
	if e != nil {
		return nil, e
	}
	d := &Decoder{cfg: c}
	d.Reset()
	return d, nil
}
func (d *Decoder) Config() Config { return d.cfg }
func (d *Decoder) Reset() {
	for i := range d.banks {
		d.banks[i].Reset()
	}
	d.random = 0x1f2e3d4c
}

// Decode writes normalised interleaved float64 PCM to dst and returns FRAMES.
// dst must hold 1024*Channels scalars. Errors leave dst and decoder state intact;
// all parsed/synthesised work commits only after validation and cancellation.
// No implicit final overlap frame is emitted: a container's sample count/trim
// plan determines which coded frames and padding are required.
func (d *Decoder) Decode(ctx context.Context, packet []byte, dst []float64) (int, error) {
	if d == nil || ctx == nil || d.cfg.FrameSamples != 1024 || (d.cfg.Channels != 1 && d.cfg.Channels != 2) {
		return 0, fmt.Errorf("%w: decoder/context", pcm.ErrMalformed)
	}
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if len(dst) < 1024*d.cfg.Channels {
		return 0, fmt.Errorf("%w: AAC output buffer", pcm.ErrMalformed)
	}
	frame, e := lc.Parse(packet, d.cfg.SampleRate, d.cfg.Channels)
	if e != nil {
		return 0, e
	}
	random := d.random
	spec, e := lc.Reconstruct(&frame, d.cfg.SampleRate, &random)
	if e != nil {
		return 0, e
	}
	banks := d.banks
	var planar [2][1024]float64
	for c := 0; c < frame.Count; c++ {
		ch := frame.Channels[c]
		if e = banks[c].Synthesize(spec[c][:], int(ch.Sequence), int(ch.Shape), planar[c][:]); e != nil {
			return 0, e
		}
	}
	if e = ctx.Err(); e != nil {
		return 0, e
	}
	var out [2048]float64
	for i := 0; i < 1024; i++ {
		for c := 0; c < frame.Count; c++ {
			v := planar[c][i] / 32768
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0, fmt.Errorf("%w: non-finite PCM", pcm.ErrMalformed)
			}
			out[i*frame.Count+c] = v
		}
	}
	copy(dst, out[:1024*frame.Count])
	d.banks = banks
	d.random = random
	return 1024, nil
}
