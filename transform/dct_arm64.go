//go:build arm64

package transform

import "unsafe"

// ARM64 NEON implementations of DCT/IDCT.
// The 4×4 butterfly uses VADD/VSUB/VSHR (int16 vector ops).

//go:noescape
func IDCT4x4_NEON(block *int16)

//go:noescape
func DCT4x4_NEON(block *int16)

//go:noescape
func IDCT8x8_NEON(block *int16)

//go:noescape
func DCT8x8_NEON(block *int16)

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
func IDCT8x8_ASM(block *int16) { IDCT8x8_NEON(block) } // delegate to NEON
func DCT8x8_ASM(block *int16)  { DCT8x8_NEON(block) }
