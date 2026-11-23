package audio_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/audio"
)

// aacSurroundCase lists FFmpeg channel names in its output order, which is
// also the native aac.Layout order for the same channel configuration.
type aacSurroundCase struct {
	layout    string
	positions []string
}

var aacSurroundCases = []aacSurroundCase{
	{"3.0", []string{"FL", "FR", "FC"}},
	{"4.0", []string{"FL", "FR", "FC", "BC"}},
	{"5.0", []string{"FL", "FR", "FC", "BL", "BR"}},
	{"5.1", []string{"FL", "FR", "FC", "LFE", "BL", "BR"}},
	{"7.1(wide)", []string{"FL", "FR", "FC", "LFE", "BL", "BR", "FLC", "FRC"}},
}

var aacSurroundTone = map[string]float64{
	"FL": 440, "FR": 620, "FC": 830, "LFE": 60, "BL": 1050, "BR": 1270, "FLC": 1490, "FRC": 1710, "BC": 1930,
}

// makeAACSurroundMP4 encodes expr(position) into each channel as AAC-LC.
func makeAACSurroundMP4(t *testing.T, tc aacSurroundCase, rate int, expr func(position string) string) string {
	t.Helper()
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline synthetic FFmpeg multichannel AAC oracle")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	exprs := make([]string, len(tc.positions))
	for i, position := range tc.positions {
		exprs[i] = expr(position)
	}
	path := filepath.Join(t.TempDir(), "surround.m4a")
	encodeLayout := tc.layout
	if encodeLayout == "7.1(wide)" {
		// FFmpeg's encoder writes implicit config 7 from compatibility 7.1;
		// strict decode labels this coded pair FLC/FRC rather than SL/SR.
		encodeLayout = "7.1"
		// Compatibility FL/FR are strict FLC/FRC, compatibility SL/SR
		// are strict FL/FR. Feed the encoder in that native order.
		exprs[0], exprs[1], exprs[6], exprs[7] = exprs[6], exprs[7], exprs[0], exprs[1]
	}
	filter := fmt.Sprintf("aevalsrc=%s:s=%d:d=1:c=%s", strings.Join(exprs, "|"), rate, encodeLayout)
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", filter, "-c:a", "aac", "-b:a", fmt.Sprint(96000*len(tc.positions)), "-y", path).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	// Strict mode keeps the specification-defined outside-front pair.
	out, err := exec.Command("ffprobe", "-v", "error", "-strict", "strict", "-select_streams", "a:0", "-show_entries", "stream=channel_layout", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal("ffprobe layout check failed:", err)
	}
	if got := strings.TrimSpace(string(out)); got != tc.layout {
		t.Fatalf("strict FFmpeg layout %q, want %q", got, tc.layout)
	}
	return path
}

func openSurround(t *testing.T, path string, rate, channels int) []float64 {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stat, _ := file.Stat()
	decoder, err := audio.Open(context.Background(), file, stat.Size(), audio.Options{TargetRate: rate, TargetChannels: channels})
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	if got := decoder.Metadata().Output; got.Channels != channels || got.SampleRate != rate {
		t.Fatalf("output %+v", got)
	}
	pcm := readAllPCM(t, decoder, 840*channels)
	out := make([]float64, len(pcm))
	for i, v := range pcm {
		out[i] = float64(v) / 32768
	}
	return out
}

// hannTone estimates a sinusoid's amplitude on one interleaved channel.
func hannTone(pcm []float64, channels, channel int, hz float64, rate int) float64 {
	frames := len(pcm) / channels
	start, end := frames/8, frames-frames/8
	coeff := 2 * math.Cos(2*math.Pi*hz/float64(rate))
	var s1, s2, gain float64
	for i := start; i < end; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i-start)/float64(end-start-1))
		s1, s2 = pcm[i*channels+channel]*w+coeff*s1-s2, s1
		gain += w
	}
	return math.Sqrt(math.Max(0, s1*s1+s2*s2-coeff*s1*s2)) * 2 / gain
}

// TestAACSurroundStereoDownmixOracle measures every tone's left/right gain in
// public S16 stereo output relative to the native channel carrying it.
func TestAACSurroundStereoDownmixOracle(t *testing.T) {
	k := 1 / math.Sqrt2
	gains := map[string][2]float64{"FL": {1, 0}, "FR": {0, 1}, "FLC": {1, 0}, "FRC": {0, 1}, "FC": {k, k}, "BC": {k, k}, "BL": {k, 0}, "SL": {k, 0}, "BR": {0, k}, "SR": {0, k}, "LFE": {0, 0}}
	for _, tc := range aacSurroundCases {
		t.Run(tc.layout, func(t *testing.T) {
			path := makeAACSurroundMP4(t, tc, 48000, func(position string) string {
				return fmt.Sprintf("0.25*sin(2*PI*%g*t)", aacSurroundTone[position])
			})
			n := len(tc.positions)
			native := openSurround(t, path, 48000, n)
			stereo := openSurround(t, path, 48000, 2)
			if len(native)/n != len(stereo)/2 {
				t.Fatalf("frames native %d stereo %d", len(native)/n, len(stereo)/2)
			}
			sum := 0.0
			for _, position := range tc.positions {
				sum += gains[position][0]
			}
			scale := 1 / sum
			for c, position := range tc.positions {
				hz := aacSurroundTone[position]
				source := hannTone(native, n, c, hz, 48000)
				for side, name := range []string{"L", "R"} {
					got := hannTone(stereo, 2, side, hz, 48000) / source
					want := gains[position][side] * scale
					t.Logf("%s -> %s gain %.5f want %.5f", position, name, got, want)
					if math.Abs(got-want) > 0.002 {
						t.Fatalf("%s -> %s gain %.5f want %.5f", position, name, got, want)
					}
				}
			}
		})
	}
}

