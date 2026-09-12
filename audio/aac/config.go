// Package aac parses configuration and decodes a narrow AAC-LC raw-access-unit
// subset. Accepting configuration alone does not qualify frame payloads or
// container gapless/timestamp handling. See audio/README.md for tested scope.
package aac

import (
	"fmt"
	"github.com/rcarmo/go-264/audio/pcm"
)

// Config is the intentionally narrow first-delivery AAC-LC subset.
type Config struct {
	SampleRate   int
	Channels     int
	FrameSamples int
}

var rates = [...]int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

type bits struct {
	b   []byte
	pos int
}

func (r *bits) read(n int) (uint32, error) {
	if n < 0 || n > 32 || len(r.b)*8-r.pos < n {
		return 0, fmt.Errorf("%w: truncated ASC", pcm.ErrMalformed)
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<1 | uint32((r.b[r.pos/8]>>uint(7-r.pos%8))&1)
		r.pos++
	}
	return v, nil
}

// ParseConfig accepts AAC-LC object type 2, indexed 8–96 kHz sample rates,
// mono/stereo and 1024-frame GA configuration. An explicit backwards-compatible
// sync extension signalling SBR absent is accepted (common in FFmpeg LC ASC).
// Enabled SBR/PS, PCE, 960-frame mode and unknown trailing bits fail closed.
func ParseConfig(data []byte) (Config, error) {
	var c Config
	if len(data) > 64 {
		return c, fmt.Errorf("%w: ASC size", pcm.ErrLimit)
	}
	r := bits{b: data}
	obj, e := r.read(5)
	if e != nil {
		return c, e
	}
	if obj != 2 {
		return c, fmt.Errorf("%w: AAC object type %d", pcm.ErrUnsupported, obj)
	}
	idx, e := r.read(4)
	if e != nil {
		return c, e
	}
	if idx >= 12 {
		return c, fmt.Errorf("%w: AAC sample-rate index %d", pcm.ErrUnsupported, idx)
	}
	ch, e := r.read(4)
	if e != nil {
		return c, e
	}
	if ch != 1 && ch != 2 {
		return c, fmt.Errorf("%w: AAC channel config %d", pcm.ErrUnsupported, ch)
	}
	flags, e := r.read(3)
	if e != nil {
		return c, e
	}
	if flags != 0 {
		return c, fmt.Errorf("%w: AAC GA flags (960/dependency/extension)", pcm.ErrUnsupported)
	}
	if len(data)*8-r.pos >= 11 {
		probe := r
		sync, _ := probe.read(11)
		if sync == 0x2b7 {
			r = probe
			ext, e := r.read(5)
			if e != nil {
				return c, e
			}
			if ext != 5 {
				return c, fmt.Errorf("%w: AAC sync extension type %d", pcm.ErrUnsupported, ext)
			}
			enabled, e := r.read(1)
			if e != nil {
				return c, e
			}
			if enabled != 0 {
				return c, fmt.Errorf("%w: AAC SBR enabled", pcm.ErrUnsupported)
			}
		}
	}
	for r.pos < len(data)*8 {
		v, _ := r.read(1)
		if v != 0 {
			return c, fmt.Errorf("%w: AAC trailing extension (PS/unknown not accepted)", pcm.ErrUnsupported)
		}
	}
	return Config{SampleRate: rates[idx], Channels: int(ch), FrameSamples: 1024}, nil
}
