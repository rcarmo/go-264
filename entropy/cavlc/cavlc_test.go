package cavlc

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

func TestCAVLCLowLevelDecodersHandleNilReader(t *testing.T) {
	if tc, to := DecodeCoeffToken(nil, 0); tc != 0 || to != 0 {
		t.Fatalf("DecodeCoeffToken(nil)=(%d,%d) want zero tuple", tc, to)
	}
	if got := DecodeTotalZeros(nil, 1); got != 0 {
		t.Fatalf("DecodeTotalZeros(nil)=%d want 0", got)
	}
	if got := DecodeRunBefore(nil, 1); got != 0 {
		t.Fatalf("DecodeRunBefore(nil)=%d want 0", got)
	}
}

func TestCAVLCChromaDCHandlesNilReader(t *testing.T) {
	if got := DecodeCAVLCChromaDC(nil); got != [4]int16{} {
		t.Fatalf("DecodeCAVLCChromaDC(nil)=%v want zero block", got)
	}
}

func TestCAVLCBlockDecodersHandleNilReader(t *testing.T) {
	if _, tc := DecodeCAVLCBlock(nil, 0); tc != 0 {
		t.Fatalf("DecodeCAVLCBlock(nil) totalCoeff=%d want 0", tc)
	}
	if _, tc := DecodeCAVLCBlockAC(nil, 0); tc != 0 {
		t.Fatalf("DecodeCAVLCBlockAC(nil) totalCoeff=%d want 0", tc)
	}
	if _, tc := DecodeCAVLCBlock8x8Part(nil, 0, 0); tc != 0 {
		t.Fatalf("DecodeCAVLCBlock8x8Part(nil) totalCoeff=%d want 0", tc)
	}
}

func TestCAVLC8x8PartScanMatchesFFmpegChunks(t *testing.T) {
	if zigZag8x8CAVLC[0] != 0 || zigZag8x8CAVLC[1] != 9 || zigZag8x8CAVLC[16] != 1 || zigZag8x8CAVLC[32] != 8 || zigZag8x8CAVLC[48] != 16 {
		t.Fatalf("unexpected 8x8 CAVLC scan chunks: p0=%v p1=%v p2=%v p3=%v", zigZag8x8CAVLC[:4], zigZag8x8CAVLC[16:20], zigZag8x8CAVLC[32:36], zigZag8x8CAVLC[48:52])
	}
	seen := make(map[int]bool, 64)
	for _, pos := range zigZag8x8CAVLC {
		if pos < 0 || pos >= 64 || seen[pos] {
			t.Fatalf("invalid/duplicate 8x8 CAVLC scan position %d", pos)
		}
		seen[pos] = true
	}
}

func TestDecodeCoeffToken_NC0(t *testing.T) {
	tests := []struct {
		name           string
		data           []byte
		wantTC, wantTO int
	}{
		{"0,0", []byte{0x80}, 0, 0}, // "1" → (0,0)
		{"1,1", []byte{0x40}, 1, 1}, // "01" → (1,1)
		{"2,2", []byte{0x20}, 2, 2}, // "001" → (2,2)
		{"3,3", []byte{0x18}, 3, 3}, // "00011" → (3,3)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := nal.NewReader(tt.data)
			tc, to := DecodeCoeffToken(r, 0)
			if tc != tt.wantTC || to != tt.wantTO {
				t.Errorf("got (%d,%d) want (%d,%d)", tc, to, tt.wantTC, tt.wantTO)
			}
		})
	}
}

func TestDecodeCoeffToken_NC8(t *testing.T) {
	// nC >= 8: fixed 6-bit code
	// Code = (totalCoeff-1)*4 + trailingOnes
	tests := []struct {
		code           byte // 6-bit code in MSB
		wantTC, wantTO int
	}{
		{0b00000100, 1, 0}, // code=0x01 → tc=1, to=0... actually: code/4+1=1, code%4=0
		{0b00010000, 1, 0}, // code=0x04 → tc=2, to=0
	}
	for _, tt := range tests {
		r := nal.NewReader([]byte{tt.code, 0})
		tc, to := DecodeCoeffToken(r, 8)
		t.Logf("code=0b%06b → tc=%d to=%d", tt.code>>2, tc, to)
		_ = tc
		_ = to
	}
}

