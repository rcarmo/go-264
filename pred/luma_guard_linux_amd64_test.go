//go:build linux && amd64 && !purego

package pred

import (
	"bytes"
	"syscall"
	"testing"
)

func TestLumaGuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	alloc := func() ([]byte, []byte) {
		m, e := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if e != nil {
			t.Fatal(e)
		}
		if e = syscall.Mprotect(m[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { syscall.Munmap(m) })
		return m, m[page : 2*page]
	}
	_, srcPage := alloc()
	_, outPage := alloc()
	for _, w := range []int{1, 4, 7, 8, 15, 16} {
		for _, end := range []bool{false, true} {
			n := w * 16
			so, oo := 0, 0
			if end {
				so, oo = page-n, page-n
			}
			src, out := srcPage[so:so+n:so+n], outPage[oo:oo+n:oo+n]
			for i := range src {
				src[i] = byte(i*77 + 13)
			}
			for fx := 0; fx < 4; fx++ {
				for fy := 0; fy < 4; fy++ {
					mv := MotionVector{int16(fx), int16(fy)}
					want := make([]byte, n)
					interPredLumaH264Scalar(want, w, src, w, -1, 15, w, 16, mv)
					InterPredLumaH264(out, w, src, w, -1, 15, w, 16, mv)
					if !bytes.Equal(out, want) {
						t.Fatal(w, end, mv)
					}
				}
			}
		}
	}
}

func TestLumaFastZeroAlloc(t *testing.T) {
	ref := make([]byte, 48*48)
	var out [256]byte
	for fx := 0; fx < 4; fx++ {
		for fy := 0; fy < 4; fy++ {
			mv := MotionVector{int16(fx), int16(fy)}
			if n := testing.AllocsPerRun(50, func() { InterPredLumaH264(out[:], 16, ref, 48, 8, 8, 16, 16, mv) }); n != 0 {
				t.Fatal(mv, n)
			}
		}
	}
}
