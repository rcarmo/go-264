package me

import "testing"

func TestSAD16x16_Identical(t *testing.T) {
	block := make([]uint8, 16*16)
	for i := range block {
		block[i] = uint8(i)
	}
	if sad := SAD16x16(block, block, 16, 16); sad != 0 {
		t.Fatalf("identical blocks: SAD=%d want 0", sad)
	}
}

func TestSAD16x16_Different(t *testing.T) {
	a := make([]uint8, 16*16)
	b := make([]uint8, 16*16)
	for i := range a {
		a[i] = 100
		b[i] = 200
	}
	// Each pixel differs by 100, 256 pixels → SAD = 25600
	if sad := SAD16x16(a, b, 16, 16); sad != 25600 {
		t.Fatalf("SAD=%d want 25600", sad)
	}
}

func TestSATD4x4(t *testing.T) {
	// Identical blocks → SATD = 0
	a := make([]uint8, 4*4)
	b := make([]uint8, 4*4)
	for i := range a {
		a[i] = 128
		b[i] = 128
	}
	if satd := SATD4x4(a, b, 4, 4); satd != 0 {
		t.Fatalf("identical: SATD=%d want 0", satd)
	}

	// Single pixel difference
	a[0] = 200
	satd := SATD4x4(a, b, 4, 4)
	t.Logf("single pixel diff: SATD=%d", satd)
	if satd == 0 {
		t.Fatal("expected non-zero SATD")
	}
}

func BenchmarkSAD16x16(b *testing.B) {
	blkA := make([]uint8, 16*16)
	blkB := make([]uint8, 16*16)
	for i := range blkA {
		blkA[i] = uint8(i * 3)
		blkB[i] = uint8(i * 7)
	}
	for i := 0; i < b.N; i++ {
		SAD16x16(blkA, blkB, 16, 16)
	}
}

func BenchmarkSATD4x4(b *testing.B) {
	blkA := make([]uint8, 4*4)
	blkB := make([]uint8, 4*4)
	for i := range blkA {
		blkA[i] = uint8(i * 5)
		blkB[i] = uint8(i * 11)
	}
	for i := 0; i < b.N; i++ {
		SATD4x4(blkA, blkB, 4, 4)
	}
}

func TestSAD16x16_ASMvsScalar(t *testing.T) {
	if !hasSSE2 {
		t.Skip("no SSE2")
	}
	a := make([]uint8, 16*16)
	b := make([]uint8, 16*16)
	for i := range a {
		a[i] = uint8(i*3 + 7)
		b[i] = uint8(i*7 + 13)
	}

	// Scalar
	var sadScalar uint32
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			d := int(a[y*16+x]) - int(b[y*16+x])
			if d < 0 {
				d = -d
			}
			sadScalar += uint32(d)
		}
	}

	sadASM := SAD16x16_ASM(&a[0], &b[0], 16, 16)
	if sadASM != sadScalar {
		t.Fatalf("ASM=%d scalar=%d", sadASM, sadScalar)
	}
	t.Logf("SAD16x16 ASM matches scalar: %d ✓", sadASM)
}

func BenchmarkSAD16x16_ASM(b *testing.B) {
	if !hasSSE2 {
		b.Skip("no SSE2")
	}
	blkA := make([]uint8, 16*16)
	blkB := make([]uint8, 16*16)
	for i := range blkA {
		blkA[i] = uint8(i * 3)
		blkB[i] = uint8(i * 7)
	}
	for i := 0; i < b.N; i++ {
		SAD16x16_ASM(&blkA[0], &blkB[0], 16, 16)
	}
}
