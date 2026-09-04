package nal

import (
	"errors"
	"io"
	"testing"
)

func TestReaderCheckedConsumptionAndPaddedPeek(t *testing.T) {
	r := NewReader([]byte{0x80})
	if got := r.PeekBits(16); got != 0x8000 || r.Position() != 0 || r.Err() != nil {
		t.Fatalf("padded peek changed reader: value=%x position=%d err=%v", got, r.Position(), r.Err())
	}
	if r.ReadBits(8) != 0x80 || r.Err() != nil {
		t.Fatal("valid byte rejected")
	}
	r.PeekBits(16)
	if r.Err() != nil {
		t.Fatal("EOF peek must be speculative")
	}
	r.ReadBit()
	if !errors.Is(r.Err(), io.ErrUnexpectedEOF) {
		t.Fatalf("consumed EOF: %v", r.Err())
	}
	r.Seek(0)
	r.ReadBits(8)
	if !errors.Is(r.Err(), io.ErrUnexpectedEOF) {
		t.Fatal("seek cleared first error")
	}
}

func TestReaderExpGolombErrors(t *testing.T) {
	for _, data := range [][]byte{nil, {0}, {0, 0, 0, 0, 0}} {
		r := NewReader(data)
		r.ReadUE()
		if r.Err() == nil {
			t.Fatalf("invalid UE accepted: %x", data)
		}
	}
	// 32 zero prefix bits, one delimiter, then 32 zero suffix bits is uint32 max.
	maxUE := []byte{0, 0, 0, 0, 0x80, 0, 0, 0, 0}
	r := NewReader(maxUE)
	if v := r.ReadUE(); v != ^uint32(0) || r.Err() != nil {
		t.Fatalf("uint32 max UE: %d %v", v, r.Err())
	}
	r = NewReader(maxUE)
	r.ReadSE()
	if !errors.Is(r.Err(), ErrInvalidSyntax) {
		t.Fatalf("signed overflow: %v", r.Err())
	}
}

func TestContainsEmulationPreventionByte(t *testing.T) {
	if containsEmulationPreventionByte([]byte{0, 0, 2, 3}) {
		t.Fatal("false positive EPB detection")
	}
	if !containsEmulationPreventionByte([]byte{0x12, 0, 0, 3, 0x80}) {
		t.Fatal("missed EPB detection")
	}
	if NewReader([]byte{0x12, 0x34}).hasEPB {
		t.Fatal("reader marked no-EPB payload as EPB-bearing")
	}
	if !NewReader([]byte{0, 0, 3, 0x80}).hasEPB {
		t.Fatal("reader failed to mark EPB-bearing payload")
	}
}

func TestReadBits(t *testing.T) {
	// 0xAB = 10101011
	r := NewReader([]byte{0xAB})
	if v := r.ReadBits(4); v != 0xA {
		t.Fatalf("got 0x%X want 0xA", v)
	}
	if v := r.ReadBits(4); v != 0xB {
		t.Fatalf("got 0x%X want 0xB", v)
	}
}

