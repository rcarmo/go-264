package aac_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/audio/aac"
	"github.com/rcarmo/go-264/audio/aac/internal/lc"
	"github.com/rcarmo/go-264/audio/mp4"
)

// multichannelCase is an FFmpeg layout written with an implicit AAC channel
// configuration; positions are FFmpeg's channel names in its output order.
type multichannelCase struct {
	layout    string
	config    int
	positions []string
}

var multichannelCases = []multichannelCase{
	{"3.0", 3, []string{"FL", "FR", "FC"}},
	{"4.0", 4, []string{"FL", "FR", "FC", "BC"}},
	{"5.0", 5, []string{"FL", "FR", "FC", "BL", "BR"}},
	{"5.1", 6, []string{"FL", "FR", "FC", "LFE", "BL", "BR"}},
	{"7.1(wide)", 7, []string{"FL", "FR", "FC", "LFE", "BL", "BR", "FLC", "FRC"}},
}

// multichannelTone gives every position a distinct, non-harmonic frequency.
var multichannelTone = map[string]float64{
	"FL": 440, "FR": 620, "FC": 830, "LFE": 60, "BL": 1050, "BR": 1270, "FLC": 1490, "FRC": 1710, "BC": 1930,
}

func requireFFmpegOracle(t *testing.T) {
	t.Helper()
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline synthetic FFmpeg multichannel oracle")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

// encodeMultichannel writes an AAC-LC M4A whose channel i carries source(i).
func encodeMultichannel(t *testing.T, file string, tc multichannelCase, rate, bitrate int, source func(i int, position string) string) {
	t.Helper()
	var args []string
	var inputs, mapping []string
	for i, position := range tc.positions {
		args = append(args, "-f", "lavfi", "-i", source(i, position))
		inputs = append(inputs, fmt.Sprintf("[%d:a]", i))
		mapping = append(mapping, fmt.Sprintf("%d.0-%s", i, position))
	}
	encodeLayout := tc.layout
	if tc.config == 7 {
		// FFmpeg emits implicit configuration 7 for its compatibility 7.1
		// layout. Relabel only encoder inputs; strict decoding recovers the
		// specification's front-wide ordering.
		encodeLayout = "7.1"
		compat := map[string]string{"FL": "SL", "FR": "SR", "FLC": "FL", "FRC": "FR"}
		for i, position := range tc.positions {
			if role, ok := compat[position]; ok {
				mapping[i] = fmt.Sprintf("%d.0-%s", i, role)
			}
		}
	}
	graph := fmt.Sprintf("%sjoin=inputs=%d:channel_layout=%s:map=%s[a]", strings.Join(inputs, ""), len(tc.positions), encodeLayout, strings.Join(mapping, "|"))
	args = append([]string{"-v", "error"}, args...)
	args = append(args, "-filter_complex", graph, "-map", "[a]", "-ar", fmt.Sprint(rate), "-c:a", "aac", "-b:a", fmt.Sprint(bitrate), file)
	if log, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatal(err, string(log))
	}
	requireFFmpegLayout(t, file, tc.layout)
}

// requireFFmpegLayout checks specification-defined loudspeaker positions,
// using strict decoder mode rather than FFmpeg's layout compatibility remap.
func requireFFmpegLayout(t *testing.T, file, layout string) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-strict", "strict", "-select_streams", "a:0", "-show_entries", "stream=channel_layout", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatal("ffprobe layout check failed:", err)
	}
	if got := strings.TrimSpace(string(out)); got != layout {
		t.Fatalf("strict FFmpeg layout %q, want %q", got, layout)
	}
}

func ffmpegF32(t *testing.T, file string, extra ...string) []float64 {
	t.Helper()
	raw := file + ".f32"
	args := append([]string{"-v", "error", "-ignore_editlist", "1", "-strict", "strict", "-i", file}, extra...)
	args = append(args, "-f", "f32le", raw)
	if log, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatal(err, string(log))
	}
	data, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]float64, len(data)/4)
	for i := range out {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:])))
	}
	return out
}

