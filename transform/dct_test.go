package transform

import "testing"

func TestIDCT4x4(t *testing.T) {
	// The H.264 integer transform doesn't roundtrip alone —
	// the scaling is split between quant and dequant.
	// Test the IDCT with pre-scaled coefficients instead.
	// Coefficients from spec example (already in IDCT-input form):
	block := [16]int16{
		256, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
	}
	IDCT4x4(block[:])
	// DC-only input: all outputs should be 256/64 = 4
	for i, v := range block {
		if v != 4 {
			t.Errorf("block[%d]=%d want 4", i, v)
		}
	}
	t.Logf("IDCT DC-only: %v", block)

	// Test with mixed coefficients
	block2 := [16]int16{
		256, 64, 0, 0,
		64, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
	}
	IDCT4x4(block2[:])
	t.Logf("IDCT mixed: %v", block2)
	// Should produce a smooth gradient
}

func TestQuantDequant(t *testing.T) {
	// Full DCT → Quant → Dequant → IDCT roundtrip at low QP
	block := [16]int16{
		52, 55, 61, 66,
		70, 61, 64, 73,
		63, 59, 55, 90,
		67, 61, 68, 104,
	}
	original := block

	DCT4x4(block[:])
	t.Logf("After DCT: %v", block)

	Quant4x4(block[:], 10) // Low QP = high quality
	t.Logf("After quant(QP=10): %v", block)

	nz := 0
	for _, v := range block {
		if v != 0 {
			nz++
		}
	}
	t.Logf("Non-zero coefficients: %d/16", nz)

	Dequant4x4(block[:], 10)
	t.Logf("After dequant: %v", block)

	IDCT4x4(block[:])
	t.Logf("Reconstructed: %v", block)

	maxErr := int16(0)
	for i := range original {
		d := block[i] - original[i]
		if d < 0 {
			d = -d
		}
		if d > maxErr {
			maxErr = d
		}
	}
	t.Logf("Full roundtrip (QP=10): max error=%d", maxErr)
	if maxErr > 10 {
		t.Errorf("max error %d too high for QP=10", maxErr)
	}
}

func TestZigZag(t *testing.T) {
	// Verify zig-zag visits all 16 positions exactly once
	visited := [16]bool{}
	for _, idx := range ZigZag4x4 {
		if visited[idx] {
			t.Fatalf("zig-zag visits position %d twice", idx)
		}
		visited[idx] = true
	}
	for i, v := range visited {
		if !v {
			t.Fatalf("zig-zag misses position %d", i)
		}
	}
}

func BenchmarkDCT4x4(b *testing.B) {
	block := [16]int16{52, 55, 61, 66, 70, 61, 64, 73, 63, 59, 55, 90, 67, 61, 68, 104}
	for i := 0; i < b.N; i++ {
		tmp := block
		DCT4x4(tmp[:])
	}
}

func BenchmarkIDCT4x4(b *testing.B) {
	block := [16]int16{1048, -44, 46, 4, 40, 0, -4, 6, -40, -4, 12, 2, 2, 0, 2, -4}
	for i := 0; i < b.N; i++ {
		tmp := block
		IDCT4x4(tmp[:])
	}
}

func BenchmarkIDCT4x4Batch16(b *testing.B) {
	var blocks [16][16]int16
	for i := range blocks {
		blocks[i] = [16]int16{1048, -44, 46, 4, 40, 0, -4, 6, -40, -4, 12, 2, 2, 0, 2, -4}
	}
	b.SetBytes(16 * 16 * 2)
	for i := 0; i < b.N; i++ {
		tmp := blocks
		IDCT4x4Batch(tmp[:])
	}
}

func TestIDCT4x4_SIMDvsScalar(t *testing.T) {
	if !HasAVX2 {
		t.Skip("no AVX2")
	}
	// Test many random-ish inputs
	for seed := 0; seed < 100; seed++ {
		var blockASM, blockScalar [16]int16
		for i := range blockASM {
			blockASM[i] = int16(seed*17 + i*31 - 200)
		}
		copy(blockScalar[:], blockASM[:])

		IDCT4x4_AVX2(&blockASM[0])
		IDCT4x4Scalar(blockScalar[:])

		for i := range blockASM {
			if blockASM[i] != blockScalar[i] {
				t.Fatalf("seed=%d pos=%d: asm=%d scalar=%d", seed, i, blockASM[i], blockScalar[i])
			}
		}
	}
	t.Log("IDCT4x4 ASM matches scalar for 100 inputs ✓")
}

func TestDCT4x4_SIMDvsScalar(t *testing.T) {
	if !HasAVX2 {
		t.Skip("no AVX2")
	}
	for seed := 0; seed < 100; seed++ {
		var blockASM, blockScalar [16]int16
		for i := range blockASM {
			blockASM[i] = int16(seed*13 + i*7 - 100)
		}
		copy(blockScalar[:], blockASM[:])

		DCT4x4_AVX2(&blockASM[0])
		DCT4x4Scalar(blockScalar[:])

		for i := range blockASM {
			if blockASM[i] != blockScalar[i] {
				t.Fatalf("seed=%d pos=%d: asm=%d scalar=%d", seed, i, blockASM[i], blockScalar[i])
			}
		}
	}
	t.Log("DCT4x4 ASM matches scalar for 100 inputs ✓")
}
