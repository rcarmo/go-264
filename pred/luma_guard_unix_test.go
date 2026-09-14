//go:build (linux || darwin) && (amd64 || arm64) && !purego

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

// The border cases above use edge-emulation scratch. This matrix also puts
// the raw interpolation footprint against a guard page, so ARM64's direct loads
// must respect each width and each directional six-tap halo.
func TestLumaInteriorGuardPages(t *testing.T) {
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
	srcPage, outPage := alloc(), alloc()
	for _, w := range []int{4, 8, 16} {
		for _, h := range []int{1, 16} {
			for _, atEnd := range []bool{false, true} {
				for fx := 0; fx < 4; fx++ {
					for fy := 0; fy < 4; fy++ {
						left, top, stride, rows := 0, 0, w, h
						if fx != 0 {
							left, stride = 2, w+5
						}
						if fy != 0 {
							top, rows = 2, h+5
						}
						outStride := w + 3
						srcN, outN := stride*rows, (h-1)*outStride+w
						so, oo := 0, 0
						if atEnd {
							so, oo = page-srcN, page-outN
						}
						src, out := srcPage[so:so+srcN:so+srcN], outPage[oo:oo+outN:oo+outN]
						for i := range src {
							src[i] = byte(i*77 + 13)
						}
						for i := range out {
							out[i] = 0xa5
						}
						want := append([]byte(nil), out...)
						mv := MotionVector{int16(fx), int16(fy)}
						interPredLumaH264Scalar(want, outStride, src, stride, left, top, w, h, mv)
						InterPredLumaH264(out, outStride, src, stride, left, top, w, h, mv)
						if !bytes.Equal(out, want) {
							t.Fatalf("size=%dx%d end=%t fraction=%v: pixels or padding differ", w, h, atEnd, mv)
						}
					}
				}
			}
		}
	}
}
