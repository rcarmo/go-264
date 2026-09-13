package aac_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rcarmo/go-264/audio/aac"
	"github.com/rcarmo/go-264/audio/aac/internal/lc"
	"github.com/rcarmo/go-264/audio/mp4"
)

type encoderOracleCase struct {
	name                    string
	rate, channels          int
	bitrate                 int
	pns, ms, intensity, tns bool
	requireNoiseBands       bool
}

func TestAACEncoderConfigurationOracle(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline AAC encoder configuration oracle")
	}
	cases := []encoderOracleCase{
		{name: "mono16-low-pns", rate: 16000, channels: 1, bitrate: 24000, pns: true, tns: true, requireNoiseBands: true},
		{name: "mono24-no-tools", rate: 24000, channels: 1, bitrate: 32000},
		{name: "mono32-pns", rate: 32000, channels: 1, bitrate: 48000, pns: true, requireNoiseBands: true},
		{name: "stereo32-low-all", rate: 32000, channels: 2, bitrate: 48000, pns: true, ms: true, intensity: true, tns: true, requireNoiseBands: true},
		{name: "stereo441-ms-tns", rate: 44100, channels: 2, bitrate: 96000, ms: true, tns: true},
		{name: "stereo48-independent", rate: 48000, channels: 2, bitrate: 128000, pns: true, tns: true, requireNoiseBands: true},
		{name: "stereo48-low-all", rate: 48000, channels: 2, bitrate: 64000, pns: true, ms: true, intensity: true, tns: true, requireNoiseBands: true},
		{name: "stereo96-high", rate: 96000, channels: 2, bitrate: 192000, ms: true, intensity: true, tns: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "input.m4a")
			raw := filepath.Join(dir, "reference.f32")
			input := fmt.Sprintf("aevalsrc=0.31*sin(2*PI*503*t)+0.07*sin(2*PI*1901*t)+0.03*sin(2*PI*47*t)|0.27*sin(2*PI*719*t)+0.05*sin(2*PI*2309*t)+0.02*sin(2*PI*61*t):s=%d:d=1.25", tc.rate)
			if tc.requireNoiseBands {
				input = fmt.Sprintf("aevalsrc=0.24*sin(2*PI*503*t)+0.05*sin(2*PI*1901*t)+0.04*random(264)|0.21*sin(2*PI*719*t)+0.04*sin(2*PI*2309*t)+0.04*random(265):s=%d:d=1.25", tc.rate)
			}
			args := []string{
				"-v", "error", "-f", "lavfi", "-i", input,
				"-ac", fmt.Sprint(tc.channels), "-c:a", "aac", "-b:a", fmt.Sprint(tc.bitrate),
				"-aac_pns", boolString(tc.pns), "-aac_ms", boolString(tc.ms),
				"-aac_is", boolString(tc.intensity), "-aac_tns", boolString(tc.tns), file,
			}
			if log, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
				t.Fatal(err, string(log))
			}
			if log, err := exec.Command("ffmpeg", "-v", "error", "-ignore_editlist", "1", "-i", file, "-f", "f32le", raw).CombinedOutput(); err != nil {
				t.Fatal(err, string(log))
			}
			f, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			stat, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			reader, err := mp4.Open(context.Background(), f, stat.Size(), mp4.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			decoder, err := aac.NewDecoder(reader.Track().AudioSpecificConfig)
			if err != nil {
				t.Fatal(err)
			}
			packet := make([]byte, 1<<20)
			block := make([]float64, 1024*tc.channels)
			var got []float64
			var noiseBands, msBands, intensityBands, tnsFilters, shortFrames int
			for i := 0; i < reader.Track().SampleCount; i++ {
				n, _, err := reader.ReadPacket(context.Background(), i, packet)
				if err != nil {
					t.Fatal(err)
				}
				frame, err := lc.Parse(packet[:n], tc.rate, tc.channels)
				if err != nil {
					t.Fatalf("frame %d parse: %v", i, err)
				}
				for c := 0; c < frame.Count; c++ {
					ch := &frame.Channels[c]
					if ch.Sequence == lc.SequenceEightShort {
						shortFrames++
					}
					for _, tw := range ch.TNS {
						tnsFilters += tw.Count
					}
					for g := 0; g < ch.NumGroups; g++ {
						for b := 0; b < ch.MaxSFB; b++ {
							switch ch.Codebook[g][b] {
							case 13:
								noiseBands++
							case 14, 15:
								intensityBands++
							}
							if frame.MS[g][b] {
								msBands++
							}
						}
					}
				}
				if frames, err := decoder.Decode(context.Background(), packet[:n], block); frames != 1024 || err != nil {
					t.Fatalf("frame %d decode: frames=%d err=%v", i, frames, err)
				}
				got = append(got, block...)
			}
			ref, err := os.ReadFile(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(ref) != len(got)*4 {
				t.Fatalf("sample count: got=%d reference=%d", len(got), len(ref)/4)
			}
			var signal, errEnergy, maxErr float64
			for i, value := range got {
				want := float64(math.Float32frombits(binary.LittleEndian.Uint32(ref[4*i:])))
				delta := value - want
				signal += want * want
				errEnergy += delta * delta
				maxErr = math.Max(maxErr, math.Abs(delta))
			}
			snr := math.Inf(1)
			if errEnergy != 0 {
				snr = 10 * math.Log10(signal/errEnergy)
			}
			t.Logf("samples=%d SNR=%g maxabs=%g noise=%d ms=%d intensity=%d tns=%d short=%d", len(got), snr, maxErr, noiseBands, msBands, intensityBands, tnsFilters, shortFrames)
			if math.IsNaN(snr) || snr < 70 || maxErr > 1e-5 {
				t.Fatalf("PCM mismatch: SNR=%g maxabs=%g", snr, maxErr)
			}
			if tc.requireNoiseBands && noiseBands == 0 {
				t.Fatal("encoder did not emit a PNS band")
			}
		})
	}
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
