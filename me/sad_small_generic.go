//go:build !amd64 || purego

package me

func sadSmall(a, b []byte, strideA, strideB, size int) int {
	return sadSmallScalar(a, b, strideA, strideB, size)
}
