//go:build arm64

package me

// SAD16x16_ASM computes SAD using NEON UABD + UADALP.
//
//go:noescape
func SAD16x16_ASM_NEON(a, b *uint8, strideA, strideB int) uint32

// override: use NEON path instead
var hasNEON = true

func SAD16x16_ASM(a, b *uint8, strideA, strideB int) uint32 {
	return SAD16x16_ASM_NEON(a, b, strideA, strideB)
}

var hasSSE2 = false
