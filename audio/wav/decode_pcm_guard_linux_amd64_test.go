//go:build linux && amd64 && !purego

package wav

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestDecodePCMGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(bytes int, end bool) ([]byte, func()) {
		pages := (bytes + page - 1) / page
		mem, err := syscall.Mmap(-1, 0, (pages+2)*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err = syscall.Mprotect(mem[page:(pages+1)*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			_ = syscall.Munmap(mem)
			t.Fatal(err)
		}
		start := page
		if end {
			start = (pages+1)*page - bytes
		}
		return mem[start : start+bytes], func() { _ = syscall.Munmap(mem) }
	}
	for _, bits := range []int{8, 16, 32} {
		for _, n := range []int{1, 2, 3, 4, 5, 7, 127, 128, 4096} {
			for _, end := range []bool{false, true} {
				t.Run(fmt.Sprintf("bits%d/n%d/end%t", bits, n, end), func(t *testing.T) {
					b := bits / 8
					raw, free := guarded(n*b, end)
					defer free()
					for i := range raw {
						raw[i] = byte(i*37 + 11)
					}
					draw, dfree := guarded(n*8, end)
					defer dfree()
					dst := unsafe.Slice((*float64)(unsafe.Pointer(&draw[0])), n)
					want := make([]float64, n)
					switch bits {
					case 8:
						decodePCM8(dst, raw)
						decodePCM8Scalar(want, raw)
					case 16:
						decodePCM16(dst, raw)
						decodePCM16Scalar(want, raw)
					case 32:
						decodePCM32(dst, raw)
						decodePCM32Scalar(want, raw)
					}
					checkDecodeBits(t, dst, want)
				})
			}
		}
	}
}
