package spool

import (
	"bytes"
	"context"
	"errors"
	"github.com/rcarmo/go-264/audio/pcm"
	"io"
	"os"
	"testing"
)

type closeReader struct {
	*bytes.Reader
	closed bool
}

func (r *closeReader) Close() error { r.closed = true; return nil }
func TestCopyBoundaryOwnership(t *testing.T) {
	dir := t.TempDir()
	r := &closeReader{Reader: bytes.NewReader([]byte("abc"))}
	s, e := Copy(context.Background(), r, dir, 3)
	if e != nil || s.Size() != 3 {
		t.Fatal(s, e)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal(files)
	}
	stat, _ := os.Stat(s.path)
	if stat.Mode().Perm() != 0600 {
		t.Fatal(stat.Mode())
	}
	dst := make([]byte, 3)
	n, e := s.ReadAt(dst, 0)
	if n != 3 || e != nil || string(dst) != "abc" {
		t.Fatal(n, e, dst)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if s.Close() != nil || r.closed {
		t.Fatal("ownership")
	}
	files, _ = os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("leak")
	}
	if _, e = s.ReadAt(dst, 0); !errors.Is(e, pcm.ErrClosed) {
		t.Fatal(e)
	}
}

type failing struct {
	err    error
	cancel context.CancelFunc
}

func (r failing) Read(b []byte) (int, error) {
	if r.cancel != nil {
		r.cancel()
	}
	copy(b, "abc")
	return min(3, len(b)), r.err
}

type stalled struct{}

func (stalled) Read([]byte) (int, error) { return 0, nil }
func TestFailuresCleanup(t *testing.T) {
	for _, kind := range []string{"over", "cancel", "error", "stalled"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var r io.Reader
			var want error
			switch kind {
			case "over":
				r = bytes.NewReader([]byte("abcd"))
				want = pcm.ErrLimit
			case "cancel":
				r = failing{cancel: cancel}
				want = context.Canceled
			case "error":
				want = errors.New("input failed")
				r = failing{err: want}
			case "stalled":
				r = stalled{}
				want = io.ErrNoProgress
			}
			if _, e := Copy(ctx, r, dir, 3); !errors.Is(e, want) {
				t.Fatal(e)
			}
			files, _ := os.ReadDir(dir)
			if len(files) != 0 {
				t.Fatal("leak")
			}
		})
	}
}
func TestEmptyAndInvalid(t *testing.T) {
	s, e := Copy(context.Background(), bytes.NewReader(nil), t.TempDir(), 1)
	if e != nil || s.Size() != 0 {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = Copy(nil, bytes.NewReader(nil), t.TempDir(), 1); !errors.Is(e, pcm.ErrMalformed) {
		t.Fatal(e)
	}
	if _, e = Copy(context.Background(), bytes.NewReader(nil), t.TempDir(), -1); !errors.Is(e, pcm.ErrLimit) {
		t.Fatal(e)
	}
}
