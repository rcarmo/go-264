package aac

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/audio/aac/internal/filterbank"
	"github.com/rcarmo/go-264/audio/aac/internal/huffman"
	"github.com/rcarmo/go-264/audio/aac/internal/lc"
	"github.com/rcarmo/go-264/audio/pcm"
)

// ascFor builds a 2-byte AAC-LC AudioSpecificConfig.
func ascFor(rateIndex, channelConfig int) []byte {
	var w testBits
	w.put(2, 5)
	w.put(uint32(rateIndex), 4)
	w.put(uint32(channelConfig), 4)
	w.put(0, 3)
	return w.b
}

// putICS writes one long-window individual_channel_stream. A non-zero ICS has
// a single unit coefficient at bin 0 with unit gain.
func putICS(w *testBits, impulse bool) {
	maxSFB := uint32(0)
	if impulse {
		maxSFB = 1
	}
	w.put(100, 8)
	w.put(0, 1)
	w.put(0, 2)
	w.put(0, 1)
	w.put(maxSFB, 6)
	w.put(0, 1)
	if impulse {
		w.put(1, 4)
		w.put(1, 5)
		sf, _ := huffman.ScalefactorCodeword(0)
		w.put(sf.Code, int(sf.Bits))
	}
	w.put(0, 3)
	if impulse {
		idx, _ := huffman.EncodeSpectralIndex(1, []int16{1, 0, 0, 0})
		cw, _ := huffman.SpectralCodeword(1, idx)
		w.put(cw.Code, int(cw.Bits))
	}
}

// impulsePacket writes the configured elements with independent windows and an
// impulse in coded channel impulse (-1 for silence).
func impulsePacket(channelConfig, impulse int) []byte {
	return elementsPacket(lc.Elements(channelConfig), impulse)
}

func elementsPacket(elements []lc.ElementType, impulse int) []byte {
	var w testBits
	coded := 0
	tags := map[lc.ElementType]int{}
	for _, e := range elements {
		w.put(uint32(e), 3)
		w.put(uint32(tags[e]), 4)
		tags[e]++
		if e == lc.ElementCPE {
			w.put(0, 1)
			putICS(&w, coded == impulse)
			putICS(&w, coded+1 == impulse)
			coded += 2
			continue
		}
		putICS(&w, coded == impulse)
		coded++
	}
	w.put(7, 3)
	return w.b
}

func TestConfigChannelConfigurations(t *testing.T) {
	want := [...]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, 7: 8}
	for config := 1; config <= 7; config++ {
		c, err := ParseConfig(ascFor(3, config))
		if err != nil || c.Channels != want[config] || c.ChannelConfig != config || c.SampleRate != 48000 || c.FrameSamples != 1024 {
			t.Fatalf("config %d: %+v %v", config, c, err)
		}
	}
	for _, config := range []int{0, 8, 11, 12, 15} {
		if _, err := ParseConfig(ascFor(3, config)); !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatalf("config %d err=%v", config, err)
		}
	}
}

func TestLayoutOrder(t *testing.T) {
	want := map[int]string{
		1: "[FC]",
		2: "[FL FR]",
		3: "[FL FR FC]",
		4: "[FL FR FC BC]",
		5: "[FL FR FC BL BR]",
		6: "[FL FR FC LFE BL BR]",
		7: "[FL FR FC LFE BL BR FLC FRC]",
	}
	for config := 1; config <= 7; config++ {
		layout := Layout(config)
		if got := fmt.Sprint(layout); got != want[config] {
			t.Fatalf("Layout(%d)=%s want %s", config, got, want[config])
		}
		layout[0] = LFE // returned slices are caller-owned copies
		elementChannels := 0
		for _, e := range lc.Elements(config) {
			elementChannels++
			if e == lc.ElementCPE {
				elementChannels++
			}
		}
		if elementChannels != len(codedLayouts[config]) || len(Layout(config)) != len(codedLayouts[config]) {
			t.Fatalf("config %d: %d element channels, %d positions", config, elementChannels, len(codedLayouts[config]))
		}
		slots := outputSlots(codedLayouts[config])
		seen := map[int]bool{}
		for c, role := range codedLayouts[config] {
			if Layout(config)[slots[c]] != role || seen[slots[c]] {
				t.Fatalf("config %d coded channel %d slot %d", config, c, slots[c])
			}
			seen[slots[c]] = true
		}
	}
	for _, config := range []int{-1, 0, 8} {
		if Layout(config) != nil {
			t.Fatalf("Layout(%d) is not nil", config)
		}
	}
	if Channel(200).String() != "unknown" {
		t.Fatal(Channel(200))
	}
}

