//go:build linux && amd64 && !purego

package decode

import (
	"bytes"
	"syscall"
	"testing"

	"github.com/rcarmo/go-264/syntax"
)

func TestChromaInterSSE2GuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	alloc := func() []byte {
		m, e := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if e != nil {
			t.Fatal(e)
		}
		if e = syscall.Mprotect(m[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = syscall.Munmap(m) })
		return m[page : 2*page]
	}
	srcPage, dstPage := alloc(), alloc()
	var d Decoder
	for _, stride := range []int{9, 10, 17, 31} {
		height := 9
		n := (height-1)*stride + 9
		for _, end := range []bool{false, true} {
			so, do := 0, 0
			if end {
				so, do = page-n, page-64
			}
			src, dst := srcPage[so:so+n:so+n], dstPage[do:do+64:do+64]
			for i := range src {
				src[i] = byte(i*73 + 11)
			}
			for fx := 1; fx < 8; fx++ {
				for fy := 1; fy < 8; fy++ {
					want := make([]byte, 64)
					fillChromaInterPredScalar(want, src, stride, 9, height, 0, 0, syntax.MotionVector{X: int16(fx), Y: int16(fy)})
					d.fillChromaInterPred(dst, src, stride, 9, height, 0, 0, syntax.MotionVector{X: int16(fx), Y: int16(fy)})
					if !bytes.Equal(dst, want) {
						t.Fatal(stride, end, fx, fy)
					}
				}
			}
		}
	}
}
