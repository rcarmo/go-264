package cabac

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

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
