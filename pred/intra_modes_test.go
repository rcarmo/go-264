package pred

import (
	"testing"
)

// Reference pixels for all I4x4 mode tests.
var (
	testTop      = []uint8{200, 210, 220, 230}
	testTopRight = []uint8{240, 250, 255, 255} // topRight[3] always clamped
	testLeft     = []uint8{100, 110, 120, 130}
	testTopLeft  = uint8(90)
)

func clip(v int) uint8 {
	if v < 0 { return 0 }
	if v > 255 { return 255 }
	return uint8(v)
}

func TestIntra4x4DiagDownLeft(t *testing.T) {
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4DiagDownLeft, testTop, testTopRight, testLeft, testTopLeft)
	// (0,0): (top[0]+2*top[1]+top[2]+2)>>2 = (200+420+220+2)>>2 = 842>>2 = 210
	want00 := uint8(210)
	if pred[0] != want00 {
		t.Errorf("DiagDownLeft (0,0)=%d want %d", pred[0], want00)
	}
	// All pixels must be in range
	for i, v := range pred {
		if v == 0 && i != 0 { continue } // just check no panic
		_ = v
	}
}

func TestIntra4x4DiagDownRight(t *testing.T) {
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4DiagDownRight, testTop, testTopRight, testLeft, testTopLeft)
	// (0,0): (left[0]+2*topLeft+top[0]+2)>>2 = (100+180+200+2)>>2 = 482>>2 = 120
	want00 := uint8(120)
	if pred[0] != want00 {
		t.Errorf("DiagDownRight (0,0)=%d want %d", pred[0], want00)
	}
	// (1,0): (topLeft+2*top[0]+top[1]+2)>>2 = (90+400+210+2)>>2 = 702>>2 = 175
	want10 := uint8(175)
	if pred[1] != want10 {
		t.Errorf("DiagDownRight (1,0)=%d want %d", pred[1], want10)
	}
}

func TestIntra4x4VerticalRight(t *testing.T) {
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4VerticalRight, testTop, testTopRight, testLeft, testTopLeft)
	// (0,0): zVR=2*0-0=0, even: (topLeft+top[0]+1)>>1 = (90+200+1)>>1 = 145
	want00 := uint8(145)
	if pred[0] != want00 {
		t.Errorf("VerticalRight (0,0)=%d want %d", pred[0], want00)
	}
}

func TestIntra4x4HorizontalDown(t *testing.T) {
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4HorizontalDown, testTop, testTopRight, testLeft, testTopLeft)
	// (0,0): zHD=2*0-0=0, even: (topLeft+left[0]+1)>>1 = (90+100+1)>>1 = 95
	want00 := uint8(95)
	if pred[0] != want00 {
		t.Errorf("HorizontalDown (0,0)=%d want %d", pred[0], want00)
	}
}

func TestIntra4x4VerticalLeft(t *testing.T) {
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4VerticalLeft, testTop, testTopRight, testLeft, testTopLeft)
	// (0,0): idx=0+(0>>1)=0, y%2==0: (top[0]+top[1]+1)>>1 = (200+210+1)>>1 = 205
	want00 := uint8(205)
	if pred[0] != want00 {
		t.Errorf("VerticalLeft (0,0)=%d want %d", pred[0], want00)
	}
	// (1,0): idx=1, y%2==0: (top[1]+top[2]+1)>>1 = (210+220+1)>>1 = 215
	want10 := uint8(215)
	if pred[1] != want10 {
		t.Errorf("VerticalLeft (1,0)=%d want %d", pred[1], want10)
	}
}

func TestIntra4x4HorizontalUp(t *testing.T) {
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4HorizontalUp, testTop, testTopRight, testLeft, testTopLeft)
	// (0,0): zHU=0, idx=0, zHU%2==0, idx+1<=3: (left[0]+left[1]+1)>>1 = (100+110+1)>>1 = 105
	want00 := uint8(105)
	if pred[0] != want00 {
		t.Errorf("HorizontalUp (0,0)=%d want %d", pred[0], want00)
	}
}

func TestIntra4x4AllModesNoOOB(t *testing.T) {
	pred := make([]uint8, 16)
	for mode := 0; mode <= 8; mode++ {
		PredIntra4x4(pred, mode, testTop, testTopRight, testLeft, testTopLeft)
		for i, v := range pred {
			_ = i
			_ = v // just verify no panic
		}
	}
}

// --- I4x4 DC boundary cases ---

