//go:build linux && amd64 && !purego

package lc

import (
	"fmt"
	"math"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestDequantGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(bytes int, end bool) ([]byte, func()) {
		pages := (bytes + page - 1) / page
		mem, err := syscall.Mmap(-1, 0, (pages+2)*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mprotect(mem[page:(pages+1)*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			_ = syscall.Munmap(mem)
			t.Fatal(err)
		}
		start := page
		if end {
			start = (pages+1)*page - bytes
		}
		return mem[start : start+bytes], func() { _ = syscall.Munmap(mem) }
	}
	for _, n := range []int{1, 2, 3, 7, 127, 128, 1024} {
		for _, end := range []bool{false, true} {
			t.Run(fmt.Sprintf("n%d/end%t", n, end), func(t *testing.T) {
				qraw, freeQ := guarded(n*4, end)
				defer freeQ()
				draw, freeD := guarded(n*8, end)
				defer freeD()
				q := unsafe.Slice((*int32)(unsafe.Pointer(&qraw[0])), n)
				dst := unsafe.Slice((*float64)(unsafe.Pointer(&draw[0])), n)
				for i := range q {
					q[i] = int32((i*97)%16383) - 8191
				}
				if !dequantBand(dst, q, 0.25) {
					t.Fatal("valid band rejected")
				}
				for i := range q {
					if math.Float64bits(dst[i]) != math.Float64bits(directDequant(q[i], 0.25)) {
						t.Fatal("value", i)
					}
				}
			})
		}
	}
}
