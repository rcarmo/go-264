//go:build !amd64 && !arm64

package pred

import "testing"

func TestInterPred16x16CopyASMOtherInvalidStrideDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("InterPred16x16Copy_ASM panicked on invalid stride: %v", r)
		}
	}()
	var dst, src [256]uint8
	InterPred16x16Copy_ASM(&dst[0], &src[0], 15, 16)
	InterPred16x16Copy_ASM(&dst[0], &src[0], 16, 15)
	InterPred16x16Copy_ASM(nil, &src[0], 16, 16)
	InterPred16x16Copy_ASM(&dst[0], nil, 16, 16)
}
