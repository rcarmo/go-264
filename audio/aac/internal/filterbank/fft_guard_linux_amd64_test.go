//go:build linux && amd64 && !purego

package filterbank

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// Place the final real/imaginary pair immediately before a protected page.
// Offsetting the start by eight bytes additionally tests legal unaligned loads.
func TestFFTStageGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(n, offset int) ([]complex128, func()) {
		dataPages := (n*16 + offset + page - 1) / page
		mem, err := syscall.Mmap(-1, 0, (dataPages+1)*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mprotect(mem[dataPages*page:], syscall.PROT_NONE); err != nil {
			_ = syscall.Munmap(mem)
			t.Fatal(err)
		}
		p := unsafe.Pointer(&mem[dataPages*page-n*16-offset])
		return unsafe.Slice((*complex128)(p), n), func() { _ = syscall.Munmap(mem) }
	}
	for _, n := range []int{2, 4, 16, shortTransform, longTransform} {
		for _, offset := range []int{0, 8} {
			t.Run(fmt.Sprintf("n%d/offset%d", n, offset), func(t *testing.T) {
				x, freeX := guarded(n, offset)
				defer freeX()
				roots, freeRoots := guarded(n/2, offset)
				defer freeRoots()
				copy(roots, makePlan(n).roots)
				for i := range x {
					x[i] = complex(float64(i%7)/3, -float64(i%5)/7)
				}
				want := append([]complex128(nil), x...)
				for half := 1; half < n; half *= 2 {
					fftStageScalar(want, roots, half, n/(2*half))
					fftStage(x, roots, half, n/(2*half))
				}
				for i := range x {
					if !sameComplexBits(x[i], want[i]) {
						t.Fatalf("sample %d: got %v, want %v", i, x[i], want[i])
					}
				}
			})
		}
	}
}
