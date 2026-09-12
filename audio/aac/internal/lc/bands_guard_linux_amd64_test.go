//go:build linux && amd64 && !purego

package lc

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestBandKernelGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(n int, end bool) ([]float64, func()) {
		pages := (n*8 + page - 1) / page
		mem, e := syscall.Mmap(-1, 0, (pages+2)*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if e != nil {
			t.Fatal(e)
		}
		if e = syscall.Mprotect(mem[page:(pages+1)*page], syscall.PROT_READ|syscall.PROT_WRITE); e != nil {
			syscall.Munmap(mem)
			t.Fatal(e)
		}
		start := page
		if end {
			start = (pages+1)*page - n*8
		}
		return unsafe.Slice((*float64)(unsafe.Pointer(&mem[start])), n), func() { syscall.Munmap(mem) }
	}
	for _, n := range []int{1, 2, 3, 7, 128, 1024} {
		for _, end := range []bool{false, true} {
			t.Run(fmt.Sprintf("n%d/end%t", n, end), func(t *testing.T) {
				x, xf := guarded(n, end)
				defer xf()
				y, yf := guarded(n, end)
				defer yf()
				for i := range x {
					x[i] = float64(i%17-8) / 16
					y[i] = float64(i%11-5) / 8
				}
				wx, wy := append([]float64(nil), x...), append([]float64(nil), y...)
				midSide(x, y)
				midSideScalar(wx, wy)
				sameBand(t, x, wx)
				sameBand(t, y, wy)
				scaleBand(y, x, -0.5)
				scaleBandScalar(wy, wx, -0.5)
				sameBand(t, y, wy)
				for order := 1; order <= 12; order++ {
					a, af := guarded(order, end)
					for i := range a {
						a[i] = float64(i%3-1) / 32
					}
					for _, reverse := range []bool{false, true} {
						copy(x, wx)
						want := append([]float64(nil), wx...)
						tnsBand(x, a, reverse)
						tnsBandScalar(want, a, reverse)
						sameBand(t, x, want)
					}
					af()
				}
			})
		}
	}
}
