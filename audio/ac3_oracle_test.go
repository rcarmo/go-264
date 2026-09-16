package audio_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/audio"
	"github.com/rcarmo/go-264/audio/pcm"
)

func makeAC3OracleMP4(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tones.mp4")
	filter := "aevalsrc=0.2*sin(2*PI*301*t)|0.2*sin(2*PI*401*t)|0.2*sin(2*PI*503*t)|0.2*sin(2*PI*61*t)|0.2*sin(2*PI*601*t)|0.2*sin(2*PI*701*t):s=48000:d=0.2:c=5.1(side)"
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", filter, "-c:a", "ac3", "-b:a", "448k", "-movflags", "+faststart", "-f", "mp4", "-y", path).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	return path
}

func openAC3Oracle(t *testing.T, path string, channels int, selected []int) (*audio.Decoder, *os.File) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, _ := file.Stat()
	track := 0
	decoder, err := audio.Open(context.Background(), file, stat.Size(), audio.Options{
		TargetRate: 48000, TargetChannels: channels, TrackIndex: &track, SourceChannels: selected,
	})
	if err != nil {
		file.Close()
		t.Fatal(err)
	}
	return decoder, file
}

func readAllPCM(t *testing.T, decoder *audio.Decoder, chunk int) []int16 {
	t.Helper()
	buffer := make([]int16, chunk)
	var output []int16
	for {
		n, _, err := decoder.ReadPCM(context.Background(), buffer)
		output = append(output, buffer[:n]...)
		if errors.Is(err, io.EOF) {
			return output
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAC3PublicExtractionDownmixAndSeek(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline FFmpeg AC-3 fixture")
	}
	path := makeAC3OracleMP4(t)
	for _, test := range []struct {
		name     string
		channels int
		selected []int
	}{
		{name: "mono-downmix", channels: 1},
		{name: "stereo-downmix", channels: 2},
		{name: "centre", channels: 1, selected: []int{2}},
		{name: "centre-lfe", channels: 2, selected: []int{2, 3}},
		{name: "surrounds", channels: 2, selected: []int{4, 5}},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoder, file := openAC3Oracle(t, path, test.channels, test.selected)
			defer file.Close()
			defer decoder.Close()
			metadata := decoder.Metadata()
			if decoder.TrackIndex() != 0 || !reflect.DeepEqual(decoder.SourceChannels(), test.selected) || metadata.Source.Channels != 6 || metadata.Output.Channels != test.channels || metadata.Source.SampleRate != 48000 || metadata.Output.SampleRate != 48000 || metadata.PrimingFrames != 256 {
				t.Fatalf("track=%d selected=%v metadata=%+v", decoder.TrackIndex(), decoder.SourceChannels(), metadata)
			}
			all := readAllPCM(t, decoder, 514*test.channels)
			if len(all) != int(metadata.Output.Frames)*test.channels {
				t.Fatalf("samples=%d metadata=%+v", len(all), metadata)
			}
			for _, frame := range []int64{0, 1, 255, 1535, metadata.Output.Frames - 1, metadata.Output.Frames} {
				if frame < 0 {
					continue
				}
				if err := decoder.Seek(context.Background(), frame); err != nil {
					t.Fatal(err)
				}
				got := make([]int16, (metadata.Output.Frames-frame)*int64(test.channels))
				n, span, err := decoder.ReadPCM(context.Background(), got)
				if n != len(got) || err != nil || span.StartFrame != frame || !reflect.DeepEqual(got, all[frame*int64(test.channels):]) {
					t.Fatalf("seek=%d n=%d span=%+v err=%v", frame, n, span, err)
				}
			}
		})
	}
}

func TestAC3SelectionValidationAndCancellation(t *testing.T) {
	if os.Getenv("GO264_AUDIO_ORACLE") != "1" {
		t.Skip("offline FFmpeg AC-3 fixture")
	}
	path := makeAC3OracleMP4(t)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stat, _ := file.Stat()
	track := 0
	for _, options := range []audio.Options{
		{TargetRate: 48000, TargetChannels: 1, TrackIndex: &track, SourceChannels: []int{6}},
		{TargetRate: 48000, TargetChannels: 2, TrackIndex: &track, SourceChannels: []int{2}},
	} {
		if _, err := audio.Open(context.Background(), file, stat.Size(), options); !errors.Is(err, pcm.ErrMalformed) && !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatalf("options=%+v error=%v", options, err)
		}
	}
	decoder, err := audio.Open(context.Background(), file, stat.Size(), audio.Options{TargetRate: 48000, TargetChannels: 2, TrackIndex: &track})
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	buffer := make([]int16, 128)
	for i := range buffer {
		buffer[i] = 1234
	}
	if n, span, err := decoder.ReadPCM(ctx, buffer); n != 0 || !errors.Is(err, context.Canceled) || span.StartFrame != 0 {
		t.Fatalf("canceled read=(%d,%+v,%v)", n, span, err)
	}
	for _, value := range buffer {
		if value != 1234 {
			t.Fatal("canceled read changed destination")
		}
	}
	resumed := readAllPCM(t, decoder, 512)
	fresh, freshFile := openAC3Oracle(t, path, 2, nil)
	defer freshFile.Close()
	defer fresh.Close()
	if want := readAllPCM(t, fresh, 777*2); !reflect.DeepEqual(resumed, want) {
		t.Fatal("cancellation changed resumed PCM")
	}
}