// noiseBands counts PNS bands per coded channel across all access units.
func noiseBands(t *testing.T, file string, channelConfig int) []int {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()
	m, err := mp4.Open(context.Background(), f, stat.Size(), mp4.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var counts []int
	packet := make([]byte, 1<<20)
	for i := 0; i < m.Track().SampleCount; i++ {
		n, _, err := m.ReadPacket(context.Background(), i, packet)
		if err != nil {
			t.Fatal(err)
		}
		block, err := lc.NewBlock(packet[:n], m.Track().SampleRate, channelConfig)
		if err != nil {
			t.Fatal(err)
		}
		var element lc.Frame
		for coded := 0; block.Remaining() > 0; coded += element.Count {
			if err := block.Next(&element); err != nil {
				t.Fatal(err)
			}
			for c := 0; c < element.Count; c++ {
				if coded+c >= len(counts) {
					counts = append(counts, 0)
				}
				ch := &element.Channels[c]
				for g := 0; g < ch.NumGroups; g++ {
					for b := 0; b < ch.MaxSFB; b++ {
						if ch.Codebook[g][b] == 13 {
							counts[coded+c]++
						}
					}
				}
			}
		}
	}
	return counts
}

// decodeM4A decodes every access unit without applying the edit list.
func decodeM4A(t *testing.T, file string, opts aac.Options) ([]float64, *aac.Decoder) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()
	m, err := mp4.Open(context.Background(), f, stat.Size(), mp4.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := aac.NewDecoderWithOptions(m.Track().AudioSpecificConfig, opts)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 1<<20)
	dst := make([]float64, 1024*d.OutputChannels())
	var got []float64
	for i := 0; i < m.Track().SampleCount; i++ {
		n, _, err := m.ReadPacket(context.Background(), i, packet)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Decode(context.Background(), packet[:n], dst); err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		got = append(got, dst...)
	}
	return got, d
}

// toneAmplitude estimates a sinusoid's amplitude with a Hann-windowed Goertzel
// filter over one interleaved channel, skipping codec start-up and tail.
func toneAmplitude(pcm []float64, channels, channel int, hz, rate float64) float64 {
	frames := len(pcm) / channels
	start, end := frames/8, frames-frames/8
	coeff := 2 * math.Cos(2*math.Pi*hz/rate)
	var s1, s2, gain float64
	for i := start; i < end; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i-start)/float64(end-start-1))
		s1, s2 = pcm[i*channels+channel]*w+coeff*s1-s2, s1
		gain += w
	}
	return math.Sqrt(math.Max(0, s1*s1+s2*s2-coeff*s1*s2)) * 2 / gain
}

// channelSNR compares one interleaved channel from frame start onwards.
func channelSNR(got, want []float64, channels, channel, start int) (snr, maxErr float64) {
	var signal, noise float64
	for i := start*channels + channel; i < len(want); i += channels {
		d := got[i] - want[i]
		signal += want[i] * want[i]
		noise += d * d
		maxErr = math.Max(maxErr, math.Abs(d))
	}
	if noise == 0 {
		return math.Inf(1), 0
	}
	return 10 * math.Log10(signal/noise), maxErr
}

