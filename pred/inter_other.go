//go:build !amd64 && !arm64

package pred

import "unsafe"

func InterPred16x16Copy_ASM(dst *uint8, src *uint8, dstStride, srcStride int) {
	if dst == nil || src == nil || dstStride < 16 || srcStride < 16 {
		return
	}
	d := unsafe.Slice(dst, dstStride*16)
	s := unsafe.Slice(src, srcStride*16)
	for y := 0; y < 16; y++ {
		copy(d[y*dstStride:y*dstStride+16], s[y*srcStride:y*srcStride+16])
	}
}
