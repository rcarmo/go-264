//go:build amd64

package me

// SAD16x16_ASM computes SAD using PSADBW (SSE2).
// 16 bytes per iteration, 16 rows = 16 PSADBW + horizontal reduction.
//
//go:noescape
func SAD16x16_ASM(a, b *uint8, strideA, strideB int) uint32

var hasSSE2 = true // All amd64 has SSE2
