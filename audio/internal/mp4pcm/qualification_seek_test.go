package mp4pcm

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

func TestQualificationRepeatedSeekCancelReplay(t *testing.T) {
	ctx := context.Background()
	src := &mutableSource{data: fixture(false)}
	r, err := Open(ctx, src, int64(len(src.data)), pcm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	info := r.Info()
	positions := []int64{0, 1, info.Frames / 2, info.Frames - 1, 0}
	for _, pos := range positions {
		// A cached read need not call ReaderAt. Reopen for each injected-I/O
		// cancellation so the trigger tests replay rather than cached output.
		r, err = Open(ctx, src, int64(len(src.data)), pcm.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.SeekFrame(ctx, pos); err != nil {
			t.Fatalf("SeekFrame(%d) error = %v", pos, err)
		}
		cancelCtx, cancel := context.WithCancel(ctx)
		src.cancel = cancel
		src.trigger = src.calls + 2
		dst := []float64{7}
		n, err := r.ReadFrames(cancelCtx, dst)
		if n != 0 || !errors.Is(err, context.Canceled) || dst[0] != 7 {
			t.Fatalf("canceled ReadFrames(%d) = (%d, %v, %v), want (0, context.Canceled, [7])", pos, n, err, dst)
		}
		src.cancel = nil
		cancel()

		remaining := int(info.Frames - pos)
		buf := make([]float64, remaining+1)
		for i := range buf {
			buf[i] = 9
		}
		n, err = r.ReadFrames(ctx, buf)
		if n != remaining || err != io.EOF {
			t.Fatalf("retry ReadFrames(%d) = (%d, %v), want (%d, EOF)", pos, n, err, remaining)
		}
		for i, v := range buf[:n] {
			if v != 0 {
				t.Fatalf("retry sample[%d] at pos %d = %v, want 0", i, pos, v)
			}
		}
		if buf[n] != 9 {
			t.Fatalf("retry sentinel at pos %d = %v, want 9", pos, buf[n])
		}
	}
}
