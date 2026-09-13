//go:build linux && (amd64 || arm64) && !purego

package ac3

import (
	"fmt"
	"os"
	"reflect"
	"syscall"
	"testing"
	"unsafe"
)

func TestOverlapAddGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(n int, end bool) ([]float64, func()) {
		pages := (n*8 + page - 1) / page
		memory, err := syscall.Mmap(-1, 0, (pages+2)*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mprotect(memory[page:(pages+1)*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			_ = syscall.Munmap(memory)
			t.Fatal(err)
		}
		start := page
		if end {
			start = (pages+1)*page - n*8
		}
		return unsafe.Slice((*float64)(unsafe.Pointer(&memory[start])), n), func() { _ = syscall.Munmap(memory) }
	}
	for _, n := range []int{1, 2, 3, 7, 127, 128, 255, 256, 257, 513} {
		for _, end := range []bool{false, true} {
			t.Run(fmt.Sprintf("n%d/end%t", n, end), func(t *testing.T) {
				current, freeCurrent := guarded(n, end)
				defer freeCurrent()
				previous, freePrevious := guarded(n, end)
				defer freePrevious()
				output, freeOutput := guarded(n, end)
				defer freeOutput()
				for i := range current {
					current[i], previous[i] = float64(i%17-8)/31, float64(i%11-5)/29
				}
				want := make([]float64, n)
				overlapAddScalar(want, current, previous)
				overlapAdd(output, current, previous)
				if !reflect.DeepEqual(output, want) {
					t.Fatal("output differs")
				}
			})
		}
	}
}
