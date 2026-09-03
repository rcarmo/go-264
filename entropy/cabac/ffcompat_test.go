package cabac

import (
	"errors"
	"io"
	"testing"

	"github.com/rcarmo/go-264/nal"
)

func TestFFCompatPaddingRefillsTheRegisterAndIsBounded(t *testing.T) {
	for _, offset := range []int{0, 1} {
		r := nal.NewReader([]byte{0x80, 0x80})
		d := &CABACDecoder{r: r, ffBuf: []byte{0x80, 0x80}, ffPos: 2 + offset, codILow: 0x10000}
		d.refill()
		if r.Err() != nil || d.codILow != 1 || d.ffPos != 4+offset {
			t.Fatalf("offset %d: low=%x pos=%d err=%v", offset, d.codILow, d.ffPos, r.Err())
		}
		// Simulate consuming all 16 padded bits. Another refill must not extend
		// an exhausted slice indefinitely.
		d.codILow = 0x10000
		d.refill()
		if !errors.Is(r.Err(), io.ErrUnexpectedEOF) {
			t.Fatalf("offset %d: %v", offset, r.Err())
		}
	}
}

func TestFFCompatTerminationChecksConsumedNotPrefetchedBits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pos       int
		low       uint32
		wantError bool
	}{
		{"prefetched_one_byte", 3, 0x3fc0002, false},  // consumed 9 of 16 physical bits
		{"prefetched_two_bytes", 4, 0x3fc0001, false}, // consumed exactly 16 physical bits
		{"one_bit_past_end", 4, 0x3fc0002, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			physical := 2
			r := nal.NewReader(make([]byte, physical))
			d := &CABACDecoder{r: r, ffBuf: make([]byte, physical), ffPos: tc.pos, codILow: tc.low, codIRange: 510}
			if d.DecodeTerminateFF() != 1 {
				t.Fatal("expected termination")
			}
			if (r.Err() != nil) != tc.wantError {
				t.Fatalf("err=%v wantError=%v", r.Err(), tc.wantError)
			}
		})
	}
}

func TestFFCompatShortTerminalTailBothAlignments(t *testing.T) {
	for _, offset := range []int{0, 1} {
		data := append(make([]byte, offset), 0xfe, 0x80)
		r := nal.NewReader(data)
		r.ReadBits(offset * 8)
		d := &CABACDecoder{r: r}
		d.InitFFCompat()
		if d.DecodeTerminateFF() != 1 || r.Err() != nil {
			t.Fatalf("offset %d: %v", offset, r.Err())
		}
	}
}

func TestFFCompatRefillOneRealByteWithShiftedSentinel(t *testing.T) {
	r := nal.NewReader([]byte{0x80})
	d := &CABACDecoder{r: r, ffBuf: []byte{0x80}, codILow: 0x20000}
	before := d.ffPos*8 - 16 + ctz32(d.codILow)
	d.refill()
	after := d.ffPos*8 - 16 + ctz32(d.codILow)
	// One real 0x80 byte plus virtual 0x00, shifted by ctz(low)-16 == 1.
	if r.Err() != nil || d.ffPos != 2 || d.codILow != 0x20002 || before != after {
		t.Fatalf("low=%x pos=%d consumed=%d->%d err=%v", d.codILow, d.ffPos, before, after, r.Err())
	}
}

func TestFFCompatTablesPreserveSignedInitializers(t *testing.T) {
	// FFmpeg stores ff_h264_cabac_tables as uint8_t but several LPS entries are
	// written as negative C initializers. The Go table must keep their modulo-256
	// values; parsing them as unsigned decimal magnitudes makes CABAC drift at the
	// second bin of the bbb IDR frame (state=4/range=494 expects LPS=216).
	if got := lpsRange[2*(494&0xC0)+4]; got != 216 {
		t.Fatalf("lpsRange[388]=%d, want 216", got)
	}
	if got := lpsRange[2*(510&0xC0)+104]; got != 16 {
		t.Fatalf("lpsRange[488]=%d, want 16", got)
	}
}

func TestInitFFCompatUsesUnalignedThreeByteSeed(t *testing.T) {
	dec := &CABACDecoder{r: nal.NewReader([]byte{0x64, 0x12, 0x78, 0xaa})}
	dec.InitFFCompat()
	if dec.codILow != 26233314 {
		t.Fatalf("low=%d, want 26233314", dec.codILow)
	}
	if dec.codIRange != 0x1fe {
		t.Fatalf("range=%d, want 510", dec.codIRange)
	}
	if dec.ffPos != 3 {
		t.Fatalf("ffPos=%d, want 3", dec.ffPos)
	}
}

func TestInitFFCompatUsesAlignedTwoByteSeedAtOddPayloadOffset(t *testing.T) {
	r := nal.NewReader([]byte{0xff, 0x64, 0x12, 0x78})
	r.ReadBits(8)
	dec := &CABACDecoder{r: r}
	dec.InitFFCompat()
	if dec.codILow != 26233344 {
		t.Fatalf("low=%d, want 26233344", dec.codILow)
	}
	if dec.ffPos != 2 {
		t.Fatalf("ffPos=%d, want 2", dec.ffPos)
	}
}

func TestDecodeBinFFMatchesFFmpegReferencePrefix(t *testing.T) {
	dec := &CABACDecoder{r: nal.NewReader([]byte{0x64, 0x12, 0x78, 0xaa})}
	dec.InitFFCompat()
	state := uint8(104)
	if got := dec.DecodeBinFF(&state); got != 0 {
		t.Fatalf("bin=%d, want 0", got)
	}
	if state != 106 {
		t.Fatalf("state=%d, want 106", state)
	}
	if dec.codIRange != 494 {
		t.Fatalf("range=%d, want 494", dec.codIRange)
	}
	if dec.codILow != 26233314 {
		t.Fatalf("low=%d, want 26233314", dec.codILow)
	}

	state = 4
	if got := dec.DecodeBinFF(&state); got != 0 {
		t.Fatalf("second bin=%d, want 0", got)
	}
	if state != 6 {
		t.Fatalf("second state=%d, want 6", state)
	}
	if dec.codIRange != 278 {
		t.Fatalf("second range=%d, want 278", dec.codIRange)
	}
	if dec.codILow != 26233314 {
		t.Fatalf("second low=%d, want 26233314", dec.codILow)
	}
}