func TestDownmixGainsBS775(t *testing.T) {
	k := 1 / math.Sqrt2
	for _, tc := range []struct {
		config      int
		left, right []float64 // coded order
	}{
		{3, []float64{k, 1, 0}, []float64{k, 0, 1}},
		{4, []float64{k, 1, 0, k}, []float64{k, 0, 1, k}},
		{5, []float64{k, 1, 0, k, 0}, []float64{k, 0, 1, 0, k}},
		{6, []float64{k, 1, 0, k, 0, 0}, []float64{k, 0, 1, 0, k, 0}},
		{7, []float64{k, 1, 0, 1, 0, k, 0, 0}, []float64{k, 0, 1, 0, 1, 0, k, 0}},
	} {
		left, right, scale := downmixGains(codedLayouts[tc.config])
		sum := 0.0
		for c := range tc.left {
			if left[c] != tc.left[c] || right[c] != tc.right[c] {
				t.Fatalf("config %d channel %d gains %g/%g", tc.config, c, left[c], right[c])
			}
			sum += tc.left[c]
		}
		if scale != 1/sum {
			t.Fatalf("config %d scale %g want 1/%g", tc.config, scale, sum)
		}
	}
	if _, _, scale := downmixGains(codedLayouts[6]); math.Abs(scale-1/(1+2*0.7071)) > 1e-4 {
		t.Fatalf("5.1 normalisation %g", scale)
	}
}

func TestNewDecoderOutputChannels(t *testing.T) {
	for _, tc := range []struct {
		config, output, want int
	}{
		{1, 0, 1}, {1, 1, 1}, {2, 0, 2}, {2, 2, 2},
		{3, 0, 3}, {3, 2, 2}, {3, 1, 1}, {6, 6, 6}, {6, 2, 2}, {7, 0, 8}, {7, 8, 8}, {7, 1, 1},
	} {
		d, err := NewDecoderWithOptions(ascFor(3, tc.config), Options{OutputChannels: tc.output})
		if err != nil || d.OutputChannels() != tc.want {
			t.Fatalf("config %d output %d: %v %v", tc.config, tc.output, d, err)
		}
	}
	for _, tc := range []struct{ config, output int }{
		{1, 2}, {2, 1}, {2, 3}, {3, 4}, {6, 5}, {6, 3}, {7, 6}, {6, -1}, {6, 9},
	} {
		if _, err := NewDecoderWithOptions(ascFor(3, tc.config), Options{OutputChannels: tc.output}); !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatalf("config %d output %d err=%v", tc.config, tc.output, err)
		}
	}
}

func decodeFrames(t *testing.T, d *Decoder, packet []byte, frames int) []float64 {
	t.Helper()
	var out []float64
	dst := make([]float64, 1024*d.OutputChannels())
	for i := 0; i < frames; i++ {
		if _, err := d.Decode(context.Background(), packet, dst); err != nil {
			t.Fatal(err)
		}
		out = append(out, dst...)
	}
	return out
}

// TestMultichannelPlacementAndDownmix decodes one impulse per coded channel.
// Native output must reproduce a mono decode of the same ICS exactly in that
// channel's Layout slot only; downmixes must apply the BS.775 gains exactly.
func TestMultichannelPlacementAndDownmix(t *testing.T) {
	mono, err := NewDecoder(ascFor(3, 1))
	if err != nil {
		t.Fatal(err)
	}
	reference := decodeFrames(t, mono, impulsePacket(1, 0), 2)
	energy := 0.0
	for _, v := range reference {
		energy += v * v
	}
	if energy == 0 {
		t.Fatal("silent reference")
	}
	for config := 3; config <= 7; config++ {
		coded := codedLayouts[config]
		left, right, scale := downmixGains(coded)
		for c, role := range coded {
			t.Run(fmt.Sprintf("config%d-%v", config, role), func(t *testing.T) {
				packet := impulsePacket(config, c)
				native, err := NewDecoder(ascFor(3, config))
				if err != nil {
					t.Fatal(err)
				}
				n := len(coded)
				got := decodeFrames(t, native, packet, 2)
				slot := -1
				for i, position := range Layout(config) {
					if position == role {
						slot = i
					}
				}
				for i := range reference {
					for ch := 0; ch < n; ch++ {
						want := 0.0
						if ch == slot {
							want = reference[i]
						}
						if got[i*n+ch] != want {
							t.Fatalf("frame %d channel %d = %g want %g", i, ch, got[i*n+ch], want)
						}
					}
				}
				for _, output := range []int{2, 1} {
					d, err := NewDecoderWithOptions(ascFor(3, config), Options{OutputChannels: output})
					if err != nil {
						t.Fatal(err)
					}
					mixed := decodeFrames(t, d, packet, 2)
					for i, v := range reference {
						l, r := (0+v*left[c])*scale, (0+v*right[c])*scale
						if output == 2 && (mixed[2*i] != l || mixed[2*i+1] != r) {
							t.Fatalf("stereo frame %d = %g,%g want %g,%g", i, mixed[2*i], mixed[2*i+1], l, r)
						}
						if output == 1 && mixed[i] != (l+r)*0.5 {
							t.Fatalf("mono frame %d = %g want %g", i, mixed[i], (l+r)*0.5)
						}
					}
				}
			})
		}
	}
}

