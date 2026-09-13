// Package ac3 decodes bounded ATSC A/52 AC-3 syncframes to deterministic PCM.
//
// The AC-3 bit-allocation, mantissa and transform implementation is adapted
// from AC3Psy commit ebdd1d3d6cf80690d1c7e648231f7d603ca33f97,
// Copyright (c) 2026 PCPX, used under the MIT licence in LICENSE.ac3psy.
// E-AC-3 and AC3Psy-specific concealment, telemetry and C ABI code are omitted.
package ac3

import (
	"context"
	"fmt"
	"math"

	"github.com/rcarmo/go-264/audio/pcm"
)

const (
	FrameSamples = 1536
	MaxChannels  = 6
)

// Channel identifies a canonical decoded source channel. Channel indices used
// by Config.SourceChannels refer to Layout order, not coded acmod order.
type Channel uint8

const (
	FrontLeft Channel = iota
	FrontRight
	FrontCenter
	LFE
	SideLeft
	SideRight
	BackCenter
)

func (c Channel) String() string {
	switch c {
	case FrontLeft:
		return "FL"
	case FrontRight:
		return "FR"
	case FrontCenter:
		return "FC"
	case LFE:
		return "LFE"
	case SideLeft:
		return "SL"
	case SideRight:
		return "SR"
	case BackCenter:
		return "BC"
	default:
		return "unknown"
	}
}

// Config selects mono/stereo downmix or one/two canonical source channels.
// A nil SourceChannels applies the stream's centre/surround mix metadata and
// excludes LFE. A non-nil slice must contain exactly OutputChannels distinct
// indices into the frame's canonical Layout.
type Config struct {
	OutputChannels int
	SourceChannels []int
}

// FrameInfo describes one successfully decoded syncframe.
type FrameInfo struct {
	FrameSize     int
	SampleRate    int
	BitRateKbps   int
	BSID          uint8
	BSMod         uint8
	ACMod         uint8
	LFE           bool
	DialNorm      uint8
	InputChannels int
	// Layout contains InputChannels canonical entries without per-frame allocation.
	Layout [MaxChannels]Channel
}

type header struct {
	frameSize, sampleRate, bitRateKbps int
	fscod, frmsizecod, bsid, bsmod     uint8
	acmod, lfeon, nfchans              uint8
	dialnorm, dialnorm2                uint8
	cmixlev, surmixlev, dsurmod        uint8
	compre, compr, compre2, compr2     uint8
}

// Decoder is stateful because AC-3 transform overlap and dithering span blocks.
// It is sequential and not safe for concurrent use.
type Decoder struct {
	config  Config
	state   blockState
	overlap [MaxChannels][256]float64
	window  [256]float64
	dither  uint32
}

func NewDecoder(config Config) (*Decoder, error) {
	if config.OutputChannels != 1 && config.OutputChannels != 2 {
		return nil, fmt.Errorf("%w: AC-3 output channel count", pcm.ErrUnsupported)
	}
	if config.SourceChannels != nil {
		if len(config.SourceChannels) != config.OutputChannels {
			return nil, fmt.Errorf("%w: AC-3 source channel selection", pcm.ErrMalformed)
		}
		for i, channel := range config.SourceChannels {
			if channel < 0 || channel >= MaxChannels {
				return nil, fmt.Errorf("%w: AC-3 source channel index", pcm.ErrMalformed)
			}
			for _, previous := range config.SourceChannels[:i] {
				if previous == channel {
					return nil, fmt.Errorf("%w: duplicate AC-3 source channel", pcm.ErrMalformed)
				}
			}
		}
		config.SourceChannels = append([]int(nil), config.SourceChannels...)
	}
	d := &Decoder{config: config, dither: 0x6d2b79f5}
	d.initWindow()
	return d, nil
}

func (d *Decoder) Reset() {
	config := d.config
	*d = Decoder{config: config, dither: 0x6d2b79f5}
	d.initWindow()
}