func TestReadUE(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint32
	}{
		{"0 (1)", []byte{0x80}, 0},       // 1... → 0
		{"1 (010)", []byte{0x40}, 1},     // 010... → 1
		{"2 (011)", []byte{0x60}, 2},     // 011... → 2
		{"3 (00100)", []byte{0x20}, 3},   // 00100... → 3
		{"4 (00101)", []byte{0x28}, 4},   // 00101... → 4
		{"5 (00110)", []byte{0x30}, 5},   // 00110... → 5
		{"6 (00111)", []byte{0x38}, 6},   // 00111... → 6
		{"7 (0001000)", []byte{0x10}, 7}, // 0001000... → 7
	}
	for _, tt := range tests {
		r := NewReader(tt.data)
		got := r.ReadUE()
		if got != tt.want {
			t.Errorf("ReadUE(%s) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestReadSE(t *testing.T) {
	tests := []struct {
		data []byte
		want int32
	}{
		{[]byte{0x80}, 0},  // UE=0 → SE=0
		{[]byte{0x40}, 1},  // UE=1 → SE=1
		{[]byte{0x60}, -1}, // UE=2 → SE=-1
		{[]byte{0x20}, 2},  // UE=3 → SE=2
		{[]byte{0x28}, -2}, // UE=4 → SE=-2
	}
	for _, tt := range tests {
		r := NewReader(tt.data)
		got := r.ReadSE()
		if got != tt.want {
			t.Errorf("ReadSE(%x) = %d, want %d", tt.data, got, tt.want)
		}
	}
}

func TestEmulationPrevention(t *testing.T) {
	// 0x00 0x00 0x03 0x01 → should read as 0x00 0x00 0x01
	data := []byte{0x00, 0x00, 0x03, 0x01}
	r := NewReader(data)
	b0 := r.ReadU8()
	b1 := r.ReadU8()
	b2 := r.ReadU8()
	if b0 != 0x00 || b1 != 0x00 || b2 != 0x01 {
		t.Fatalf("got %02x %02x %02x, want 00 00 01", b0, b1, b2)
	}
}

func TestReadBitsFastPathEmulationPrevention(t *testing.T) {
	data := []byte{0x12, 0x00, 0x00, 0x03, 0x45, 0x67}
	r := NewReader(data)
	if v := r.ReadBits(24); v != 0x120000 {
		t.Fatalf("ReadBits(24)=0x%06x want 0x120000", v)
	}
	if v := r.ReadBits(16); v != 0x4567 {
		t.Fatalf("ReadBits(16)=0x%04x want 0x4567", v)
	}
}

func TestReadBitsNoEPBFastPathMatchesBitByBit(t *testing.T) {
	data := []byte{0b10110110, 0b01011100, 0b11110000, 0x12, 0x34}
	for start := 0; start < len(data)*8; start++ {
		for n := 0; n <= 40; n++ {
			rFast := NewReader(data)
			rSlow := NewReader(data)
			rFast.Seek(start)
			rSlow.Seek(start)
			got := rFast.ReadBits(n)
			wantN := n
			if wantN > 32 {
				wantN = 32
			}
			var want uint32
			for i := 0; i < wantN; i++ {
				want = (want << 1) | rSlow.ReadBit()
			}
			if got != want || rFast.Position() != rSlow.Position() {
				t.Fatalf("start=%d n=%d got=0x%x/%d want=0x%x/%d", start, n, got, rFast.Position(), want, rSlow.Position())
			}
		}
	}
}

func TestReadBitsMixedAlignmentFastPath(t *testing.T) {
	r := NewReader([]byte{0b10110110, 0b01011100, 0b11110000})
	if v := r.ReadBits(3); v != 0b101 {
		t.Fatalf("first bits=%03b", v)
	}
	if v := r.ReadBits(13); v != 0b1011001011100 {
		t.Fatalf("mixed bits=%013b", v)
	}
	if v := r.ReadBits(8); v != 0b11110000 {
		t.Fatalf("final byte=%08b", v)
	}
}

func TestReadBitsDefensiveBounds(t *testing.T) {
	r := NewReader([]byte{0xff, 0x00, 0xaa, 0x55, 0x80})
	if got := r.ReadBits(-1); got != 0 || r.Position() != 0 {
		t.Fatalf("ReadBits(-1) got=%d pos=%d, want 0 pos 0", got, r.Position())
	}
	if got := r.ReadBits(0); got != 0 || r.Position() != 0 {
		t.Fatalf("ReadBits(0) got=%d pos=%d, want 0 pos 0", got, r.Position())
	}
	if got := r.ReadBits(40); got != 0xff00aa55 {
		t.Fatalf("ReadBits(40 clamped)=0x%08x want 0xff00aa55", got)
	}
	r.ReadBits(32)
	if left := r.BitsLeft(); left != 0 {
		t.Fatalf("BitsLeft past EOF = %d, want 0", left)
	}
}

func TestByteAlignSkipsEmulationPrevention(t *testing.T) {
	r := NewReader([]byte{0x00, 0x00, 0x03, 0x80})
	// Consume one bit from the second 0x00, then align. Crossing the byte boundary
	// lands on the EPB byte, which must be skipped just like ReadBit/readByte.
	r.Seek(8)
	if bit := r.ReadBit(); bit != 0 {
		t.Fatalf("unexpected bit before align: %d", bit)
	}
	r.ByteAlign()
	if got := r.ReadBits(8); got != 0x80 {
		t.Fatalf("ByteAlign failed to skip EPB: got 0x%02x want 0x80", got)
	}
}

func TestPeekBitsFastPathMatchesReadBits(t *testing.T) {
	data := []byte{0b10110110, 0b01011100, 0b11110000, 0x12, 0x34}
	for start := 0; start < 24; start++ {
		for n := 0; n <= 32; n++ {
			rPeek := NewReader(data)
			rRead := NewReader(data)
			rPeek.Seek(start)
			rRead.Seek(start)
			got := rPeek.PeekBits(n)
			want := rRead.ReadBits(n)
			if got != want || rPeek.Position() != start {
				t.Fatalf("start=%d n=%d got=0x%x want=0x%x pos=%d", start, n, got, want, rPeek.Position())
			}
		}
	}
}

func TestPeekBitsEmulationPreventionFallback(t *testing.T) {
	data := []byte{0x12, 0x00, 0x00, 0x03, 0x45, 0x67}
	rPeek := NewReader(data)
	rRead := NewReader(data)
	got := rPeek.PeekBits(32)
	want := rRead.ReadBits(32)
	if got != want || rPeek.Position() != 0 {
		t.Fatalf("PeekBits EPB got=0x%x want=0x%x pos=%d", got, want, rPeek.Position())
	}
}

func BenchmarkPeekBits16(b *testing.B) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = uint8(i*37 + 11)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := NewReader(data)
		var sum uint32
		for r.BitsLeft() >= 16 {
			sum ^= r.PeekBits(16)
			r.ReadBits(5)
		}
		if sum == 0xdeadbeef {
			b.Fatal(sum)
		}
	}
}

func TestSeekDefensiveBounds(t *testing.T) {
	r := NewReader([]byte{0xaa, 0x55})
	r.Seek(-10)
	if r.Position() != 0 || r.BitsLeft() != 16 {
		t.Fatalf("negative seek pos=%d left=%d, want pos=0 left=16", r.Position(), r.BitsLeft())
	}
	r.Seek(5)
	if r.Position() != 5 {
		t.Fatalf("seek pos=%d want 5", r.Position())
	}
	r.Seek(999)
	if r.Position() != 16 || r.BitsLeft() != 0 {
		t.Fatalf("past-end seek pos=%d left=%d, want pos=16 left=0", r.Position(), r.BitsLeft())
	}
}

func BenchmarkReadBitsByteAligned(b *testing.B) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = uint8(i*37 + 11)
	}
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		r := NewReader(data)
		var sum uint32
		for !r.EOF() {
			sum ^= r.ReadBits(8)
		}
		if sum == 0xdeadbeef {
			b.Fatal(sum)
		}
	}
}

func BenchmarkReadBitsUnaligned(b *testing.B) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = uint8(i*37 + 11)
	}
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		r := NewReader(data)
		_ = r.ReadBits(3)
		var sum uint32
		for r.BitsLeft() >= 5 {
			sum ^= r.ReadBits(5)
		}
		if sum == 0xdeadbeef {
			b.Fatal(sum)
		}
	}
}
