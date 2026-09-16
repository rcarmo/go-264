package transform

import "testing"

func TestDequant8x8Rounding(t *testing.T) {
	// QP 8, position 2: Table 8-15 factor 33, shift 1.
	for _, tc := range []struct{ level, want int16 }{{1, 17}, {-1, -16}, {3, 50}, {-3, -49}} {
		var b [64]int16
		b[2] = tc.level
		Dequant8x8(b[:], 8)
		if b[2] != tc.want {
			t.Errorf("level %d: got %d, want %d", tc.level, b[2], tc.want)
		}
	}
}
