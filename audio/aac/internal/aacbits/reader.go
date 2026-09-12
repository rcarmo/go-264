// Package aacbits provides a small bounded MSB-first bit reader for AAC-LC
// syntax parsing. It is intentionally narrow: read-only, checked, and panic-
// free for future scalar parsers.
package aacbits

import (
	"fmt"

	"github.com/rcarmo/go-264/audio/pcm"
)

// Reader reads bits MSB-first from a bounded byte slice.
//
// It never reads past the supplied slice. All widths are checked before any
// state is mutated.
type Reader struct {
	data []byte
	pos  int
}

// New returns a fresh bounded reader over data. The slice is not copied.
func New(data []byte) *Reader {
	return &Reader{data: data}
}

// Read returns the next n bits as an unsigned MSB-first value.
//
// Valid widths are 0..32. A width outside that range returns pcm.ErrLimit. A
// truncated read returns pcm.ErrMalformed. On error the read position is left
// unchanged.
func (r *Reader) Read(n int) (uint32, error) {
	if r == nil {
		return 0, fmt.Errorf("%w: nil AAC bit reader", pcm.ErrMalformed)
	}
	if n < 0 || n > 32 {
		return 0, fmt.Errorf("%w: AAC bit read width %d", pcm.ErrLimit, n)
	}
	if r.Remaining() < n {
		return 0, fmt.Errorf("%w: truncated AAC bitstream", pcm.ErrMalformed)
	}
	var v uint32
	pos := r.pos
	for i := 0; i < n; i++ {
		v = (v << 1) | uint32((r.data[pos/8]>>uint(7-pos%8))&1)
		pos++
	}
	r.pos = pos
	return v, nil
}

// Position returns the number of bits consumed.
func (r *Reader) Position() int {
	if r == nil {
		return 0
	}
	return r.pos
}

// Remaining returns the number of unread bits.
func (r *Reader) Remaining() int {
	if r == nil {
		return 0
	}
	return len(r.data)*8 - r.pos
}

// Align advances to the next byte boundary.
//
// When already byte-aligned it is a no-op.
func (r *Reader) Align() error {
	if r == nil {
		return fmt.Errorf("%w: nil AAC bit reader", pcm.ErrMalformed)
	}
	if rem := r.pos & 7; rem != 0 {
		r.pos += 8 - rem
	}
	return nil
}