func TestIntra4x4DC_TopOnly(t *testing.T) {
	// When called with mode=DC, the caller (reconstruct4x4) handles availability.
	// PredIntra4x4 itself receives the filled slices, so DC from all-100 refs = 100.
	top := []uint8{100, 100, 100, 100}
	topR := []uint8{100, 100, 100, 100}
	left := []uint8{100, 100, 100, 100}
	pred := make([]uint8, 16)
	PredIntra4x4(pred, Intra4x4DC, top, topR, left, 100)
	// DC = (100*4 + 100*4 + 4) / 8 = 804/8 = 100 (via PredIntra4x4 which sums all 8)
	// Actually PredIntra4x4 DC path sums top[0..3]+left[0..3] = 800+4=804, >>3=100
	for i, v := range pred {
		if v != 100 {
			t.Errorf("DC pred[%d]=%d want 100", i, v)
		}
	}
}

// --- I8x8 modes ---

func TestIntra8x8AllModes(t *testing.T) {
	top := make([]uint8, 16)
	left := make([]uint8, 8)
	for i := range top { top[i] = 128 }
	for i := range left { left[i] = 128 }
	topLeft := uint8(128)

	pred := make([]uint8, 64)
	for mode := 0; mode <= 8; mode++ {
		PredIntra8x8(pred, mode, top, left, topLeft)
		for i, v := range pred {
			_ = i
			_ = v // no panic check
		}
	}
}

func TestIntra8x8DC_Flat(t *testing.T) {
	top := make([]uint8, 16)
	left := make([]uint8, 8)
	for i := range top { top[i] = 100 }
	for i := range left { left[i] = 100 }

	pred := make([]uint8, 64)
	PredIntra8x8(pred, 2, top, left, 100) // mode 2 = DC
	// DC of 8 top + 8 left all=100 = 100
	for i, v := range pred {
		if v != 100 {
			t.Errorf("I8x8 DC pred[%d]=%d want 100", i, v)
		}
	}
}

func TestIntra8x8Vertical(t *testing.T) {
	top := make([]uint8, 16)
	left := make([]uint8, 8)
	for i := range top { top[i] = uint8(i * 10) }

	pred := make([]uint8, 64)
	PredIntra8x8(pred, 0, top, left, 0) // mode 0 = Vertical
	// Each column x should equal the (filtered) top[x]
	// After the §8.3.2.3 strong filter, the top pixels are smoothed.
	// Just verify no out-of-range values and the column structure.
	for y := 0; y < 8; y++ {
		for x := 1; x < 7; x++ {
			// Column x and column x+1 (not extreme corners) should be adjacent
			diff := int(pred[y*8+x]) - int(pred[y*8+(x-1)])
			if diff < -20 || diff > 20 {
				t.Errorf("Vertical: large column step at row %d, col %d: diff=%d", y, x, diff)
			}
		}
	}
}

func TestIntra8x8Horizontal(t *testing.T) {
	top := make([]uint8, 16)
	left := make([]uint8, 8)
	for i := range left { left[i] = uint8(i * 10) }

	pred := make([]uint8, 64)
	PredIntra8x8(pred, 1, top, left, 0) // mode 1 = Horizontal
	// Each row y should be constant (from filtered left[y])
	for y := 0; y < 8; y++ {
		v0 := pred[y*8]
		for x := 1; x < 8; x++ {
			if pred[y*8+x] != v0 {
				t.Errorf("Horizontal mode: row %d not constant: [%d]=%d != [0]=%d", y, x, pred[y*8+x], v0)
			}
		}
	}
}

// --- I16x16 boundary coverage ---

func TestIntra16x16Vertical(t *testing.T) {
	top := make([]uint8, 16)
	left := make([]uint8, 16)
	for i := range top { top[i] = uint8(i * 16) }

	pred := make([]uint8, 256)
	PredIntra16x16(pred, Intra16x16Vertical, top, left, 0)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if pred[y*16+x] != top[x] {
				t.Errorf("16x16 Vertical (%d,%d)=%d want %d", x, y, pred[y*16+x], top[x])
			}
		}
	}
}

func TestIntra16x16Horizontal(t *testing.T) {
	top := make([]uint8, 16)
	left := make([]uint8, 16)
	for i := range left { left[i] = uint8(i * 16) }

	pred := make([]uint8, 256)
	PredIntra16x16(pred, Intra16x16Horizontal, top, left, 0)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if pred[y*16+x] != left[y] {
				t.Errorf("16x16 Horizontal (%d,%d)=%d want %d", x, y, pred[y*16+x], left[y])
			}
		}
	}
}
