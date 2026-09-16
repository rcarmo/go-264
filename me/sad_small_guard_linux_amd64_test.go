//go:build linux && amd64 && !purego

package me

import (
	"os"
	"syscall"
	"testing"
)

func TestSmallSADGuardPages(t *testing.T) {
	page := os.Getpagesize()
	for _, size := range []int{4, 8} {
		for _, stride := range []int{size, size + 1, 32} {
			n := (size-1)*stride + size
			for _, end := range []bool{false, true} {
				makeGuard := func() ([]byte, []byte) {
					mem, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
					if err != nil {
						t.Fatal(err)
					}
					if err = syscall.Mprotect(mem[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
						syscall.Munmap(mem)
						t.Fatal(err)
					}
					start := page
					if end {
						start = 2*page - n
					}
					return mem, mem[start : start+n]
				}
				am, a := makeGuard()
				bm, b := makeGuard()
				for i := range a {
					a[i], b[i] = byte(i*31), byte(i*17)
				}
				if got, want := sadSmall(a, b, stride, stride, size), sadSmallScalar(a, b, stride, stride, size); got != want {
					t.Fatal(size, stride, end, got, want)
				}
				syscall.Munmap(am)
				syscall.Munmap(bm)
			}
		}
	}
}