func TestMultichannelDecodeTransaction(t *testing.T) {
	ctx := context.Background()
	for _, output := range []int{0, 2} {
		d, err := NewDecoderWithOptions(ascFor(3, 6), Options{OutputChannels: output})
		if err != nil {
			t.Fatal(err)
		}
		n := d.OutputChannels()
		dst := make([]float64, 1024*n+1)
		dst[1024*n] = 7
		if got, err := d.Decode(ctx, impulsePacket(6, 2), dst); got != 1024 || err != nil || dst[1024*n] != 7 {
			t.Fatal(got, err)
		}
		before := *d
		before.banks = append([]filterbank.Bank(nil), d.banks...)
		for i := range dst {
			dst[i] = 9
		}
		good := impulsePacket(6, 3)
		sce, cpe, lfe := lc.ElementSCE, lc.ElementCPE, lc.ElementLFE
		for name, packet := range map[string][]byte{
			"truncated":   good[:len(good)-2],
			"trailing":    append(append([]byte(nil), good...), 0),
			"reordered":   elementsPacket([]lc.ElementType{sce, cpe, lfe, cpe}, 1),
			"stereo-only": impulsePacket(2, 0),
			"extra-lfe":   elementsPacket([]lc.ElementType{sce, cpe, cpe, lfe, lfe}, 1),
			"missing-lfe": elementsPacket([]lc.ElementType{sce, cpe, cpe}, 1),
			"empty":       nil,
		} {
			if got, err := d.Decode(ctx, packet, dst); got != 0 || err == nil {
				t.Fatalf("%s: accepted %d", name, got)
			}
			if !reflect.DeepEqual(d.banks, before.banks) || d.random != before.random {
				t.Fatalf("%s: state mutated", name)
			}
			for _, v := range dst {
				if v != 9 {
					t.Fatalf("%s: output mutated", name)
				}
			}
		}
		c, cancel := context.WithCancel(ctx)
		cancel()
		if got, err := d.Decode(c, good, dst); got != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal(got, err)
		}
		if got, err := d.Decode(ctx, good, dst[:1024*n-1]); got != 0 || !errors.Is(err, pcm.ErrMalformed) {
			t.Fatal(got, err)
		}
		d.Reset()
		fresh, _ := NewDecoderWithOptions(ascFor(3, 6), Options{OutputChannels: output})
		if !reflect.DeepEqual(*d, *fresh) {
			t.Fatal("reset")
		}
	}
}

func TestDecodeZeroAllocAllLayouts(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ config, output int }{{1, 0}, {2, 0}, {3, 2}, {6, 0}, {6, 2}, {6, 1}, {7, 0}, {7, 2}} {
		d, err := NewDecoderWithOptions(ascFor(3, tc.config), Options{OutputChannels: tc.output})
		if err != nil {
			t.Fatal(err)
		}
		packet := impulsePacket(tc.config, 0)
		dst := make([]float64, 1024*d.OutputChannels())
		var decodeErr error
		allocs := testing.AllocsPerRun(50, func() {
			_, decodeErr = d.Decode(ctx, packet, dst)
		})
		if decodeErr != nil || allocs != 0 {
			t.Fatalf("config %d output %d allocated %g, err=%v", tc.config, tc.output, allocs, decodeErr)
		}
	}
}

func FuzzDecodeMultichannel(f *testing.F) {
	for config := 3; config <= 7; config++ {
		f.Add(impulsePacket(config, config-1), config)
	}
	f.Fuzz(func(t *testing.T, b []byte, config int) {
		if len(b) > 4096 || config < 3 || config > 7 {
			return
		}
		for _, output := range []int{0, 2} {
			d, _ := NewDecoderWithOptions(ascFor(3, config), Options{OutputChannels: output})
			dst := make([]float64, 1024*d.OutputChannels())
			if n, err := d.Decode(context.Background(), b, dst); err == nil {
				for _, v := range dst[:n*d.OutputChannels()] {
					if math.IsNaN(v) || math.IsInf(v, 0) {
						t.Fatal("non-finite output")
					}
				}
			}
		}
	})
}
