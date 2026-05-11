package nal

import "testing"

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
