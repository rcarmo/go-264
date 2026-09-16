package mp4

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type pieceReader struct {
	data   []byte
	chunk  int
	calls  int
	cancel context.CancelFunc
	count  int
	err    error
}

func (r *pieceReader) ReadAt(dst []byte, off int64) (int, error) {
	r.calls++
	if r.cancel != nil {
		r.cancel()
	}
	if r.count != 0 {
		return r.count, r.err
	}
	if r.chunk == 0 {
		return 0, r.err
	}
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(dst[:min(len(dst), r.chunk)], r.data[off:])
	return n, r.err
}

func TestReadAtFullProgressBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, chunk := range []int{1, 2, 3, 8} {
		r := &pieceReader{data: []byte("abcdefghij"), chunk: chunk}
		out := make([]byte, 6)
		n, err := readAtFull(ctx, r, 2, out)
		if err != nil || n != 6 || string(out) != "cdefgh" {
			t.Fatal(chunk, n, err, string(out))
		}
	}
	for _, tc := range []struct {
		name  string
		r     pieceReader
		wantN int
		want  error
	}{
		{"empty", pieceReader{chunk: 1}, 0, io.EOF},
		{"short-eof", pieceReader{data: []byte("abc"), chunk: 2, err: io.EOF}, 2, io.ErrUnexpectedEOF},
		{"no-progress", pieceReader{}, 0, io.ErrNoProgress},
		{"negative-count", pieceReader{count: -1}, 0, io.ErrUnexpectedEOF},
		{"oversized-count", pieceReader{count: 9}, 0, io.ErrUnexpectedEOF},
		{"complete-with-eof", pieceReader{data: []byte("abcd"), chunk: 4, err: io.EOF}, 4, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := readAtFull(ctx, &tc.r, 0, make([]byte, 4))
			if n != tc.wantN || !errors.Is(err, tc.want) {
				t.Fatal(n, err)
			}
		})
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	r := &pieceReader{data: []byte("abcd"), chunk: 1, cancel: cancel}
	n, err := readAtFull(cancelCtx, r, 0, make([]byte, 4))
	if n != 1 || !errors.Is(err, context.Canceled) || r.calls != 1 {
		t.Fatal(n, err, r.calls)
	}
}

func TestReadAtFullZeroAlloc(t *testing.T) {
	r := bytes.NewReader(make([]byte, 128))
	out := make([]byte, 64)
	ctx := context.Background()
	allocs := testing.AllocsPerRun(100, func() {
		n, e := readAtFull(ctx, r, 8, out)
		if e != nil || n != 64 {
			panic("read")
		}
	})
	if allocs != 0 {
		t.Fatal(allocs)
	}
}
