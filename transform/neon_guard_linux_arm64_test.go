//go:build linux && arm64

package transform

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestNEONDCT4GuardPages(t *testing.T) {
	page := os.Getpagesize()
	for _, end := range []bool{false, true} {
		for off := 0; off < 8; off++ {
			mem, e := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
			if e != nil {
				t.Fatal(e)
			}
			if e = syscall.Mprotect(mem[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); e != nil {
				t.Fatal(e)
			}
			start := page + off*2
			if end {
				start = 2*page - 32 - off*2
			}
			block := unsafe.Slice((*int16)(unsafe.Pointer(&mem[start])), 16)
			for i := range block {
				block[i] = int16(i*6001 - 32000)
			}
			want := append([]int16(nil), block...)
			DCT4x4Scalar(want)
			DCT4x4_NEON(&block[0])
			for i := range want {
				if block[i] != want[i] {
					t.Fatal(end, off, i, block[i], want[i])
				}
			}
			_ = syscall.Munmap(mem)
		}
	}
}