// TestAACSurroundFullScaleDownmixDoesNotClip drives every channel near full
// scale in phase (0.9 leaves headroom for AAC coding overshoot, which would
// clip native S16 too; the abrupt start and end are excluded). An
// unnormalised 5.1 sum would reach about 2.2; the downmix must stay within the
// loudest native channel and never hit the rails. audio/aac's float oracle
// checks the same bound at 0 dBFS.
func TestAACSurroundFullScaleDownmixDoesNotClip(t *testing.T) {
	for _, tc := range aacSurroundCases {
		t.Run(tc.layout, func(t *testing.T) {
			path := makeAACSurroundMP4(t, tc, 48000, func(position string) string {
				if position == "LFE" {
					return "0.9*sin(2*PI*60*t)"
				}
				return "0.9*sin(2*PI*1000*t)"
			})
			n := len(tc.positions)
			native := openSurround(t, path, 48000, n)
			// Measure the steady state: abrupt tone starts/ends overshoot.
			frames := len(native) / n
			native = native[frames/8*n : (frames-frames/8)*n]
			nativePeak, nativeRails := 0.0, 0
			for i, v := range native {
				if tc.positions[i%n] != "LFE" {
					nativePeak = math.Max(nativePeak, math.Abs(v))
				}
				if v >= 32767.0/32768 || v <= -1 {
					nativeRails++
				}
			}
			if nativeRails != 0 {
				t.Fatalf("native output already clips %d samples", nativeRails)
			}
			for _, channels := range []int{2, 1} {
				mixed := openSurround(t, path, 48000, channels)
				mixed = mixed[frames/8*channels : (frames-frames/8)*channels]
				peak, rails := 0.0, 0
				for _, v := range mixed {
					peak = math.Max(peak, math.Abs(v))
					if v >= 32767.0/32768 || v <= -1 {
						rails++
					}
				}
				t.Logf("channels %d peak %.5f native non-LFE peak %.5f rails %d", channels, peak, nativePeak, rails)
				// One S16 rounding step of slack per output sample.
				if rails != 0 || peak > nativePeak+1.0/32768 || peak < 0.9*nativePeak {
					t.Fatalf("channels %d peak %.5f native %.5f rails %d", channels, peak, nativePeak, rails)
				}
			}
		})
	}
}

// TestAACSurroundNativeResampledOracle resamples native 5.1/7.1 from 44.1 kHz.
func TestAACSurroundNativeResampledOracle(t *testing.T) {
	for _, tc := range aacSurroundCases[3:] {
		t.Run(tc.layout, func(t *testing.T) {
			path := makeAACSurroundMP4(t, tc, 44100, func(position string) string {
				return fmt.Sprintf("0.25*sin(2*PI*%g*t)", aacSurroundTone[position])
			})
			n := len(tc.positions)
			native := openSurround(t, path, 48000, n)
			if frames := len(native) / n; frames != 48000 {
				t.Fatalf("frames %d want 48000", frames)
			}
			for c, position := range tc.positions {
				own := hannTone(native, n, c, aacSurroundTone[position], 48000)
				worst := 0.0
				for _, other := range tc.positions {
					if other != position {
						worst = math.Max(worst, hannTone(native, n, c, aacSurroundTone[other], 48000))
					}
				}
				t.Logf("%s amplitude %.4f isolation %.1f dB", position, own, 20*math.Log10(own/worst))
				if math.Abs(own-0.25) > 0.005 || own/worst < 1000 {
					t.Fatalf("%s amplitude %.4f worst leak %.3g", position, own, worst)
				}
			}
		})
	}
}

// TestAACSurroundWithAC3DefaultTrack keeps the AC-3 default for files that
// pair a multichannel AAC-LC trak with AC-3, as before multichannel support.
func TestAACSurroundWithAC3DefaultTrack(t *testing.T) {
	path := makeAACSurroundMP4(t, aacSurroundCases[3], 48000, func(position string) string {
		return fmt.Sprintf("0.25*sin(2*PI*%g*t)", aacSurroundTone[position])
	})
	both := filepath.Join(t.TempDir(), "aac-ac3.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:a", "-map", "0:a", "-c:a:0", "copy", "-c:a:1", "ac3", "-b:a:1", "448k", "-y", both).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	file, err := os.Open(both)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stat, _ := file.Stat()
	for _, opts := range []audio.Options{{TargetRate: 48000, TargetChannels: 2}, {TargetRate: 48000, TargetChannels: 1, SourceChannels: []int{2}}} {
		decoder, err := audio.Open(context.Background(), file, stat.Size(), opts)
		if err != nil {
			t.Fatal(opts, err)
		}
		if decoder.TrackIndex() != 1 {
			t.Fatalf("%+v selected track %d, want AC-3 track 1", opts, decoder.TrackIndex())
		}
		decoder.Close()
	}
	track := 0
	decoder, err := audio.Open(context.Background(), file, stat.Size(), audio.Options{TargetRate: 48000, TargetChannels: 6, TrackIndex: &track})
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	if decoder.TrackIndex() != 0 || decoder.Metadata().Output.Channels != 6 {
		t.Fatalf("track %d output %+v", decoder.TrackIndex(), decoder.Metadata().Output)
	}
}
