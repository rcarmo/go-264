//go:build linux && arm64 && !purego

package filterbank

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestWindowAndOverlapGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(n int, end bool) ([]float64, func()) {
		pages := (n*8 + page - 1) / page
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
			start = (pages+1)*page - n*8
		}
		return unsafe.Slice((*float64)(unsafe.Pointer(&mem[start])), n), func() { _ = syscall.Munmap(mem) }
	}
	for _, n := range []int{1, 2, 3, 7, 127, 128, 129, 1024} {
		for _, end := range []bool{false, true} {
			t.Run(fmt.Sprintf("n%d/end%t", n, end), func(t *testing.T) {
				dst, freeDst := guarded(n, end)
				defer freeDst()
				src, freeSrc := guarded(n, end)
				defer freeSrc()
				weights, freeWeights := guarded(n, end)
				defer freeWeights()
				for i := range src {
					src[i], weights[i] = float64(i%9)-3, float64(i%7)/11
				}
				for _, reverse := range []bool{false, true} {
					for _, add := range []bool{false, true} {
						want := append([]float64(nil), dst...)
						windowScalar(want, src, weights, reverse, add)
						applyWindow(dst, src, weights, reverse, add)
						checkFloatBits(t, dst, want)
					}
				}
				want := make([]float64, n)
				overlapScalar(want, src, weights)
				addOverlap(dst, src, weights)
				checkFloatBits(t, dst, want)
			})
		}
	}
}
