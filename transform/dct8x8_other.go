//go:build !amd64

package transform

func IDCT8x8_ASM(block *int16) { panic("no asm") }
func DCT8x8_ASM(block *int16)  { panic("no asm") }
