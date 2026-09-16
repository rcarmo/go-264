//go:build linux && amd64 && !purego

package transform

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestDequantGuardPages(t *testing.T) {
	page := os.Getpagesize()
	for _, size := range []int{16, 64} {
		for _, end := range []bool{false, true} {
			mem, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
			if err != nil {
				t.Fatal(err)
			}
			if err = syscall.Mprotect(mem[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
				syscall.Munmap(mem)
				t.Fatal(err)
			}
			start := page
			if end {
				start = 2*page - size*2
			}
			block := unsafe.Slice((*int16)(unsafe.Pointer(&mem[start])), size)
			for qp := 0; qp <= 51; qp++ {
				for i := range block {
					block[i] = int16(i*971 - 32768)
				}
				want := append([]int16(nil), block...)
				if size == 16 {
					dequant4Scalar(want, qp, 1)
					dequant4Kernel(block, qp, 1)
				} else {
					dequant8Scalar(want, qp)
					dequant8Kernel(block, qp)
				}
				for i := range want {
					if block[i] != want[i] {
						t.Fatal(size, end, qp, i)
					}
				}
			}
			syscall.Munmap(mem)
		}
	}
}
