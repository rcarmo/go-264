//go:build linux && amd64 && !purego

package filter

import (
	"bytes"
	"syscall"
	"testing"
)

func TestVerticalDeblockSSE2GuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	guarded := func() []byte {
		mem, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mprotect(mem[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Munmap(mem) })
		return mem[page : 2*page]
	}
	for _, atEnd := range []bool{false, true} {
		for _, bs := range [][4]int{{1, 2, 3, 1}, {4, 4, 4, 4}, {0, 2, 0, 3}} {
			const stride = 16
			const rows = 16
			n := stride * rows
			off := 0
			if atEnd {
				off = page - n
			}
			gotPage := guarded()
			got := gotPage[off : off+n : off+n]
			for i := range got {
				got[i] = byte(i*73 + 19)
			}
			want := append([]byte(nil), got...)
			FilterLumaEdgeV(got, stride, 4, 0, 16, bs, 31, 31)
			filterLumaEdgeVReference(want, stride, 4, 0, 16, bs, 31, 31)
			if !bytes.Equal(got, want) {
				t.Fatalf("luma end=%t bs=%v", atEnd, bs)
			}
			gotPage = guarded()
			got = gotPage[off : off+n : off+n]
			for i := range got {
				got[i] = byte(i*29 + 101)
			}
			want = append([]byte(nil), got...)
			FilterChromaEdgeV(got, stride, 2, 0, 8, bs, 31, 31)
			filterChromaEdgeVReference(want, stride, 2, 0, 8, bs, 31, 31)
			if !bytes.Equal(got, want) {
				t.Fatalf("chroma end=%t bs=%v", atEnd, bs)
			}
		}
	}
}
