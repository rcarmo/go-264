package resample

import (
	"context"
	"errors"
	"github.com/rcarmo/go-264/audio/pcm"
	"io"
	"math"
	"reflect"
	"testing"
)

type mem struct {
	data []float64
	rate int
	pos  int
}

func (m *mem) Info() pcm.Info {
	return pcm.Info{SampleRate: m.rate, Channels: 1, Frames: int64(len(m.data))}
}
func (m *mem) SeekFrame(_ context.Context, n int64) error { m.pos = int(n); return nil }
func (m *mem) ReadFrames(ctx context.Context, d []float64) (int, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	n := copy(d, m.data[m.pos:])
	m.pos += n
	if n < len(d) {
		return n, io.EOF
	}
	return n, nil
}
func render(t *testing.T, r *Reader, chunk int) []float64 {
	t.Helper()
	var result []float64
	b := make([]float64, chunk)
	for {
		n, e := r.ReadFrames(context.Background(), b)
		result = append(result, b[:n]...)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	return result
}
func signal(n, rate int, hz float64) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = 0.5 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate))
	}
	return x
}
func TestCountsChunksSeek(t *testing.T) {
	for _, rate := range []int{8000, 16000, 22050, 44100, 48000, 96000, 192000} {
		x := signal(rate+37, rate, 1000)
		a, e := New(&mem{data: x, rate: rate}, 16000)
		if e != nil {
			t.Fatal(e)
		}
		one := render(t, a, 397)
		want, _ := pcm.OutputFrames(int64(len(x)), rate, 16000)
		if len(one) != int(want) {
			t.Fatal(rate, len(one), want)
		}
		b, _ := New(&mem{data: x, rate: rate}, 16000)
		two := render(t, b, 1601)
		if !reflect.DeepEqual(one, two) {
			t.Fatal("chunk mismatch", rate)
		}
		for _, off := range []int{0, 1, 127, len(one) - 1, len(one)} {
			if e = b.SeekFrame(context.Background(), int64(off)); e != nil {
				t.Fatal(e)
			}
			got := render(t, b, 999)
			if !reflect.DeepEqual(got, one[off:]) && len(got) > 0 {
				t.Fatal("seek mismatch", rate, off)
			}
		}
	}
}
func TestPassbandAndAlias(t *testing.T) {
	for _, hz := range []float64{1000, 12000} {
		r, e := New(&mem{data: signal(48000, 48000, hz), rate: 48000}, 16000)
		if e != nil {
			t.Fatal(e)
		}
		out := render(t, r, 512)
		energy := 0.0
		for _, v := range out[200 : len(out)-200] {
			energy += v * v
		}
		rms := math.Sqrt(energy / float64(len(out)-400))
		if hz == 1000 && math.Abs(rms-0.5/math.Sqrt2) > 0.001 {
			t.Fatal("passband", rms)
		}
		if hz == 12000 && rms > 0.00005 {
			t.Fatal("alias", rms)
		}
	}
}
func TestImpulseSilenceCancel(t *testing.T) {
	for _, n := range []int{0, 1, 64, 1024} {
		x := make([]float64, n)
		r, _ := New(&mem{data: x, rate: 44100}, 16000)
		out := render(t, r, 7)
		for _, v := range out {
			if v != 0 {
				t.Fatal(v)
			}
		}
	}
	x := make([]float64, 4800)
	x[2400] = 1
	r, _ := New(&mem{data: x, rate: 48000}, 16000)
	out := render(t, r, 51)
	peak := 0
	for i, v := range out {
		if math.Abs(v) > math.Abs(out[peak]) {
			peak = i
		}
	}
	if peak != 800 {
		t.Fatal("delay", peak)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := r.ReadFrames(ctx, make([]float64, 7)); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := r.SeekFrame(context.Background(), -1); e == nil {
		t.Fatal("seek")
	}
}
func Benchmark48000To16000(b *testing.B) {
	x := signal(48000, 48000, 1000)
	r, _ := New(&mem{data: x, rate: 48000}, 16000)
	buf := make([]float64, 16000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.SeekFrame(context.Background(), 0)
		_, _ = r.ReadFrames(context.Background(), buf)
	}
}

// A source may return valid frames and cancellation in the same read. The
// resampler must retain those frames and resume without duplicating/dropping.
type cancelSource struct {
	mem
	calls int
	stop  context.CancelFunc
}

func (s *cancelSource) ReadFrames(ctx context.Context, b []float64) (int, error) {
	n, e := s.mem.ReadFrames(ctx, b)
	s.calls++
	if s.calls == 3 && s.stop != nil {
		s.stop()
		return n, context.Canceled
	}
	return n, e
}
func TestCancellationResume(t *testing.T) {
	x := signal(16000, 48000, 997)
	ref, _ := New(&mem{data: x, rate: 48000}, 16000)
	want := render(t, ref, 2000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &cancelSource{mem: mem{data: x, rate: 48000}, stop: cancel}
	r, _ := New(src, 16000)
	buf := make([]float64, 5000)
	n, e := r.ReadFrames(ctx, buf)
	if !errors.Is(e, context.Canceled) || n == 0 {
		t.Fatal(n, e)
	}
	got := append([]float64{}, buf[:n]...)
	got = append(got, render(t, r, 731)...)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("cancel/resume differs", len(got), len(want))
	}
}
