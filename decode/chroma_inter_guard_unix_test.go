//go:build (linux || darwin) && (amd64 || arm64) && !purego

package decode

import (
	"bytes"
	"syscall"
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func TestChromaInterGuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	alloc := func() []byte {
		m, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Munmap(m) })
		if err := syscall.Mprotect(m[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			t.Fatal(err)
		}
		return m[page : 2*page]
	}
	srcPage, dstPage := alloc(), alloc()
	for _, w := range []int{2, 4, 8} {
		for _, h := range []int{1, 4, 8} {
			for _, stride := range []int{w + 1, w + 9} {
				// Only the bilinear halo is readable. The destination has stride8,
				// with no padding after the final narrow row.
				srcN, dstN := h*stride+w+1, (h-1)*8+w
				for _, atEnd := range []bool{false, true} {
					so, do := 0, 0
					if atEnd {
						so, do = page-srcN, page-dstN
					}
					src, dst := srcPage[so:so+srcN:so+srcN], dstPage[do:do+dstN:do+dstN]
					for i := range src {
						src[i] = byte(i*73 + 11)
					}
					for fx := 0; fx < 8; fx++ {
						for fy := 0; fy < 8; fy++ {
							mv := syntax.MotionVector{X: int16(fx), Y: int16(fy)}
							var prediction [64]byte
							fillChromaInterPredReference(prediction[:], src, stride, w+1, h+1, 0, 0, mv)
							for i := range dst {
								dst[i] = 0xa5
							}
							want := append([]byte(nil), dst...)
							for y := 0; y < h; y++ {
								copy(want[y*8:y*8+w], prediction[y*8:y*8+w])
							}
							if !fillChromaInterPredBlock(dst, src, stride, w+1, h+1, 0, 0, w, h, mv) || !bytes.Equal(dst, want) {
								t.Fatalf("size=%dx%d stride=%d end=%t fraction=%v: pixels or padding differ", w, h, stride, atEnd, mv)
							}
						}
					}
				}
			}
		}
	}
}
