package convert

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

type layoutSource struct {
	data           []float64
	channels, pos  int
	reportChannels int
}

func (s *layoutSource) Info() pcm.Info {
	return pcm.Info{SampleRate: 48000, Channels: s.reportChannels, Frames: int64(len(s.data) / max(s.channels, 1))}
}
func (s *layoutSource) SeekFrame(_ context.Context, n int64) error {
	s.pos = int(n) * s.channels
	return nil
}
func (s *layoutSource) ReadFrames(_ context.Context, d []float64) (int, error) {
	n := copy(d, s.data[s.pos:])
	s.pos += n
	if n < len(d) {
		return n / s.channels, io.EOF
	}
	return n / s.channels, nil
}

func newLayoutSource(frames, channels int) *layoutSource {
	data := make([]float64, frames*channels)
	for i := range data {
		data[i] = float64(i%97-48) / 64
	}
	return &layoutSource{data: data, channels: channels, reportChannels: channels}
}

func readAll(t *testing.T, r *Reader, chunkFrames int) []float64 {
	t.Helper()
	buf := make([]float64, chunkFrames*r.Info().Channels)
	var out []float64
	for {
		n, err := r.ReadFrames(context.Background(), buf)
		out = append(out, buf[:n*r.Info().Channels]...)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMultichannelPassThrough(t *testing.T) {
	for channels := 3; channels <= pcm.MaxChannels; channels++ {
		t.Run(fmt.Sprint(channels), func(t *testing.T) {
			src := newLayoutSource(1001, channels)
			want := append([]float64(nil), src.data...)
			r, err := New(src, channels)
			if err != nil {
				t.Fatal(err)
			}
			if r.Info() != src.Info() {
				t.Fatal(r.Info(), src.Info())
			}
			got := readAll(t, r, 64)
			if len(got) != len(want) {
				t.Fatal(len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample %d: %v != %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestMultichannelRejectsConversions(t *testing.T) {
	for _, tc := range [][2]int{{6, 2}, {6, 1}, {2, 6}, {1, 6}, {pcm.MaxChannels + 1, pcm.MaxChannels + 1}, {0, 0}} {
		src := newLayoutSource(16, max(tc[0], 1))
		src.reportChannels = tc[0]
		if _, err := New(src, tc[1]); !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatalf("%d->%d: %v", tc[0], tc[1], err)
		}
	}
}

func TestMonoStereoConversionsStillWork(t *testing.T) {
	stereo := &layoutSource{data: []float64{1, 0, 0.5, -0.5, -1, 0.25}, channels: 2, reportChannels: 2}
	r, err := New(stereo, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, r, 8)
	want := []float64{0.5, 0, -0.375}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
	mono := &layoutSource{data: []float64{0.25, -0.75}, channels: 1, reportChannels: 1}
	if r, err = New(mono, 2); err != nil {
		t.Fatal(err)
	}
	got = readAll(t, r, 8)
	want = []float64{0.25, 0.25, -0.75, -0.75}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
}
