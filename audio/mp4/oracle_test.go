package mp4

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rcarmo/go-264/audio/pcm"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFFmpegPackets(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline FFmpeg/ffprobe oracle")
	}
	for _, rate := range []int{44100, 48000} {
		for _, ch := range []int{1, 2} {
			for _, fast := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-%d-fast%v", rate, ch, fast), func(t *testing.T) {
					p := filepath.Join(t.TempDir(), "tone.m4a")
					args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=997:sample_rate=%d:duration=0.2", rate), "-ac", strconv.Itoa(ch), "-c:a", "aac", "-b:a", "96k"}
					if fast {
						args = append(args, "-movflags", "+faststart")
					}
					args = append(args, p)
					if b, e := exec.Command("ffmpeg", args...).CombinedOutput(); e != nil {
						t.Fatal(e, string(b))
					}
					f, e := os.Open(p)
					if e != nil {
						t.Fatal(e)
					}
					defer f.Close()
					s, _ := f.Stat()
					r, e := Open(context.Background(), f, s.Size(), Limits{})
					if e != nil {
						t.Fatal(e)
					}
					track := r.Track()
					if track.SampleRate != rate || track.Channels != ch {
						t.Fatal(track)
					}
					b, e := exec.Command("ffprobe", "-v", "error", "-ignore_editlist", "1", "-show_packets", "-show_entries", "packet=pos,size,dts,pts,duration", "-of", "json", p).Output()
					if e != nil {
						t.Fatal(e)
					}
					var ref struct {
						Packets []struct {
							Pos      string
							Size     string
							Dts      int64
							Pts      int64
							Duration int64
						}
					}
					if e = json.Unmarshal(b, &ref); e != nil {
						t.Fatal(e)
					}
					if len(ref.Packets) != track.SampleCount {
						t.Fatal(len(ref.Packets), track.SampleCount)
					}
					for i, v := range ref.Packets {
						pos, _ := strconv.ParseInt(v.Pos, 10, 64)
						size, _ := strconv.Atoi(v.Size)
						buf := make([]byte, size)
						n, info, e := r.ReadPacket(context.Background(), i, buf)
						if e != nil || n != size || info.Offset != pos || int64(info.DecodeTime) != v.Dts || info.PresentationTime != v.Pts || int64(info.Duration) != v.Duration {
							t.Fatalf("packet%d %+v ref%+v n%d err%v", i, info, v, n, e)
						}
						direct := make([]byte, size)
						if _, e = f.ReadAt(direct, pos); e != nil {
							t.Fatal(e)
						}
						if !bytes.Equal(buf, direct) {
							t.Fatal("payload")
						}
					}
					if _, _, e = r.ReadPacket(context.Background(), track.SampleCount, nil); e != io.EOF {
						t.Fatal(e)
					}
				})
			}
		}
	}
}
func TestDescriptorLimits(t *testing.T) {
	p := esdsPayload([]byte{0x12, 0x10})[4:]
	for i := 0; i < 10; i++ {
		p = descriptor(3, append([]byte{0, 1, 0}, p...))
	}
	if _, e := parseESDS(append([]byte{0, 0, 0, 0}, p...)); !errors.Is(e, pcm.ErrLimit) {
		t.Fatal(e)
	}
}
func FuzzDemux(f *testing.F) {
	f.Add(box("moov", makeMVHD(0, 0, 1000, 200), false))
	f.Fuzz(func(t *testing.T, b []byte) {
		r, e := Open(context.Background(), bytes.NewReader(b), int64(len(b)), Limits{MaxBytes: 1 << 20, MaxSamples: 1024, MaxBoxes: 256, MaxDepth: 8, MaxTableBytes: 1 << 20})
		if e == nil {
			_, _, _ = r.ReadPacket(context.Background(), 0, make([]byte, 4096))
		}
	})
}
