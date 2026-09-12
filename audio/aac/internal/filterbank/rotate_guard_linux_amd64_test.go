//go:build linux && amd64 && !purego

package filterbank

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestRotationGuardPages(t *testing.T) {
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
	for _, n := range []int{1, 2, 3, 127, 128, 1024} {
		for _, end := range []bool{false, true} {
			for _, offset := range []int{0, 8} {
				t.Run(fmt.Sprintf("n%d/end%t/offset%d", n, end, offset), func(t *testing.T) {
					c, cf := guarded(n*8+offset, end)
					defer cf()
					x, xf := guarded(n*16+offset, end)
					defer xf()
					w, wf := guarded(n*16+offset, end)
					defer wf()
					d, df := guarded(n*8+offset, end)
					defer df()
					// At the trailing boundary, offset=0 puts the last value
					// against the guard; offset=8 tests eight-byte alignment.
					coeff := unsafe.Slice((*float64)(unsafe.Pointer(&c[0])), n)
					src := unsafe.Slice((*complex128)(unsafe.Pointer(&x[0])), n)
					weights := unsafe.Slice((*complex128)(unsafe.Pointer(&w[0])), n)
					dst := unsafe.Slice((*float64)(unsafe.Pointer(&d[0])), n)
					for i := range coeff {
						coeff[i], weights[i] = float64(i%7)-3, complex(float64(i%9)/11, -float64(i%5)/7)
					}
					want := make([]complex128, n)
					rotateInputScalar(want, coeff, weights)
					rotateInput(src, coeff, weights)
					for i := range want {
						if !sameComplexBits(src[i], want[i]) {
							t.Fatal("input rotation", i)
						}
					}
					wantReal := make([]float64, n)
					rotateOutputScalar(wantReal, src, weights, 1.0/1024)
					rotateOutput(dst, src, weights, 1.0/1024)
					checkFloatBits(t, dst, wantReal)
				})
			}
		}
	}
}
