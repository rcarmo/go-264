//go:build linux && arm64 && !purego

package convert

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestARM64LayoutKernelGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(bytes int, end bool) ([]byte, func()) {
		pages := (bytes + page - 1) / page
		m, e := syscall.Mmap(-1, 0, (pages+2)*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if e != nil {
			t.Fatal(e)
		}
		if e = syscall.Mprotect(m[page:(pages+1)*page], syscall.PROT_READ|syscall.PROT_WRITE); e != nil {
			t.Fatal(e)
		}
		start := page
		if end {
			start = (pages+1)*page - bytes
		}
		return m[start : start+bytes], func() { _ = syscall.Munmap(m) }
	}
	for _, n := range []int{1, 2, 3, 7, 127, 128, 2048} {
		for _, end := range []bool{false, true} {
			t.Run(fmt.Sprintf("n%d/end%t", n, end), func(t *testing.T) {
				raw, free := guarded(n*16, end)
				defer free()
				src := unsafe.Slice((*float64)(unsafe.Pointer(&raw[0])), n*2)
				for i := range src {
					src[i] = float64(i%7-3) / 8
				}
				draw, dfree := guarded(n*16, end)
				defer dfree()
				dst := unsafe.Slice((*float64)(unsafe.Pointer(&draw[0])), n*2)
				ref := make([]float64, n*2)
				monoToStereoScalar(ref, src[:n])
				monoToStereo(dst, src[:n])
				checkPCMFloatBits(t, dst, ref)
				interleaveStereoScalar(ref, src[:n], src[n:])
				interleaveStereo(dst, src[:n], src[n:])
				checkPCMFloatBits(t, dst, ref)
			})
		}
	}
}
