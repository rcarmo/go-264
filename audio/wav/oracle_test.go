package wav

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

// Offline opt-in oracle only. The runtime WAV/audio library never executes
// subprocesses. Test samples are deterministic synthetic integer PCM.
func TestFFmpegPCMOracle(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("set GO264_AUDIO_ORACLE=1 for offline FFmpeg PCM comparison")
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(bin, "-version").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("oracle: %s", bytes.SplitN(version, []byte("\n"), 2)[0])
	for _, depth := range []int{8, 16, 24, 32} {
		for _, ch := range []int{1, 2} {
			for _, rate := range []int{8000, 16000, 22050, 44100, 48000} {
				// Widen extrema safely and vary every input frame/channel deterministically.
				vals := make([]int32, 257*ch)
				max := int64(1) << (depth - 1)
				for i := range vals {
					vals[i] = int32((int64(i)*7919)%(2*max) - max)
				}
				vals[0] = int32(-max)
				vals[len(vals)-1] = int32(max - 1)
				// The WAV fixture helper takes unsigned values for 8-bit PCM.
				if depth == 8 {
					for i := range vals {
						vals[i] += 128
					}
				}
				data := makePCMFixture(formatPCM, ch, rate, depth, vals, nil, true)
				dir := t.TempDir()
				in := filepath.Join(dir, "input.wav")
				out := filepath.Join(dir, "reference.s32le")
				if err = os.WriteFile(in, data, 0600); err != nil {
					t.Fatal(err)
				}
				if log, e := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-i", in, "-c:a", "pcm_s32le", "-f", "s32le", out).CombinedOutput(); e != nil {
					t.Fatalf("oracle %d/%d/%d: %v %s", depth, ch, rate, e, log)
				}
				expected, e := os.ReadFile(out)
				if e != nil {
					t.Fatal(e)
				}
				if len(expected) != len(vals)*4 {
					t.Fatal("oracle length", len(expected))
				}
				r, e := Open(context.Background(), bytes.NewReader(data), int64(len(data)), pcm.Limits{})
				if e != nil {
					t.Fatal(e)
				}
				decoded := make([]float64, len(vals))
				n, e := r.ReadFrames(context.Background(), decoded)
				if e != nil || n != len(vals)/ch {
					t.Fatal(n, e)
				}
				for i, v := range decoded {
					got := int64(v * 2147483648)
					want := int64(int32(binary.LittleEndian.Uint32(expected[i*4:])))
					if got != want {
						t.Fatalf("depth%d ch%d rate%d scalar%d: got %d want %d", depth, ch, rate, i, got, want)
					}
				}
			}
		}
	}
}
