package audio_test

import (
	"bytes"
	"context"
	"github.com/rcarmo/go-264/audio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCanonicalMP4VsDecodedWAV(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline canonical MP4 oracle")
	}
	ctx := context.Background()
	dir := t.TempDir()
	p, wav := filepath.Join(dir, "input.m4a"), filepath.Join(dir, "ref.wav")
	if b, e := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "aevalsrc=0.25*sin(2*PI*997*t)|0.3*sin(2*PI*1301*t):s=48000:d=1", "-c:a", "aac", p).CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	if b, e := exec.Command("ffmpeg", "-v", "error", "-i", p, "-af", "atrim=end_sample=48000", "-c:a", "pcm_s16le", wav).CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	decode := func(path string) []int16 {
		f, e := os.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		s, _ := f.Stat()
		d, e := audio.Open(ctx, f, s.Size(), audio.Options{})
		if e != nil {
			t.Fatal(e)
		}
		defer d.Close()
		if d.Metadata().Output.Frames != 16000 {
			t.Fatal(d.Metadata())
		}
		var out []int16
		buf := make([]int16, 4096)
		for {
			n, _, e := d.ReadPCM(ctx, buf)
			out = append(out, buf[:n]...)
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
		}
		if e = d.Seek(ctx, 617); e != nil {
			t.Fatal(e)
		}
		n, _, e := d.ReadPCM(ctx, buf)
		if e != nil || !equalPCM(out[617:617+n], buf[:n]) {
			t.Fatal("resampled seek", e)
		}
		return out
	}
	got, want := decode(p), decode(wav)
	maxDiff := 0
	for i, v := range got {
		delta := int(v) - int(want[i])
		if delta < 0 {
			delta = -delta
		}
		maxDiff = max(maxDiff, delta)
	}
	t.Logf("canonical MP4 vs independent FFmpeg PCM+same FIR maxLSB%d", maxDiff)
	if maxDiff > 2 {
		t.Fatal(maxDiff)
	}
}
func equalPCM(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func benchmarkM4A(b *testing.B) []byte {
	b.Helper()
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		b.Skip("offline fixture generation required")
	}
	p := filepath.Join(b.TempDir(), "bench.m4a")
	if log, e := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000:duration=2", "-ac", "2", "-c:a", "aac", p).CombinedOutput(); e != nil {
		b.Fatal(e, string(log))
	}
	data, e := os.ReadFile(p)
	if e != nil {
		b.Fatal(e)
	}
	return data
}
func BenchmarkMP4Decode(b *testing.B) {
	data := benchmarkM4A(b)
	for _, rate := range []int{48000, 16000} {
		name := "source-rate"
		if rate == 16000 {
			name = "canonical"
		}
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			buf := make([]int16, 4096)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d, e := audio.Open(ctx, bytes.NewReader(data), int64(len(data)), audio.Options{TargetRate: rate, TargetChannels: 1})
				if e != nil {
					b.Fatal(e)
				}
				count := 0
				for {
					n, _, e := d.ReadPCM(ctx, buf)
					count += n
					if e == io.EOF {
						break
					}
					if e != nil {
						b.Fatal(e)
					}
				}
				d.Close()
				if count != 2*rate {
					b.Fatal(count)
				}
			}
		})
	}
}
