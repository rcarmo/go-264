package mp4

import (
	"context"
	"io"
)

// readAtFull avoids allocating a SectionReader for each packet/header. Accept
// ReaderAt implementations that make short progress; never spin on no progress.
// Full data plus an error is successful, matching io.ReadFull's existing rule.
func readAtFull(ctx context.Context, src io.ReaderAt, off int64, dst []byte) (int, error) {
	n := 0
	for n < len(dst) {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		m, err := src.ReadAt(dst[n:], off+int64(n))
		if m < 0 || m > len(dst)-n {
			return n, io.ErrUnexpectedEOF
		}
		n += m
		if n == len(dst) {
			return n, nil
		}
		if err != nil {
			if err == io.EOF && n > 0 {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
		if m == 0 {
			return n, io.ErrNoProgress
		}
	}
	return n, nil
}
