package resample

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
)

type cancelStereoSource struct {
	interleavedMem
	calls  int
	cancel context.CancelFunc
}

func (s *cancelStereoSource) ReadFrames(ctx context.Context, dst []float64) (int, error) {
	n, err := s.interleavedMem.ReadFrames(ctx, dst)
	s.calls++
	if s.calls == 3 {
		s.cancel()
		return n, context.Canceled
	}
	return n, err
}

func TestStereoCancellationResumeOracle(t *testing.T) {
	input := make([]float64, 10000*2)
	for i := range input {
		input[i] = math.Sin(float64(i)*0.19) * 0.5
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancelStereoSource{interleavedMem: interleavedMem{data: input, rate: 44100, channels: 2}, cancel: cancel}
	r, err := New(source, 16000)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float64, 10000*2)
	n, err := r.ReadFrames(ctx, buf)
	if n == 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(n, err)
	}
	got := append([]float64(nil), buf[:n*2]...)
	for {
		n, err = r.ReadFrames(context.Background(), buf[:731*2])
		got = append(got, buf[:n*2]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if int64(len(got)) != r.Info().Frames*2 {
		t.Fatal("frame count", len(got))
	}
	for i, v := range got {
		want := directFIR(r, input, int64(i/2), i%2)
		if math.IsNaN(v) || math.Abs(v-want) > 1e-14 {
			t.Fatalf("sample=%d error=%g", i, math.Abs(v-want))
		}
	}
}
