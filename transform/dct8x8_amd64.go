//go:build amd64

package transform

import "unsafe"

//go:noescape
func IDCT8x8_ASM(block *int16)

//go:noescape
func dct8x8ASMDisabled(block *int16)

func DCT8x8_ASM(block *int16) {
	if block == nil {
		return
	}
	DCT8x8(unsafe.Slice(block, 64))
}