// Decode decodes exactly one complete syncframe. It returns 1536 frames.
// On any error, decoder state and dst are unchanged.
func (d *Decoder) Decode(ctx context.Context, frame []byte, dst []float64) (int, FrameInfo, error) {
	var info FrameInfo
	if d == nil {
		return 0, info, fmt.Errorf("%w: nil AC-3 decoder", pcm.ErrMalformed)
	}
	if ctx == nil {
		return 0, info, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return 0, info, err
	}
	if len(dst) < FrameSamples*d.config.OutputChannels {
		return 0, info, fmt.Errorf("%w: AC-3 output buffer", pcm.ErrMalformed)
	}
	h, br, err := parseHeader(frame)
	if err != nil {
		return 0, info, err
	}
	if h.frameSize != len(frame) {
		return 0, info, fmt.Errorf("%w: AC-3 sample contains trailing data", pcm.ErrMalformed)
	}
	words := h.frameSize / 2
	firstWords := (words >> 1) + (words >> 3)
	if crc16(frame[2:], (firstWords-1)*16) != 0 || crc16(frame[2:], (h.frameSize-2)*8) != 0 {
		return 0, info, fmt.Errorf("%w: AC-3 CRC", pcm.ErrMalformed)
	}
	layout, layoutChannels := canonicalLayoutFixed(h.acmod, h.lfeon != 0)
	if layoutChannels != int(h.nfchans+h.lfeon) {
		return 0, info, fmt.Errorf("%w: AC-3 channel layout", pcm.ErrMalformed)
	}
	for _, index := range d.config.SourceChannels {
		if index >= layoutChannels {
			return 0, info, fmt.Errorf("%w: AC-3 source channel index", pcm.ErrUnsupported)
		}
	}

	work := *d
	var planar [MaxChannels][FrameSamples]float64
	if err := work.decodeBlocks(ctx, br, h, &planar); err != nil {
		return 0, info, err
	}
	var output [FrameSamples * 2]float64
	work.render(h, layout, &planar, output[:FrameSamples*d.config.OutputChannels])
	copy(dst, output[:FrameSamples*d.config.OutputChannels])
	*d = work
	info = FrameInfo{
		FrameSize: h.frameSize, SampleRate: h.sampleRate, BitRateKbps: h.bitRateKbps,
		BSID: h.bsid, BSMod: h.bsmod, ACMod: h.acmod, LFE: h.lfeon != 0,
		DialNorm: h.dialnorm, InputChannels: layoutChannels, Layout: layout,
	}
	return FrameSamples, info, nil
}

// Layout returns canonical channel order for an AC-3 acmod/lfeon pair.
// It returns nil for reserved acmod values.
func Layout(acmod uint8, lfe bool) []Channel {
	if acmod > 7 {
		return nil
	}
	layout, channels := canonicalLayoutFixed(acmod, lfe)
	return append([]Channel(nil), layout[:channels]...)
}

func canonicalLayout(acmod uint8, lfe bool) []Channel { return Layout(acmod, lfe) }

func canonicalLayoutFixed(acmod uint8, lfe bool) (out [MaxChannels]Channel, channels int) {
	coded := codedLayout(acmod)
	order := [...]Channel{FrontLeft, FrontRight, FrontCenter, LFE, SideLeft, SideRight, BackCenter}
	for _, wanted := range order {
		if wanted == LFE {
			if lfe {
				out[channels] = LFE
				channels++
			}
			continue
		}
		for _, channel := range coded {
			if channel == wanted {
				out[channels] = channel
				channels++
				break
			}
		}
	}
	return out, channels
}

func codedLayout(acmod uint8) []Channel {
	layouts := [...][5]Channel{
		{FrontLeft, FrontRight},
		{FrontCenter},
		{FrontLeft, FrontRight},
		{FrontLeft, FrontCenter, FrontRight},
		{FrontLeft, FrontRight, BackCenter},
		{FrontLeft, FrontCenter, FrontRight, BackCenter},
		{FrontLeft, FrontRight, SideLeft, SideRight},
		{FrontLeft, FrontCenter, FrontRight, SideLeft, SideRight},
	}
	counts := [...]int{2, 1, 2, 3, 3, 4, 4, 5}
	return layouts[acmod][:counts[acmod]]
}

func (d *Decoder) render(h header, layout [MaxChannels]Channel, planar *[MaxChannels][FrameSamples]float64, dst []float64) {
	coded := codedLayout(h.acmod)
	codedIndex := func(role Channel) int {
		if role == LFE {
			return int(h.nfchans)
		}
		for i, value := range coded {
			if value == role {
				return i
			}
		}
		return -1
	}
	if d.config.SourceChannels != nil {
		for n := 0; n < FrameSamples; n++ {
			for out, source := range d.config.SourceChannels {
				dst[n*d.config.OutputChannels+out] = planar[codedIndex(layout[source])][n]
			}
		}
		return
	}
	cmix, smix := 1/math.Sqrt2, 1/math.Sqrt2
	if h.cmixlev == 1 {
		cmix = math.Pow(2, -0.75)
	} else if h.cmixlev == 2 {
		cmix = 0.5
	}
	if h.surmixlev == 1 {
		smix = 0.5
	} else if h.surmixlev == 2 {
		smix = 0
	}
	var leftGain, rightGain [5]float64
	var sumLeft, sumRight float64
	for i, role := range coded {
		switch role {
		case FrontLeft:
			leftGain[i] = 1
		case FrontRight:
			rightGain[i] = 1
		case FrontCenter:
			leftGain[i], rightGain[i] = cmix, cmix
		case BackCenter:
			leftGain[i], rightGain[i] = smix, smix
		case SideLeft:
			leftGain[i] = smix
		case SideRight:
			rightGain[i] = smix
		}
		sumLeft += leftGain[i]
		sumRight += rightGain[i]
	}
	scale := 1 / math.Max(1, math.Max(sumLeft, sumRight))
	for n := 0; n < FrameSamples; n++ {
		var left, right float64
		for i := range coded {
			left += planar[i][n] * leftGain[i]
			right += planar[i][n] * rightGain[i]
		}
		left *= scale
		right *= scale
		if d.config.OutputChannels == 1 {
			dst[n] = (left + right) * 0.5
		} else {
			dst[2*n], dst[2*n+1] = left, right
		}
	}
}
