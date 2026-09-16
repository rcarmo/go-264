//go:build linux && amd64 && !purego

package transform

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestIDCT8GuardPages(t *testing.T) {
	page := os.Getpagesize()
	for _, end := range []bool{false, true} {
		for off := 0; off < 8; off++ {
			mem, e := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
			if e != nil {
				t.Fatal(e)
			}
			if e = syscall.Mprotect(mem[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); e != nil {
				syscall.Munmap(mem)
				t.Fatal(e)
			}
			start := page + off*2
			if end {
				start = 2*page - 128 - off*2
			}
			block := unsafe.Slice((*int16)(unsafe.Pointer(&mem[start])), 64)
			for i := range block {
				block[i] = int16(i*991 - 30000)
			}
			var want [64]int16
			copy(want[:], block)
			IDCT8x8_ASM(&want[0])
			idct8Packed(block)
			for i := range want {
				if block[i] != want[i] {
					t.Fatal(end, off, i)
				}
			}
			syscall.Munmap(mem)
		}
	}
}
