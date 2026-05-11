//go:build !amd64 && !arm64

package transform

import "unsafe"

// Stubs for non-amd64 platforms.
var HasAVX2 = false
var HasNEON = false

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

func IDCT4x4_NEON(block *int16) {
	if block == nil {
		return
	}
	IDCT4x4Scalar(unsafe.Slice(block, 16))
}
func DCT4x4_NEON(block *int16) {
	if block == nil {
		return
	}
	DCT4x4Scalar(unsafe.Slice(block, 16))
}
