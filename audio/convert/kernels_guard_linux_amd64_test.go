//go:build linux && amd64 && !purego

package convert

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestPCMKernelGuardPages(t *testing.T) {
	page := os.Getpagesize()
	guarded := func(bytes int, end bool) ([]byte, func()) {
		pages := (bytes + page - 1) / page
		mem, err := syscall.Mmap(-1, 0, (pages+2)*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			t.Fatal(err)
		}
		if err = syscall.Mprotect(mem[page:(pages+1)*page], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
			syscall.Munmap(mem)
			t.Fatal(err)
		}
		start := page
		if end {
			start = (pages+1)*page - bytes
		}
		return mem[start : start+bytes], func() { syscall.Munmap(mem) }
	}
	for _, n := range []int{1, 2, 3, 7, 127, 128, 2048} {
		for _, end := range []bool{false, true} {
			t.Run(fmt.Sprintf("n%d/end%t", n, end), func(t *testing.T) {
				raw, free := guarded(n*16, end)
				defer free()
				src := unsafe.Slice((*float64)(unsafe.Pointer(&raw[0])), n*2)
				draw, dfree := guarded(n*16, end)
				defer dfree()
				dst := unsafe.Slice((*float64)(unsafe.Pointer(&draw[0])), n*2)
				praw, pfree := guarded(n*2, end)
				defer pfree()
				pcm := unsafe.Slice((*int16)(unsafe.Pointer(&praw[0])), n)
				for i := range src {
					src[i] = float64(i%7-3) / 8
				}
				sraw, sfree := guarded(n*8, end)
				defer sfree()
				s16src := unsafe.Slice((*float64)(unsafe.Pointer(&sraw[0])), n)
				copy(s16src, src[:n])
				want := make([]int16, n)
				s16Scalar(want, s16src)
				s16Kernel(pcm, s16src)
				for i := range want {
					if pcm[i] != want[i] {
						t.Fatal("s16", i)
					}
				}
				ref := make([]float64, n*2)
				monoToStereoScalar(ref, src[:n])
				monoToStereo(dst, src[:n])
				checkPCMFloatBits(t, dst, ref)
				interleaveStereoScalar(ref, src[:n], src[n:])
				interleaveStereo(dst, src[:n], src[n:])
				checkPCMFloatBits(t, dst, ref)
				// A separate exact-length destination catches vector tail overstores.
				mraw, mfree := guarded(n*8, end)
				defer mfree()
				mono := unsafe.Slice((*float64)(unsafe.Pointer(&mraw[0])), n)
				stereoToMonoScalar(ref[:n], src)
				stereoToMono(mono, src)
				checkPCMFloatBits(t, mono, ref[:n])
			})
		}
	}
}
