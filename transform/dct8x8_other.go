//go:build !amd64 && !arm64

package transform

import "unsafe"

func IDCT8x8_ASM(block *int16) {
	if block == nil {
		return
	}
	IDCT8x8(unsafe.Slice(block, 64))
}
func DCT8x8_ASM(block *int16) {
	if block == nil {
		return
	}
	DCT8x8(unsafe.Slice(block, 64))
}
