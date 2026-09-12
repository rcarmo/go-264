//go:build linux && amd64 && !purego

package resample

import (
	"math"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Place the final scalar immediately before a protected page. A vector load
// past the odd tail faults, so this covers actual assembly memory extents.
func TestDotGuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	for _, n := range []int{1, 2, 3, 15, 145} {
		a, e := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
		if e != nil {
			t.Fatal(e)
		}
		b, e := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
		if e != nil {
			syscall.Munmap(a)
			t.Fatal(e)
		}
		func() {
			defer syscall.Munmap(a)
			defer syscall.Munmap(b)
			if e = syscall.Mprotect(a[page:], syscall.PROT_NONE); e != nil {
				t.Fatal(e)
			}
			if e = syscall.Mprotect(b[page:], syscall.PROT_NONE); e != nil {
				t.Fatal(e)
			}
			aa := unsafe.Slice((*float64)(unsafe.Pointer(&a[page-8*n])), n)
			bb := unsafe.Slice((*float64)(unsafe.Pointer(&b[page-8*n])), n)
			for i := range aa {
				aa[i] = float64(i) + 0.25
				bb[i] = float64(i%3) - 0.5
			}
			got, want := dot(aa, bb), dotScalar(aa, bb)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatal(n, got, want)
			}
			runtime.KeepAlive(a)
			runtime.KeepAlive(b)
		}()
	}
}
