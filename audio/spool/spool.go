// Package spool provides bounded temporary random-access storage for audio
// sources that cannot seek. It does not close the caller's original reader.
package spool

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rcarmo/go-264/audio/pcm"
)

// Source owns its temporary file. It is sequential with respect to Close;
// callers must exclude Close while ReadAt is running. Close removes the file.
// Keep the containing directory private; this API is not a filesystem sandbox.
type Source struct {
	file *os.File
	path string
	size int64
}

// Copy retains at most maxBytes bytes on disk (zero selects 512 MiB).
// It reads one extra byte at the limit to distinguish exact EOF from overflow,
// without writing that byte. Scratch is bounded to 32 KiB. A source Read that
// is already blocked cannot be interrupted by context; the source owns that
// capability. dir must be caller-owned; empty uses the OS temporary directory.
// This transient file is not a durable resume/checkpoint artifact.
func Copy(ctx context.Context, src io.Reader, dir string, maxBytes int64) (out *Source, err error) {
	if ctx == nil || src == nil {
		return nil, fmt.Errorf("%w: spool source/context", pcm.ErrMalformed)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("%w: negative spool limit", pcm.ErrLimit)
	}
	if maxBytes == 0 {
		maxBytes = 512 << 20
	}
	f, err := os.CreateTemp(dir, "go264-audio-*")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	out = &Source{file: f, path: f.Name()}
	var buf [32768]byte
	noProgress := 0
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		want := int64(len(buf))
		if left := maxBytes - out.size; left < want {
			want = left + 1
		}
		n, re := src.Read(buf[:int(want)])
		if n < 0 || n > int(want) {
			return nil, fmt.Errorf("%w: invalid source read count", pcm.ErrMalformed)
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if int64(n) > maxBytes-out.size {
			return nil, fmt.Errorf("%w: spool byte cap", pcm.ErrLimit)
		}
		if n > 0 {
			written, we := f.Write(buf[:n])
			if we != nil {
				return nil, we
			}
			if written != n {
				return nil, io.ErrShortWrite
			}
			out.size += int64(n)
			noProgress = 0
		} else {
			noProgress++
			if re == nil && noProgress >= 100 {
				return nil, io.ErrNoProgress
			}
		}
		if re != nil {
			if re != io.EOF {
				return nil, re
			}
			break
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	keep = true
	return out, nil
}
func (s *Source) Size() int64 {
	if s == nil {
		return 0
	}
	return s.size
}
func (s *Source) ReadAt(p []byte, off int64) (int, error) {
	if s == nil || s.file == nil {
		return 0, pcm.ErrClosed
	}
	return s.file.ReadAt(p, off)
}

// Close is idempotent; failed unlink may be retried. No caller source is closed.
func (s *Source) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	if s.file != nil {
		closeErr = s.file.Close()
		s.file = nil
	}
	if s.path != "" {
		e := os.Remove(s.path)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		s.path = ""
	}
	return closeErr
}
