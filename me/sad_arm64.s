//go:build arm64

#include "textflag.h"

// func SAD16x16_ASM_NEON(a, b *uint8, strideA, strideB int) uint32
// Go's ARM64 assembler does not expose UABD directly. max(a,b)-min(a,b)
// is the same unsigned byte difference; UADDLV widens and sums each row.
TEXT ·SAD16x16_ASM_NEON(SB), NOSPLIT, $0-36
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	MOVD strideA+16(FP), R2
	MOVD strideB+24(FP), R3
	MOVD $16, R4
	MOVD $0, R5
sad_loop:
	VLD1 (R0), [V0.B16]
	VLD1 (R1), [V1.B16]
	VUMAX V1.B16, V0.B16, V2.B16
	VUMIN V1.B16, V0.B16, V3.B16
	VSUB V3.B16, V2.B16, V2.B16
	VUADDLV V2.B16, V4
	VMOV V4.S[0], R6
	ADD R6, R5
	ADD R2, R0
	ADD R3, R1
	SUBS $1, R4
	BNE sad_loop
	MOVW R5, ret+32(FP)
	RET
