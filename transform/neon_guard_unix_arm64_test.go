//go:build (linux || darwin) && arm64 && !purego

package transform

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// Place the exact kernel footprint against an inaccessible page. Returning
// the middle page also lets read-only inputs reject accidental SIMD stores.
func guardedNEONBytes(t *testing.T, n int, end bool, offset int) ([]byte, []byte) {
	t.Helper()
	pageSize := os.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 3*pageSize, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(mem); err != nil {
			t.Error(err)
		}
	})
	page := mem[pageSize : 2*pageSize]
	if err := syscall.Mprotect(page, syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
		t.Fatal(err)
	}
	start := offset
	if end {
		start = len(page) - n - offset
	}
	return page[start : start+n], page
}

func TestNEONDCT4GuardPages(t *testing.T) {
	for _, end := range []bool{false, true} {
		for off := 0; off < 8; off++ {
			data, _ := guardedNEONBytes(t, 32, end, off*2)
			block := unsafe.Slice((*int16)(unsafe.Pointer(&data[0])), 16)
			for i := range block {
				block[i] = int16(i*6001 - 32000)
			}
			input := append([]int16(nil), block...)
			want := append([]int16(nil), input...)
			DCT4x4Scalar(want)
			DCT4x4_NEON(&block[0])
			for i := range want {
				if block[i] != want[i] {
					t.Fatal("DCT", end, off, i, block[i], want[i])
				}
			}
			copy(block, input)
			copy(want, input)
			IDCT4x4Scalar(want)
			IDCT4x4_NEON(&block[0])
			for i := range want {
				if block[i] != want[i] {
					t.Fatal("IDCT", end, off, i, block[i], want[i])
				}
			}
		}
	}
}

func TestReconstruct4x4GuardPages(t *testing.T) {
	if !Reconstruct4x4Available() {
		t.Skip("fused NEON kernel unavailable")
	}
	for _, end := range []bool{false, true} {
		for _, strides := range [][2]int{{4, 4}, {11, 7}, {19, 13}} {
			for _, qp := range []int{-1, 0, 26, 51} {
				ds, ps := strides[0], strides[1]
				coeffBytes, coeffPage := guardedNEONBytes(t, 32, end, 0)
				coeff := (*[16]int16)(unsafe.Pointer(&coeffBytes[0]))
				prediction, predPage := guardedNEONBytes(t, 3*ps+4, end, 0)
				dst, _ := guardedNEONBytes(t, 3*ds+4, end, 0)
				for i := range coeff {
					coeff[i] = int16(i*6001 - 32000)
				}
				for i := range prediction {
					prediction[i] = byte(i*73 + 19)
				}
				for i := range dst {
					dst[i] = 0xa5
				}
				want := bytes.Clone(dst)
				residual := *coeff
				if qp >= 0 {
					for i := range residual {
						residual[i] *= dequant4Packed[qp][i]
					}
				}
				idct4WideReference(&residual)
				for y := 0; y < 4; y++ {
					for x := 0; x < 4; x++ {
						want[y*ds+x] = byte(max(0, min(255, int(prediction[y*ps+x])+int(residual[y*4+x]))))
					}
				}
				for _, page := range [][]byte{coeffPage, predPage} {
					if err := syscall.Mprotect(page, syscall.PROT_READ); err != nil {
						t.Fatal(err)
					}
				}
				if !Reconstruct4x4(dst, prediction, coeff, ds, ps, qp) {
					t.Skip("NEON disabled")
				}
				if !bytes.Equal(dst, want) {
					t.Fatalf("end=%t dstStride=%d predStride=%d qp=%d: output or padding differs", end, ds, ps, qp)
				}
			}
		}
	}
}
