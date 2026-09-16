//go:build linux && (amd64 || arm64) && !purego

package decode

import (
	"bytes"
	"syscall"
	"testing"
)

func TestBiBlendSIMDGuardPages(t *testing.T) {
	page := syscall.Getpagesize()
	guarded := func() []byte {
		mem, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mprotect(mem[page:2*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			_ = syscall.Munmap(mem)
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Munmap(mem) })
		return mem[page : 2*page]
	}
	for _, shape := range []struct{ w, h int }{{4, 4}, {8, 4}, {8, 8}, {16, 8}, {8, 16}, {16, 16}} {
		for _, stride := range []int{shape.w, 16, 23} {
			if stride < shape.w {
				continue
			}
			n := (shape.h-1)*stride + shape.w
			for _, atEnd := range []bool{false, true} {
				for _, p := range []struct{ w0, w1, round, shift, offset int }{
					{32, 32, 32, 6, 0}, {-128, 127, 1, 1, -128}, {127, -128, 128, 8, 127},
				} {
					aPage, bPage, dstPage := guarded(), guarded(), guarded()
					off := 0
					if atEnd {
						off = page - n
					}
					a, b, got := aPage[off:off+n:off+n], bPage[off:off+n:off+n], dstPage[off:off+n:off+n]
					for i := range a {
						a[i], b[i], got[i] = byte(i*73+11), byte(i*29+197), 0xa5
					}
					want := bytes.Repeat([]byte{0xa5}, n)
					plain := p.w0 == 32 && p.w1 == 32 && p.round == 32 && p.shift == 6 && p.offset == 0
					blendReferenceParams(want, a, b, stride, shape.w, shape.h, p.w0, p.w1, p.round, p.shift, p.offset, plain)
					biBlendRectParams(got, a, b, stride, shape.w, shape.h, p.w0, p.w1, p.round, p.shift, p.offset)
					if !bytes.Equal(got, want) {
						t.Fatalf("shape=%+v stride=%d end=%t params=%+v", shape, stride, atEnd, p)
					}
				}
			}
		}
	}
}
