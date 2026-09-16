//go:build !amd64 || purego

package transform

func idct8Packed(block []int16) { IDCT8x8Scalar(block) }