func TestMultichannelPCMOracle(t *testing.T) {
	requireFFmpegOracle(t)
	type variant struct {
		kind    string
		rate    int
		perChan int
	}
	for _, tc := range multichannelCases {
		variants := []variant{{"tone", 48000, 96000}}
		if tc.config >= 6 {
			variants = append(variants, variant{"tone", 44100, 96000}, variant{"noise", 48000, 24000}, variant{"transient", 48000, 64000})
		}
		for _, v := range variants {
			t.Run(fmt.Sprintf("%s-%s-%d", tc.layout, v.kind, v.rate), func(t *testing.T) {
				file := filepath.Join(t.TempDir(), "multichannel.m4a")
				encodeMultichannel(t, file, tc, v.rate, v.perChan*len(tc.positions), func(i int, position string) string {
					hz := multichannelTone[position]
					switch v.kind {
					case "noise":
						return fmt.Sprintf("anoisesrc=color=pink:seed=%d:amplitude=0.3:sample_rate=%d:duration=0.6", 11+i, v.rate)
					case "transient":
						return fmt.Sprintf("aevalsrc=0.4*sin(2*PI*%g*t)*lt(mod(t+%g\\,0.07)\\,0.02):s=%d:d=0.6", hz, 0.009*float64(i), v.rate)
					}
					return fmt.Sprintf("aevalsrc=0.25*sin(2*PI*%g*t):s=%d:d=0.6", hz, v.rate)
				})
				got, d := decodeM4A(t, file, aac.Options{})
				if cfg := d.Config(); cfg.ChannelConfig != tc.config || cfg.SampleRate != v.rate {
					t.Fatalf("FFmpeg wrote %+v, want channel configuration %d", cfg, tc.config)
				}
				if layout := fmt.Sprint(aac.Layout(tc.config)); layout != fmt.Sprint(tc.positions) {
					t.Fatalf("Layout %s, FFmpeg order %v", layout, tc.positions)
				}
				want := ffmpegF32(t, file)
				n := len(tc.positions)
				if len(got) != len(want) {
					t.Fatalf("samples %d want %d", len(got), len(want))
				}
				for c, position := range tc.positions {
					// The first access unit is excluded: for its left window half
					// the filterbank uses the current window_shape, while FFmpeg
					// assumes a previous sine window. The difference is confined
					// to encoder priming that MP4 edit lists trim, predates
					// multichannel support and applies to mono/stereo alike.
					_, firstErr := channelSNR(got[:1024*n], want[:1024*n], n, c, 0)
					snr, maxErr := channelSNR(got, want, n, c, 1024)
					t.Logf("%s SNR %.1f dB max error %.3g (first access unit %.3g)", position, snr, maxErr, firstErr)
					if math.IsNaN(snr) || snr < 70 || maxErr > 1e-5 {
						t.Fatalf("%s SNR %.1f dB max error %.3g", position, snr, maxErr)
					}
				}
				if v.kind == "noise" {
					// PNS consumes one decoder-wide PRNG in element order, so
					// noise in later elements checks cross-element ordering.
					counts := noiseBands(t, file, tc.config)
					withNoise := 0
					for _, count := range counts {
						if count > 0 {
							withNoise++
						}
					}
					t.Logf("PNS bands per coded channel %v", counts)
					if withNoise < 2 {
						t.Fatalf("encoder produced PNS in %d channels", withNoise)
					}
				}
				if v.kind != "tone" {
					return
				}
				// Each output channel carries only its own tone.
				for c, position := range tc.positions {
					own := toneAmplitude(got, n, c, multichannelTone[position], float64(v.rate))
					if own < 0.2 || own > 0.3 {
						t.Fatalf("%s own tone amplitude %.4f", position, own)
					}
					worst := 0.0
					for _, other := range tc.positions {
						if other != position {
							worst = math.Max(worst, toneAmplitude(got, n, c, multichannelTone[other], float64(v.rate)))
						}
					}
					isolation := 20 * math.Log10(own/worst)
					t.Logf("%s tone %.4f isolation %.1f dB", position, own, isolation)
					if isolation < 30 {
						t.Fatalf("%s isolation %.1f dB", position, isolation)
					}
				}
			})
		}
	}
}

