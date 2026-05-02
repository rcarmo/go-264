//go:build !amd64

package pred

func InterPred16x16Copy_ASM(dst *uint8, src *uint8, dstStride, srcStride int) {
	panic("no SSE2")
}
