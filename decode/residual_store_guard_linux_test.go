//go:build linux && (amd64 || arm64) && !purego

package decode

import (
	"bytes"
	"syscall"
	"testing"
	"unsafe"
)

func TestResidualAddStoreSIMDGuardPages(t *testing.T) {
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
	for _, w := range []int{4, 8} {
		for _, stride := range []int{w, 16, 23} {
			h := w
			byteN, resN := (h-1)*stride+w, (h-1)*stride+w
			for _, atEnd := range []bool{false, true} {
				dstPage, predPage, resPage := guarded(), guarded(), guarded()
				boff, roff := 0, 0
				if atEnd {
					boff, roff = page-byteN, page-resN*2
				}
				dst := dstPage[boff : boff+byteN : boff+byteN]
				pred := predPage[boff : boff+byteN : boff+byteN]
				resBytes := resPage[roff : roff+resN*2 : roff+resN*2]
				res := unsafe.Slice((*int16)(unsafe.Pointer(&resBytes[0])), resN)
				for i := range dst {
					dst[i], pred[i] = 0xa5, byte(i*73+19)
				}
				for i := range res {
					res[i] = int16(i*997 - 32768)
				}
				want := bytes.Repeat([]byte{0xa5}, byteN)
				residualAddStoreScalar(want, stride, pred, stride, res, stride, w, h)
				residualAddStore(dst, stride, pred, stride, res, stride, w, h)
				if !bytes.Equal(dst, want) {
					t.Fatalf("w=%d stride=%d end=%t", w, stride, atEnd)
				}
			}
		}
	}
}
