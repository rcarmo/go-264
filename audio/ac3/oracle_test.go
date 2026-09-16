package ac3_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/rcarmo/go-264/audio/ac3"
	"github.com/rcarmo/go-264/audio/mp4"
	"github.com/rcarmo/go-264/audio/pcm"
)

func TestFFmpegFivePointOneChannelOracle(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline FFmpeg AC-3 oracle")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tones.mp4")
	refPath := filepath.Join(dir, "reference.f64le")
	filter := "aevalsrc=0.2*sin(2*PI*301*t)|0.2*sin(2*PI*401*t)|0.2*sin(2*PI*503*t)|0.2*sin(2*PI*61*t)|0.2*sin(2*PI*601*t)|0.2*sin(2*PI*701*t):s=48000:d=1:c=5.1(side)"
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", filter, "-c:a", "ac3", "-b:a", "448k", "-movflags", "+faststart", "-f", "mp4", "-y", path).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:a:0", "-c:a", "pcm_f64le", "-f", "f64le", "-y", refPath).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stat, _ := file.Stat()
	demux, err := mp4.OpenTrack(context.Background(), file, stat.Size(), mp4.Limits{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	track := demux.Track()
	plan, err := track.TimingPlan(int64(track.SampleCount) * ac3.FrameSamples)
	if err != nil {
		t.Fatal(err)
	}
	if track.Codec != "ac-3" || track.Channels != 6 || track.SampleRate != 48000 || plan.PrimingFrames != 256 {
		t.Fatalf("track=%+v plan=%+v", track, plan)
	}
	decoders := make([]*ac3.Decoder, 3)
	for pair := range decoders {
		decoders[pair], err = ac3.NewDecoder(ac3.Config{OutputChannels: 2, SourceChannels: []int{2 * pair, 2*pair + 1}})
		if err != nil {
			t.Fatal(err)
		}
	}
	decoded := make([]float64, int64(track.SampleCount)*ac3.FrameSamples*6)
	packet := make([]byte, 4096)
	for frame := 0; frame < track.SampleCount; frame++ {
		n, _, err := demux.ReadPacket(context.Background(), frame, packet)
		if err != nil {
			t.Fatal(err)
		}
		for pair, decoder := range decoders {
			var output [ac3.FrameSamples * 2]float64
			if frames, _, err := decoder.Decode(context.Background(), packet[:n], output[:]); err != nil || frames != ac3.FrameSamples {
				t.Fatalf("frame=%d pair=%d frames=%d err=%v", frame, pair, frames, err)
			}
			for sample := 0; sample < ac3.FrameSamples; sample++ {
				base := (frame*ac3.FrameSamples + sample) * 6
				decoded[base+2*pair] = output[2*sample]
				decoded[base+2*pair+1] = output[2*sample+1]
			}
		}
	}
	referenceBytes, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}
	referenceFrames := len(referenceBytes) / (8 * 6)
	if referenceFrames != int(plan.OutputFrames)+int(plan.PaddingFrames) {
		t.Fatalf("reference frames=%d plan=%+v", referenceFrames, plan)
	}
	for channel := 0; channel < 6; channel++ {
		var signal, noise, cross, gotEnergy float64
		maxDifference := 0.0
		for frame := 0; frame < referenceFrames; frame++ {
			got := decoded[(frame+int(plan.PrimingFrames))*6+channel]
			want := math.Float64frombits(binary.LittleEndian.Uint64(referenceBytes[(frame*6+channel)*8:]))
			difference := got - want
			signal += want * want
			noise += difference * difference
			cross += got * want
			gotEnergy += got * got
			maxDifference = math.Max(maxDifference, math.Abs(difference))
		}
		snr := 10 * math.Log10(signal/noise)
		correlation := cross / math.Sqrt(gotEnergy*signal)
		if snr < 70 || correlation < 0.9999999 || maxDifference > 0.001 {
			t.Fatalf("channel=%d SNR=%g correlation=%g max=%g", channel, snr, correlation, maxDifference)
		}
		t.Logf("channel=%d SNR=%.2fdB correlation=%.10f max=%g", channel, snr, correlation, maxDifference)
	}
}

func TestDecodeRejectsMalformedAndEAC3Transactionally(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline FFmpeg AC-3 fixture")
	}
	dir := t.TempDir()
	ac3Path := filepath.Join(dir, "tone.ac3")
	if output, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000:duration=0.032", "-c:a", "ac3", "-b:a", "192k", "-frames:a", "1", "-y", ac3Path).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	frame, err := os.ReadFile(ac3Path)
	if err != nil {
		t.Fatal(err)
	}
	// Independent-substream E-AC-3 header: sync, strmtyp=0, substreamid=0,
	// frame-size code 191 (384 bytes), 48 kHz/6 blocks, acmod=1, bsid=16.
	eac3Frame := []byte{0x0b, 0x77, 0x00, 0xbf, 0x32, 0x87, 0xc0}
	decoder, err := ac3.NewDecoder(ac3.Config{OutputChannels: 1})
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := ac3.NewDecoder(ac3.Config{OutputChannels: 1})
	for _, tc := range []struct {
		name  string
		frame []byte
		want  error
	}{
		{name: "truncated", frame: frame[:len(frame)-1], want: pcm.ErrMalformed},
		{name: "trailing", frame: append(append([]byte(nil), frame...), 0), want: pcm.ErrMalformed},
		{name: "crc", frame: func() []byte { damaged := append([]byte(nil), frame...); damaged[len(damaged)/2] ^= 1; return damaged }(), want: pcm.ErrMalformed},
		{name: "eac3", frame: eac3Frame, want: pcm.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := make([]float64, ac3.FrameSamples)
			for i := range destination {
				destination[i] = 1234
			}
			if frames, _, err := decoder.Decode(context.Background(), tc.frame, destination); frames != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("frames=%d error=%v want %v", frames, err, tc.want)
			}
			for _, value := range destination {
				if value != 1234 {
					t.Fatal("failed decode changed destination")
				}
			}
		})
	}
	got := make([]float64, ac3.FrameSamples)
	want := make([]float64, ac3.FrameSamples)
	if _, _, err := decoder.Decode(context.Background(), frame, got); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Decode(context.Background(), frame, want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("failed frames changed decoder state")
	}
}

func BenchmarkFivePointOneDecode(b *testing.B) {
	path := os.Getenv("GO264_AC3_BENCH_FIXTURE")
	if path == "" {
		b.Skip("set GO264_AC3_BENCH_FIXTURE to a progressive MP4 AC-3 file")
	}
	index, _ := strconv.Atoi(os.Getenv("GO264_AC3_BENCH_TRACK"))
	file, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	stat, _ := file.Stat()
	demux, err := mp4.OpenTrack(context.Background(), file, stat.Size(), mp4.Limits{MaxBytes: stat.Size()}, index)
	if err != nil {
		b.Fatal(err)
	}
	packet := make([]byte, demux.Track().SampleSize+4096)
	n, _, err := demux.ReadPacket(context.Background(), 0, packet)
	if err != nil {
		b.Fatal(err)
	}
	decoder, _ := ac3.NewDecoder(ac3.Config{OutputChannels: 2})
	output := make([]float64, ac3.FrameSamples*2)
	b.ReportAllocs()
	b.SetBytes(ac3.FrameSamples)
	for b.Loop() {
		decoder.Reset()
		if _, _, err := decoder.Decode(context.Background(), packet[:n], output); err != nil {
			b.Fatal(fmt.Errorf("decode: %w", err))
		}
	}
}
