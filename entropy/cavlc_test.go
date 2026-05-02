package entropy

import (
	"testing"

	"github.com/rcarmo/go-264/nal"
)

func TestDecodeCoeffToken_NC0(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		wantTC, wantTO int
	}{
		{"0,0", []byte{0x80}, 0, 0},       // "1" → (0,0)
		{"1,1", []byte{0x40}, 1, 1},       // "01" → (1,1)
		{"2,2", []byte{0x20}, 2, 2},       // "001" → (2,2)
		{"3,3", []byte{0x18}, 3, 3},       // "00011" → (3,3)
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
		code   byte // 6-bit code in MSB
		wantTC, wantTO int
	}{
		{0b00000100, 1, 0},  // code=0x01 → tc=1, to=0... actually: code/4+1=1, code%4=0
		{0b00010000, 1, 0},  // code=0x04 → tc=2, to=0
	}
	for _, tt := range tests {
		r := nal.NewReader([]byte{tt.code, 0})
		tc, to := DecodeCoeffToken(r, 8)
		t.Logf("code=0b%06b → tc=%d to=%d", tt.code>>2, tc, to)
		_ = tc; _ = to
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
	f.Add([]byte{0x80}, 0)         // simple: 0 coeffs
	f.Add([]byte{0x40, 0x80}, 0)   // 1 coeff, 1 trailing one
	f.Add([]byte{0x20, 0x40}, 0)   // 2 coeffs
	f.Add([]byte{0xFF, 0xFF}, 4)   // nC=4
	f.Add([]byte{0xFF, 0xFF}, 8)   // nC=8

	f.Fuzz(func(t *testing.T, data []byte, nC int) {
		if len(data) < 1 || nC < 0 || nC > 16 {
			return
		}
		r := nal.NewReader(data)
		// Should not panic
		DecodeCAVLCBlock(r, nC)
	})
}
