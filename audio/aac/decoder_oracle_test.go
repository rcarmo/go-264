package aac_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/rcarmo/go-264/audio/aac"
	"github.com/rcarmo/go-264/audio/aac/internal/lc"
	"github.com/rcarmo/go-264/audio/mp4"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPCMOracle(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline synthetic FFmpeg oracle")
	}
	for _, kind := range []string{"tone", "transient", "noise"} {
		for _, ch := range []int{1, 2} {
			for _, rate := range []int{8000, 16000, 22050, 24000, 32000, 44100, 48000, 96000} {
				t.Run(fmt.Sprintf("%s-%d-%d", kind, rate, ch), func(t *testing.T) {
					dir := t.TempDir()
					file := filepath.Join(dir, "tone.m4a")
					raw := filepath.Join(dir, "reference.f32")
					input := fmt.Sprintf("sine=frequency=997:sample_rate=%d:duration=0.3", rate)
					pns := "0"
					if kind == "transient" {
						input = fmt.Sprintf("aevalsrc=0.4*sin(2*PI*997*t)*lt(mod(t\\,0.05)\\,0.02)|0.3*sin(2*PI*1703*t)*lt(mod(t\\,0.08)\\,0.04):s=%d:d=0.5", rate)
					}
					if kind == "noise" {
						input = fmt.Sprintf("anoisesrc=color=pink:seed=42:sample_rate=%d:duration=0.5", rate)
						pns = "1"
					}
					args := []string{"-v", "error", "-f", "lavfi", "-i", input, "-ac", fmt.Sprint(ch), "-c:a", "aac", "-aac_pns", pns, "-b:a", fmt.Sprint(min(rate*2*ch, 128000)), file}
					if log, e := exec.Command("ffmpeg", args...).CombinedOutput(); e != nil {
						t.Fatal(e, string(log))
					}
					if log, e := exec.Command("ffmpeg", "-v", "error", "-ignore_editlist", "1", "-i", file, "-f", "f32le", raw).CombinedOutput(); e != nil {
						t.Fatal(e, string(log))
					}
					f, e := os.Open(file)
					if e != nil {
						t.Fatal(e)
					}
					defer f.Close()
					stat, _ := f.Stat()
					m, e := mp4.Open(context.Background(), f, stat.Size(), mp4.Limits{})
					if e != nil {
						t.Fatal(e)
					}
					d, e := aac.NewDecoder(m.Track().AudioSpecificConfig)
					if e != nil {
						t.Fatal(e)
					}
					var got []float64
					packet := make([]byte, 1<<20)
					dst := make([]float64, 1024*ch)
					var noiseBands, shorts, tnsFilters int
					for i := 0; i < m.Track().SampleCount; i++ {
						n, _, e := m.ReadPacket(context.Background(), i, packet)
						if e != nil {
							t.Fatal(e)
						}
						frame, pe := lc.Parse(packet[:n], rate, ch)
						if pe != nil {
							t.Fatal(i, pe)
						}
						for c := 0; c < frame.Count; c++ {
							cc := frame.Channels[c]
							if cc.Sequence == lc.SequenceEightShort {
								shorts++
							}
							for _, tw := range cc.TNS {
								tnsFilters += tw.Count
							}
							for g := 0; g < cc.NumGroups; g++ {
								for b := 0; b < cc.MaxSFB; b++ {
									if cc.Codebook[g][b] == 13 {
										noiseBands++
									}
								}
							}
						}
						_, e = d.Decode(context.Background(), packet[:n], dst)
						if e != nil {
							t.Fatalf("frame%d: %v", i, e)
						}
						got = append(got, dst...)
					}
					ref, e := os.ReadFile(raw)
					if e != nil {
						t.Fatal(e)
					}
					if len(got)*4 != len(ref) {
						t.Fatal("counts", len(got), len(ref)/4)
					}
					var signal, errEnergy, cross, gotEnergy, maxErr float64
					for i, v := range got {
						want := float64(math.Float32frombits(binary.LittleEndian.Uint32(ref[4*i:])))
						diff := v - want
						signal += want * want
						errEnergy += diff * diff
						cross += v * want
						gotEnergy += v * v
						maxErr = math.Max(maxErr, math.Abs(diff))
					}
					snr := 10 * math.Log10(signal/errEnergy)
					t.Logf("samples%d SNR%g maxerr%g projection gain%g", len(got), snr, maxErr, cross/signal)
					t.Logf("noiseBands%d shortFrames%d TNSfilters%d energyRatio%g", noiseBands, shorts, tnsFilters, gotEnergy/signal)
					if math.IsNaN(snr) || snr < 70 || maxErr > 1e-5 {
						t.Fatalf("PCM SNR%g <70dB", snr)
					}
				})
			}
		}
	}
}
