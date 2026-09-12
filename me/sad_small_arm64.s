//go:build arm64 && !purego

#include "textflag.h"

// func sadSmallNEON(a, b *byte, strideA, strideB, size int) int
// Loads exactly size bytes per row (4 or 8), then computes unsigned
// max-min differences in eight byte lanes. Unused 4-wide lanes are zero.
TEXT ·sadSmallNEON(SB), NOSPLIT, $0-48
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	MOVD strideA+16(FP), R2
	MOVD strideB+24(FP), R3
	MOVD size+32(FP), R4
	MOVD R4, R5
	MOVD $0, R6
sad_small_row:
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	CMP $8, R4
	BEQ sad_small_8
	VLD1 (R0), V0.S[0]
	VLD1 (R1), V1.S[0]
	B sad_small_diff
sad_small_8:
	VLD1 (R0), V0.D[0]
	VLD1 (R1), V1.D[0]
sad_small_diff:
	VUMAX V1.B8, V0.B8, V2.B8
	VUMIN V1.B8, V0.B8, V3.B8
	VSUB V3.B8, V2.B8, V2.B8
	VUADDLV V2.B8, V4
	VMOV V4.S[0], R7
	ADD R7, R6
	ADD R2, R0
	ADD R3, R1
	SUBS $1, R5
	BNE sad_small_row
	MOVD R6, ret+40(FP)
	RET
