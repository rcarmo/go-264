package resample

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

type interleavedMem struct {
	data                []float64
	rate, channels, pos int
}

func (m *interleavedMem) Info() pcm.Info {
	return pcm.Info{SampleRate: m.rate, Channels: m.channels, Frames: int64(len(m.data) / m.channels)}
}
func (m *interleavedMem) SeekFrame(_ context.Context, f int64) error {
	m.pos = int(f) * m.channels
	return nil
}
func (m *interleavedMem) ReadFrames(_ context.Context, d []float64) (int, error) {
	n := copy(d, m.data[m.pos:])
	m.pos += n
	if n < len(d) {
		return n / m.channels, io.EOF
	}
	return n / m.channels, nil
}

func renderChannels(t *testing.T, r *Reader, chunkFrames int) []float64 {
	t.Helper()
	channels := r.Info().Channels
	buf := make([]float64, chunkFrames*channels)
	var out []float64
	for {
		n, err := r.ReadFrames(context.Background(), buf)
		out = append(out, buf[:n*channels]...)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSizedRingRetentionAndParity(t *testing.T) {
	for _, pair := range [][2]int{{48000, 16000}, {192000, 8000}, {8000, 48000}, {44100, 16000}} {
		for _, ch := range []int{1, 2} {
			t.Run(fmt.Sprintf("%d-%d/ch%d", pair[0], pair[1], ch), func(t *testing.T) {
				data := make([]float64, 11000*ch)
				for i := range data {
					data[i] = float64(i%31-15) / 16
				}
				r, err := New(&interleavedMem{data: data, rate: pair[0], channels: ch}, pair[1])
				if err != nil {
					t.Fatal(err)
				}
				if r.ringFrames < r.taps+sourceReadFrames || r.ringFrames > 4096 || len(r.ring) != r.ringFrames*ch || len(r.scratch) != sourceReadFrames*ch {
					t.Fatal("ring bounds", r.ringFrames, r.taps)
				}
				// Compare the smaller capacity with the previous fixed8192 layout,
				// keeping identical filter coefficients and arithmetic paths.
				large, err := New(&interleavedMem{data: data, rate: pair[0], channels: ch}, pair[1])
				if err != nil {
					t.Fatal(err)
				}
				large.ringFrames = 8192
				large.ringMask = 8191
				large.ring = make([]float64, 8192*ch)
				got, want := renderChannels(t, r, 257), renderChannels(t, large, 257)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("ring capacity changed PCM")
				}
				frame := r.Info().Frames / 2
				if err := r.SeekFrame(context.Background(), frame); err != nil {
					t.Fatal(err)
				}
				tail := renderChannels(t, r, 3)
				if !reflect.DeepEqual(tail, got[frame*int64(ch):]) {
					t.Fatal("seek after wrap")
				}
			})
		}
	}
}

func TestSameRateReaderAllocatesNoDSPStorage(t *testing.T) {
	src := &mem{data: make([]float64, 128), rate: 16000}
	r, err := New(src, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if r.coeff != nil || r.ring != nil || r.scratch != nil || r.ringFrames != 0 {
		t.Fatal("unneeded same-rate buffers")
	}
	allocs := testing.AllocsPerRun(100, func() {
		r, err := New(src, 16000)
		if err != nil || r.Info().Frames != 128 {
			panic("new")
		}
	})
	if allocs > 1 {
		t.Fatal("same-rate allocations", allocs)
	}
}
