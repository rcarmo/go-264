//go:build linux && arm64 && !purego

package me

import (
	"syscall"
	"testing"
)

func TestSmallSADNEONGuardPages(t *testing.T) {
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
	aPage, bPage := alloc(), alloc()
	for _, size := range []int{4, 8} {
		for _, stride := range []int{size, size + 1, 31} {
			n := (size-1)*stride + size
			for _, end := range []bool{false, true} {
				off := 0
				if end {
					off = page - n
				}
				a, b := aPage[off:off+n:off+n], bPage[off:off+n:off+n]
				for i := range a {
					a[i], b[i] = byte(i*17+9), byte(i*79+3)
				}
				if got, want := sadSmall(a, b, stride, stride, size), sadSmallScalar(a, b, stride, stride, size); got != want {
					t.Fatal(size, stride, end, got, want)
				}
			}
		}
	}
}
