package pred

import "testing"

func TestIntra4x4Vertical(t *testing.T) {
	top := []uint8{10, 20, 30, 40}
	left := []uint8{0, 0, 0, 0}
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4Vertical, top, top, left, 0)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if pred[y*4+x] != top[x] {
				t.Fatalf("pred[%d,%d]=%d want %d", y, x, pred[y*4+x], top[x])
			}
		}
	}
}

func TestIntra4x4Horizontal(t *testing.T) {
	top := []uint8{0, 0, 0, 0}
	left := []uint8{10, 20, 30, 40}
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4Horizontal, top, top, left, 0)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if pred[y*4+x] != left[y] {
				t.Fatalf("pred[%d,%d]=%d want %d", y, x, pred[y*4+x], left[y])
			}
		}
	}
}

func TestIntra4x4DC(t *testing.T) {
	top := []uint8{10, 20, 30, 40}
	left := []uint8{10, 20, 30, 40}
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4DC, top, top, left, 0)
	// DC = (10+20+30+40+10+20+30+40+4)/8 = 204/8 = 25
	for i, v := range pred {
		if v != 25 {
			t.Fatalf("pred[%d]=%d want 25", i, v)
		}
	}
}

func TestIntra16x16DC(t *testing.T) {
	top := make([]uint8, 16)
	left := make([]uint8, 16)
	for i := range top {
		top[i] = 100
		left[i] = 100
	}
	pred := make([]uint8, 256)
	PredIntra16x16(pred, Intra16x16DC, top, left, 100)
	for i, v := range pred {
		if v != 100 {
			t.Fatalf("pred[%d]=%d want 100", i, v)
		}
	}
}

func TestIntra16x16Plane(t *testing.T) {
	// Gradient: top goes 0→255, left goes 0→255
	top := make([]uint8, 16)
	left := make([]uint8, 16)
	for i := 0; i < 16; i++ {
		top[i] = uint8(i * 17) // 0, 17, 34, ..., 255
		left[i] = uint8(i * 17)
	}
	pred := make([]uint8, 256)
	PredIntra16x16(pred, Intra16x16Plane, top, left, 0)

	// Should produce a smooth 2D gradient
	t.Logf("Plane pred corners: TL=%d TR=%d BL=%d BR=%d",
		pred[0], pred[15], pred[240], pred[255])
	// Top-left should be low, bottom-right should be high
	if pred[0] > pred[255] {
		t.Error("plane prediction not monotonic")
	}
}

func TestIntra16x16DC_ASM(t *testing.T) {
	if !HasSSE2 { t.Skip("no SSE2") }
	pred := make([]uint8, 256)
	IntraPred16x16DC_ASM(&pred[0], 42)
	for i, v := range pred {
		if v != 42 { t.Fatalf("pred[%d]=%d want 42", i, v) }
	}
}

func TestIntra16x16V_ASM(t *testing.T) {
	if !HasSSE2 { t.Skip("no SSE2") }
	top := make([]uint8, 16)
	for i := range top { top[i] = uint8(i * 17) }
	pred := make([]uint8, 256)
	IntraPred16x16V_ASM(&pred[0], &top[0])
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if pred[y*16+x] != top[x] {
				t.Fatalf("pred[%d,%d]=%d want %d", y, x, pred[y*16+x], top[x])
			}
		}
	}
}

func TestIntra16x16H_ASM(t *testing.T) {
	if !HasSSE2 { t.Skip("no SSE2") }
	left := make([]uint8, 16)
	for i := range left { left[i] = uint8(i * 17) }
	pred := make([]uint8, 256)
	IntraPred16x16H_ASM(&pred[0], &left[0])
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if pred[y*16+x] != left[y] {
				t.Fatalf("pred[%d,%d]=%d want %d", y, x, pred[y*16+x], left[y])
			}
		}
	}
}

func BenchmarkIntra16x16DC(b *testing.B) {
	pred := make([]uint8, 256)
	top := make([]uint8, 16); left := make([]uint8, 16)
	for i := range top { top[i] = 100; left[i] = 100 }
	for i := 0; i < b.N; i++ {
		PredIntra16x16(pred, Intra16x16DC, top, left, 100)
	}
}

func BenchmarkIntra16x16V(b *testing.B) {
	pred := make([]uint8, 256)
	top := make([]uint8, 16); left := make([]uint8, 16)
	for i := range top { top[i] = uint8(i * 17) }
	for i := 0; i < b.N; i++ {
		PredIntra16x16(pred, Intra16x16Vertical, top, left, 0)
	}
}
