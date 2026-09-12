//go:build linux && arm64

package me

import (
	"syscall"
	"testing"
)

func TestSAD16x16NEONGuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	alloc := func() ([]byte, []byte) {
		m, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err = syscall.Mprotect(m[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Munmap(m) })
		return m, m[page : 2*page]
	}
	_, ap := alloc()
	_, bp := alloc()
	for _, stride := range []int{16, 17, 31, 64} {
		n := 15*stride + 16
		for _, end := range []bool{false, true} {
			off := 0
			if end {
				off = page - n
			}
			a, b := ap[off:off+n:off+n], bp[off:off+n:off+n]
			for i := range a {
				a[i], b[i] = byte(i*77+13), byte(i*29+211)
			}
			if got, want := SAD16x16(a, b, stride, stride), sad16Scalar(a, b, stride, stride); got != want {
				t.Fatal(stride, end, got, want)
			}
		}
	}
}
