//go:build amd64 && !purego

package decode

import (
	"bytes"
	"testing"
)

// Ensure Intel keeps the same 2/4/8 partition coverage as the NEON dispatcher;
// end-to-end oracle and guard-page tests verify the arithmetic and footprints.
func TestChromaInterSSE2DispatchCoverage(t *testing.T) {
	for _, w := range []int{2, 4, 8} {
		for _, h := range []int{1, 4, 8} {
			src := make([]byte, h*(w+1)+w+1)
			for i := range src {
				src[i] = byte(i*73 + 11)
			}
			dst := bytes.Repeat([]byte{0xa5}, (h-1)*8+w)
			want := bytes.Clone(dst)
			wa, wb, wc, wd := 15, 5, 33, 11
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					want[y*8+x] = byte((wa*int(src[y*(w+1)+x]) + wb*int(src[y*(w+1)+x+1]) + wc*int(src[(y+1)*(w+1)+x]) + wd*int(src[(y+1)*(w+1)+x+1]) + 32) >> 6)
				}
			}
			if !chromaInterSIMD(dst, src, w+1, w, h, wa, wb, wc, wd) {
				t.Fatalf("SSE2 rejected supported %dx%d partition", w, h)
			}
			if !bytes.Equal(dst, want) {
				t.Fatalf("%dx%d pixels or padding differ: got=%v want=%v", w, h, dst, want)
			}
		}
	}
	if chromaInterSIMD(make([]byte, 64), make([]byte, 81), 9, 3, 8, 16, 16, 16, 16) {
		t.Fatal("SSE2 accepted unsupported width")
	}
}
