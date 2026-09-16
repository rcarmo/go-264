//go:build !amd64 || purego

package transform

import "unsafe"

const hasSSE2Transform = false

func IDCT4x4_SSE2(block *int16) {
	if block != nil {
		IDCT4x4Scalar(unsafe.Slice(block, 16))
	}
}
func DCT4x4_SSE2(block *int16) {
	if block != nil {
		DCT4x4Scalar(unsafe.Slice(block, 16))
	}
}
