package pcm

import (
	"errors"
	"math"
	"testing"
)

func TestScale(t *testing.T) {
	for _, c := range []struct {
		n    int64
		a, b int
		want int64
	}{{441, 160, 441, 160}, {442, 160, 441, 160}, {math.MaxInt64, 1, 1, math.MaxInt64}, {0, 1, 1, 0}} {
		n, e := ScaleFrames(c.n, c.a, c.b)
		if e != nil || n != c.want {
			t.Fatalf("%+v: %d %v", c, n, e)
		}
	}
	for _, c := range []struct {
		n    int64
		a, b int
	}{{-1, 1, 1}, {1, 0, 1}, {1, 1, 0}, {math.MaxInt64, 2, 1}} {
		if _, e := ScaleFrames(c.n, c.a, c.b); e == nil {
			t.Fatal(c)
		}
	}
}
func TestOutputFrames(t *testing.T) {
	for _, c := range []struct {
		n       int64
		in, out int
		want    int64
	}{{441, 44100, 16000, 160}, {442, 44100, 16000, 161}, {1, 48000, 16000, 1}, {0, 48000, 16000, 0}, {math.MaxInt64, 1, 1, math.MaxInt64}} {
		n, e := OutputFrames(c.n, c.in, c.out)
		if e != nil || n != c.want {
			t.Fatalf("%+v: %d %v", c, n, e)
		}
	}
}
func TestLimits(t *testing.T) {
	l, e := (Limits{}).Validated()
	if e != nil || l.MaxBytes != 512<<20 || l.MaxDurationSeconds != 14400 {
		t.Fatal(l, e)
	}
	if _, e = (Limits{MaxChunks: -1}).Validated(); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}
