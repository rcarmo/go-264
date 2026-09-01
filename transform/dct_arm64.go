//go:build arm64

package transform

import "unsafe"

// The historically named NEON 4×4 entry points use scalar ARM64 registers.
// Butterfly intermediates are wide; pass outputs are stored as int16.

//go:noescape
func IDCT4x4_NEON(block *int16)

//go:noescape
func DCT4x4_NEON(block *int16)

func init() {
	// Override HasAVX2 to false on arm64, use NEON dispatch
}

var HasNEON = true
var HasAVX2 = false

func IDCT4x4_AVX2(block *int16) {
	if block == nil {
		return
	}
	IDCT4x4Scalar(unsafe.Slice(block, 16))
}
func DCT4x4_AVX2(block *int16) {
	if block == nil {
		return
	}
	DCT4x4Scalar(unsafe.Slice(block, 16))
}
func cpuidHasAVX2() bool       { return false }
func IDCT8x8_ASM(block *int16) { IDCT8x8_NEON(block) }
func DCT8x8_ASM(block *int16)  { DCT8x8_NEON(block) }

// The 8x8 NEON kernels are not implemented. Keep their entry points safe for
// both dispatched and direct callers without disabling the working 4x4 kernels.
func IDCT8x8_NEON(block *int16) {
	if block != nil {
		IDCT8x8Scalar(unsafe.Slice(block, 64))
	}
}

func DCT8x8_NEON(block *int16) {
	if block != nil {
		DCT8x8(unsafe.Slice(block, 64))
	}
}
