package audio_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/rcarmo/go-264/audio"
	"github.com/rcarmo/go-264/audio/pcm"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMP4TrimAndSeekOracle(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline MP4 PCM comparison")
	}
	for _, rate := range []int{44100, 48000} {
		for _, channels := range []int{1, 2} {
			for _, fast := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-%d-fast%v", rate, channels, fast), func(t *testing.T) {
					ctx := context.Background()
					dir := t.TempDir()
					p := filepath.Join(dir, "tone.m4a")
					refPath := filepath.Join(dir, "ref.f32")
					args := []string{"-v", "error", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=997:sample_rate=%d:duration=0.2", rate), "-ac", fmt.Sprint(channels), "-c:a", "aac"}
					if fast {
						args = append(args, "-movflags", "+faststart")
					}
					args = append(args, p)
					if b, e := exec.Command("ffmpeg", args...).CombinedOutput(); e != nil {
						t.Fatal(e, string(b))
					}
					if b, e := exec.Command("ffmpeg", "-v", "error", "-i", p, "-f", "f32le", refPath).CombinedOutput(); e != nil {
						t.Fatal(e, string(b))
					}
					f, e := os.Open(p)
					if e != nil {
						t.Fatal(e)
					}
					defer f.Close()
					stat, _ := f.Stat()
					probeMeta, e := audio.ProbeMetadata(ctx, f, stat.Size(), pcm.Limits{})
					if e != nil {
						t.Fatal(e)
					}
					d, e := audio.Open(ctx, f, stat.Size(), audio.Options{TargetRate: rate, TargetChannels: channels})
					if e != nil {
						t.Fatal(e)
					}
					defer d.Close()
					meta := d.Metadata()
					expected := int64(rate / 5)
					if probeMeta.Source != meta.Source || probeMeta.Output.Frames != meta.Output.Frames || probeMeta.Output.SampleRate != meta.Output.SampleRate || probeMeta.Output.Channels != meta.Output.Channels ||
						probeMeta.PrimingFrames != meta.PrimingFrames || probeMeta.PaddingFrames != meta.PaddingFrames || probeMeta.LeadingSilenceFrames != meta.LeadingSilenceFrames ||
						meta.Source.Frames <= meta.Output.Frames || meta.Output.Frames != expected || meta.PrimingFrames != 1024 {
						t.Fatal("probe/open metadata", probeMeta, meta)
					}
					var got []int16
					buf := make([]int16, 514)
					for {
						n, _, e := d.ReadPCM(ctx, buf)
						got = append(got, buf[:n]...)
						if e == io.EOF {
							break
						}
						if e != nil {
							t.Fatal(e)
						}
					}
					ref, e := os.ReadFile(refPath)
					if e != nil {
						t.Fatal(e)
					}
					if len(got) != int(expected)*channels || len(ref) < len(got)*4 {
						t.Fatal("count", len(got), len(ref))
					}
					maxLSB := 0.0
					for i, v := range got {
						w := float64(math.Float32frombits(binary.LittleEndian.Uint32(ref[4*i:]))) * 32768
						maxLSB = math.Max(maxLSB, math.Abs(float64(v)-w))
					}
					if maxLSB > 0.501 {
						t.Fatal("PCM quant error", maxLSB)
					}
					t.Logf("frames%d trim%d padding%d referenceFrames%d maxLSB%g", meta.Output.Frames, meta.PrimingFrames, meta.PaddingFrames, len(ref)/4/channels, maxLSB)
					for _, off := range []int64{0, 1, 1033, expected - 1, expected} {
						if e = d.Seek(ctx, off); e != nil {
							t.Fatal(e)
						}
						out := make([]int16, (expected-off)*int64(channels))
						n, _, e := d.ReadPCM(ctx, out)
						if n != len(out) || e != nil || !reflect.DeepEqual(out, got[off*int64(channels):]) {
							t.Fatal("seek", off, n, e)
						}
					}
				})
			}
		}
	}
}
