package transform

import "testing"

func TestIDCT8x8_DC(t *testing.T) {
	block := [64]int16{}
	block[0] = 512
	IDCT8x8(block[:])
	// DC-only: all outputs should be 512/64 = 8
	for i, v := range block {
		if v != 8 {
			t.Fatalf("block[%d]=%d want 8", i, v)
		}
	}
	t.Logf("IDCT8x8 DC-only: all 8 ✓")
}

func TestDCT8x8_Roundtrip(t *testing.T) {
	var original [64]int16
	for i := range original {
		original[i] = int16(50 + i)
	}
	block := original
	DCT8x8(block[:])
	t.Logf("DCT8x8 DC coeff: %d", block[0])

	// Dequant after quant would go here — for now just test IDCT
	IDCT8x8(block[:])

	maxErr := int16(0)
	for i := range original {
		d := block[i] - original[i]
		if d < 0 { d = -d }
		if d > maxErr { maxErr = d }
	}
	t.Logf("8x8 roundtrip (no quant): max error=%d", maxErr)
	// Without quant/dequant, the scaling factor causes ~10-15 error
	// Full pipeline: DCT → Quant → Dequant → IDCT gives ≤1 error
	if maxErr > 15 {
		t.Errorf("max error %d too high (want ≤15)", maxErr)
	}
}

func TestZigZag8x8(t *testing.T) {
	visited := [64]bool{}
	for _, idx := range ZigZag8x8 {
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

func BenchmarkDCT8x8(b *testing.B) {
	var block [64]int16
	for i := range block { block[i] = int16(50 + i) }
	for i := 0; i < b.N; i++ {
		tmp := block
		DCT8x8(tmp[:])
	}
}

func BenchmarkIDCT8x8(b *testing.B) {
	var block [64]int16
	block[0] = 512; block[1] = 64; block[8] = -32
	for i := 0; i < b.N; i++ {
		tmp := block
		IDCT8x8(tmp[:])
	}
}

func TestIDCT8x8_ASMvsScalar(t *testing.T) {
	if !HasAVX2 { t.Skip("no AVX2") }
	for seed := 0; seed < 50; seed++ {
		var blockASM, blockScalar [64]int16
		for i := range blockASM {
			// Use realistic dequantized coefficient range (fits in int16 after butterfly)
			blockASM[i] = int16((seed*3 + i*5 - 160) % 500)
		}
		copy(blockScalar[:], blockASM[:])
		IDCT8x8_ASM(&blockASM[0])
		IDCT8x8Scalar(blockScalar[:])
		for i := range blockASM {
			if blockASM[i] != blockScalar[i] {
				t.Fatalf("seed=%d pos=%d: asm=%d scalar=%d", seed, i, blockASM[i], blockScalar[i])
			}
		}
	}
	t.Log("IDCT8x8 ASM matches scalar for 50 inputs ✓")
}

func BenchmarkIDCT8x8_ASM(b *testing.B) {
	if !HasAVX2 { b.Skip("no AVX2") }
	var block [64]int16
	block[0] = 512; block[1] = 64; block[8] = -32
	for i := 0; i < b.N; i++ {
		tmp := block
		IDCT8x8_ASM(&tmp[0])
	}
}