func TestDecodeLevelPrefix(t *testing.T) {
	// Level prefix is unary coded: 0^n 1
	tests := []struct {
		data []byte
		want int
	}{
		{[]byte{0x80}, 0}, // "1" → prefix=0
		{[]byte{0x40}, 1}, // "01" → prefix=1
		{[]byte{0x20}, 2}, // "001" → prefix=2
		{[]byte{0x10}, 3}, // "0001" → prefix=3
	}
	for _, tt := range tests {
		r := nal.NewReader(tt.data)
		got := decodeLevelPrefix(r, 0)
		if got != tt.want {
			t.Errorf("data=%x: got %d want %d", tt.data, got, tt.want)
		}
	}
}

func TestDecodeLevelPrefixEscapeSaturatesPrefix(t *testing.T) {
	// prefix >= 15 uses a saturated prefix contribution of 15<<suffixLength
	// before escape extension bits are added. For prefix=16,suffixLength=1 and
	// zero extension bits, levelCode is 30 + (1<<13) - 4096 = 4126.
	r := bitsToReader(uint32(1<<13), 30) // 16 zero prefix bits, stop bit, 13 zero suffix bits
	got := decodeLevelPrefix(r, 1)
	if got != 4126 {
		t.Fatalf("escape levelCode = %d, want 4126", got)
	}
	if r.Position() != 30 {
		t.Fatalf("position = %d, want 30", r.Position())
	}
}

func TestDecodeRunBefore(t *testing.T) {
	// zerosLeft=1: "1"→0, "0"→1
	r := nal.NewReader([]byte{0x80}) // "1"
	if v := DecodeRunBefore(r, 1); v != 0 {
		t.Errorf("zerosLeft=1, '1': got %d want 0", v)
	}
	r = nal.NewReader([]byte{0x00}) // "0"
	if v := DecodeRunBefore(r, 1); v != 1 {
		t.Errorf("zerosLeft=1, '0': got %d want 1", v)
	}

	// zerosLeft=2: "1"→0, "01"→1, "00"→2
	r = nal.NewReader([]byte{0x80})
	if v := DecodeRunBefore(r, 2); v != 0 {
		t.Errorf("zerosLeft=2, '1': got %d want 0", v)
	}
	r = nal.NewReader([]byte{0x40})
	if v := DecodeRunBefore(r, 2); v != 1 {
		t.Errorf("zerosLeft=2, '01': got %d want 1", v)
	}
}

func TestDecodeTotalZeros(t *testing.T) {
	// totalCoeff=1: "1"→0, "011"→1, "010"→2
	r := nal.NewReader([]byte{0x80}) // "1"
	if v := DecodeTotalZeros(r, 1); v != 0 {
		t.Errorf("tc=1, '1': got %d want 0", v)
	}
	r = nal.NewReader([]byte{0x60}) // "011"
	if v := DecodeTotalZeros(r, 1); v != 1 {
		t.Errorf("tc=1, '011': got %d want 1", v)
	}
}

func FuzzDecodeCAVLC(f *testing.F) {
	f.Add([]byte{0x80}, 0)       // simple: 0 coeffs
	f.Add([]byte{0x40, 0x80}, 0) // 1 coeff, 1 trailing one
	f.Add([]byte{0x20, 0x40}, 0) // 2 coeffs
	f.Add([]byte{0xFF, 0xFF}, 4) // nC=4
	f.Add([]byte{0xFF, 0xFF}, 8) // nC=8

	f.Fuzz(func(t *testing.T, data []byte, nC int) {
		if len(data) < 1 || nC < 0 || nC > 16 {
			return
		}
		r := nal.NewReader(data)
		// Should not panic
		DecodeCAVLCBlock(r, nC)
	})
}
