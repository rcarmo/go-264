//go:build !amd64 && !arm64

package me

import "unsafe"

var hasSSE2 = false
var hasNEON = false

func SAD16x16_ASM(a, b *uint8, strideA, strideB int) uint32 {
	if a == nil || b == nil || strideA < 16 || strideB < 16 {
		return 0
	}
	aa := unsafe.Slice(a, strideA*16)
	bb := unsafe.Slice(b, strideB*16)
	var sad uint32
	for y := 0; y < 16; y++ {
		rowA := aa[y*strideA : y*strideA+16]
		rowB := bb[y*strideB : y*strideB+16]
		for x := 0; x < 16; x++ {
			d := int(rowA[x]) - int(rowB[x])
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
	}
	return sad
}
