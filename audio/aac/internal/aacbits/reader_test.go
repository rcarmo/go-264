package aacbits

import (
	"errors"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

func TestReaderReadPositionRemainingAndAlign(t *testing.T) {
	r := New([]byte{0b1011_0010, 0b0110_0001})
	if got := r.Position(); got != 0 {
		t.Fatalf("Position()=%d want 0", got)
	}
	if got := r.Remaining(); got != 16 {
		t.Fatalf("Remaining()=%d want 16", got)
	}
	v, err := r.Read(3)
	if err != nil {
		t.Fatalf("Read(3) error = %v", err)
	}
	if v != 0b101 {
		t.Fatalf("Read(3)=%b want 101", v)
	}
	if got := r.Position(); got != 3 {
		t.Fatalf("Position()=%d want 3", got)
	}
	if got := r.Remaining(); got != 13 {
		t.Fatalf("Remaining()=%d want 13", got)
	}
	if err := r.Align(); err != nil {
		t.Fatalf("Align() error = %v", err)
	}
	if got := r.Position(); got != 8 {
		t.Fatalf("Position()=%d want 8", got)
	}
	v, err = r.Read(8)
	if err != nil {
		t.Fatalf("Read(8) error = %v", err)
	}
	if v != 0b0110_0001 {
		t.Fatalf("Read(8)=%08b want 01100001", v)
	}
	if got := r.Remaining(); got != 0 {
		t.Fatalf("Remaining()=%d want 0", got)
	}
}

func TestReaderReadZeroBits(t *testing.T) {
	r := New([]byte{0xff})
	v, err := r.Read(0)
	if err != nil {
		t.Fatalf("Read(0) error = %v", err)
	}
	if v != 0 {
		t.Fatalf("Read(0)=%d want 0", v)
	}
	if got := r.Position(); got != 0 {
		t.Fatalf("Position()=%d want 0", got)
	}
}

func TestReaderReadErrorsDoNotAdvance(t *testing.T) {
	r := New([]byte{0xaa})
	if _, err := r.Read(-1); !errors.Is(err, pcm.ErrLimit) {
		t.Fatalf("Read(-1) error = %v, want limit", err)
	}
	if got := r.Position(); got != 0 {
		t.Fatalf("Position()=%d want 0", got)
	}
	if _, err := r.Read(33); !errors.Is(err, pcm.ErrLimit) {
		t.Fatalf("Read(33) error = %v, want limit", err)
	}
	if got := r.Position(); got != 0 {
		t.Fatalf("Position()=%d want 0", got)
	}
	if _, err := r.Read(9); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("Read(9) error = %v, want malformed", err)
	}
	if got := r.Position(); got != 0 {
		t.Fatalf("Position()=%d want 0", got)
	}
}

func TestReaderNilReceiver(t *testing.T) {
	var r *Reader
	if _, err := r.Read(1); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("Read on nil error = %v, want malformed", err)
	}
	if got := r.Position(); got != 0 {
		t.Fatalf("Position()=%d want 0", got)
	}
	if got := r.Remaining(); got != 0 {
		t.Fatalf("Remaining()=%d want 0", got)
	}
	if err := r.Align(); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("Align on nil error = %v, want malformed", err)
	}
}

func TestReaderAlignAtEnd(t *testing.T) {
	r := New([]byte{0b1010_0000})
	if _, err := r.Read(4); err != nil {
		t.Fatalf("Read(4) error = %v", err)
	}
	if err := r.Align(); err != nil {
		t.Fatalf("Align() error = %v", err)
	}
	if got := r.Position(); got != 8 {
		t.Fatalf("Position()=%d want 8", got)
	}
}

func FuzzReader(f *testing.F) {
	f.Add([]byte{0b1011_0010, 0b0110_0001}, []byte{3, 5, 8})
	f.Add([]byte{0xff, 0x00, 0x7f}, []byte{1, 7, 4, 12})
	f.Fuzz(func(t *testing.T, data []byte, widths []byte) {
		r := New(data)
		pos := 0
		for _, raw := range widths {
			n := int(raw % 33)
			before := r.Position()
			got, err := r.Read(n)
			if len(data)*8-pos < n {
				if err == nil {
					t.Fatalf("Read(%d) succeeded on truncated input", n)
				}
				if r.Position() != before {
					t.Fatalf("position advanced from %d to %d on error", before, r.Position())
				}
				return
			}
			if err != nil {
				t.Fatalf("Read(%d) error = %v", n, err)
			}
			want := refRead(data, pos, n)
			if got != want {
				t.Fatalf("Read(%d)=%032b want %032b at pos %d", n, got, want, pos)
			}
			pos += n
			if r.Position() != pos {
				t.Fatalf("Position()=%d want %d", r.Position(), pos)
			}
		}
	})
}

func refRead(data []byte, pos, n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		v = (v << 1) | uint32((data[(pos+i)/8]>>uint(7-(pos+i)%8))&1)
	}
	return v
}
