//go:build amd64 && !purego

package me

func sadSmall(a, b []byte, strideA, strideB, size int) int {
	if smallSADSafe(a, b, strideA, strideB, size) {
		return sadSmallSSE2(&a[0], &b[0], strideA, strideB, size)
	}
	return sadSmallScalar(a, b, strideA, strideB, size)
}

//go:noescape
func sadSmallSSE2(a, b *byte, strideA, strideB, size int) int
