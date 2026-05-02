//go:build amd64

package transform

//go:noescape
func IDCT8x8_ASM(block *int16)

//go:noescape
func DCT8x8_ASM(block *int16)
