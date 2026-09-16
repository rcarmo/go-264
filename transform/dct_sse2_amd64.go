//go:build amd64 && !purego

package transform

const hasSSE2Transform = true

// IDCT4x4_SSE2 uses four packed int32 lanes with baseline amd64 instructions.
// The full block must be addressable; intermediates narrow between passes.
//
//go:noescape
func IDCT4x4_SSE2(block *int16)

// DCT4x4_SSE2 is the packed forward transform, with int16 pass outputs.
//
//go:noescape
func DCT4x4_SSE2(block *int16)