// TestMultichannelDownmixOracle applies the documented BS.775 gains to FFmpeg's
// independent native decode and compares the decoder's float downmix.
func TestMultichannelDownmixOracle(t *testing.T) {
	requireFFmpegOracle(t)
	k := 1 / math.Sqrt2
	gains := map[string][2]float64{"FL": {1, 0}, "FR": {0, 1}, "FLC": {1, 0}, "FRC": {0, 1}, "FC": {k, k}, "BC": {k, k}, "BL": {k, 0}, "SL": {k, 0}, "BR": {0, k}, "SR": {0, k}, "LFE": {0, 0}}
	for _, tc := range multichannelCases {
		t.Run(tc.layout, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "multichannel.m4a")
			encodeMultichannel(t, file, tc, 48000, 96000*len(tc.positions), func(i int, position string) string {
				return fmt.Sprintf("aevalsrc=0.25*sin(2*PI*%g*t):s=48000:d=0.6", multichannelTone[position])
			})
			native := ffmpegF32(t, file)
			n := len(tc.positions)
			sum := 0.0
			for _, position := range tc.positions {
				sum += gains[position][0]
			}
			scale := 1 / sum
			for _, output := range []int{2, 1} {
				got, _ := decodeM4A(t, file, aac.Options{OutputChannels: output})
				want := make([]float64, len(native)/n*output)
				for i := range len(native) / n {
					var l, r float64
					for c, position := range tc.positions {
						l += native[i*n+c] * gains[position][0]
						r += native[i*n+c] * gains[position][1]
					}
					l, r = l*scale, r*scale
					if output == 1 {
						want[i] = (l + r) * 0.5
					} else {
						want[2*i], want[2*i+1] = l, r
					}
				}
				for c := 0; c < output; c++ {
					snr, maxErr := channelSNR(got, want, output, c, 0)
					t.Logf("output %d channel %d SNR %.1f dB max error %.3g", output, c, snr, maxErr)
					if math.IsNaN(snr) || snr < 70 || maxErr > 1e-5 {
						t.Fatalf("output %d channel %d SNR %.1f dB", output, c, snr)
					}
				}
			}
		})
	}
}

// TestMultichannelFullScaleDownmixBound drives every channel at 0 dBFS in
// phase, measured after the abrupt start and before the abrupt end. Coding
// overshoot may exceed 1.0 natively, but the normalised downmix is a convex
// combination: its float peak cannot exceed the loudest native non-LFE
// channel, where an unnormalised 7.1 sum would reach about 3.1.
func TestMultichannelFullScaleDownmixBound(t *testing.T) {
	requireFFmpegOracle(t)
	for _, tc := range multichannelCases {
		t.Run(tc.layout, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "multichannel.m4a")
			encodeMultichannel(t, file, tc, 48000, 96000*len(tc.positions), func(i int, position string) string {
				if position == "LFE" {
					return "aevalsrc=sin(2*PI*60*t):s=48000:d=0.6"
				}
				return "aevalsrc=sin(2*PI*1000*t):s=48000:d=0.6"
			})
			native, _ := decodeM4A(t, file, aac.Options{})
			n := len(tc.positions)
			// Measure the steady state: abrupt tone starts/ends overshoot.
			frames := len(native) / n
			native = native[frames/8*n : (frames-frames/8)*n]
			nativePeak := 0.0
			for i, v := range native {
				if tc.positions[i%n] != "LFE" {
					nativePeak = math.Max(nativePeak, math.Abs(v))
				}
			}
			for _, output := range []int{2, 1} {
				mixed, _ := decodeM4A(t, file, aac.Options{OutputChannels: output})
				mixed = mixed[frames/8*output : (frames-frames/8)*output]
				peak := 0.0
				for _, v := range mixed {
					peak = math.Max(peak, math.Abs(v))
				}
				t.Logf("output %d peak %.5f native non-LFE peak %.5f", output, peak, nativePeak)
				if peak > nativePeak*(1+1e-12) || peak < 0.9*nativePeak {
					t.Fatalf("output %d peak %.5f native %.5f", output, peak, nativePeak)
				}
			}
		})
	}
}
