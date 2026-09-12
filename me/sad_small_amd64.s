//go:build amd64 && !purego

#include "textflag.h"

// MOVL/MOVQ zero upper bytes so PSADBW sums only the exact4/8 row width.
TEXT ·sadSmallSSE2(SB), NOSPLIT, $0-48
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ strideA+16(FP), AX
	MOVQ strideB+24(FP), BX
	MOVQ size+32(FP), CX
	MOVQ CX, DX
	PXOR X2, X2
	CMPQ CX, $4
	JE sad4_loop
sad8_loop:
	MOVQ (SI), X0
	MOVQ (DI), X1
	PSADBW X1, X0
	PADDQ X0, X2
	ADDQ AX, SI
	ADDQ BX, DI
	DECQ DX
	JNZ sad8_loop
	JMP sadsmall_done
sad4_loop:
	MOVL (SI), X0
	MOVL (DI), X1
	PSADBW X1, X0
	PADDQ X0, X2
	ADDQ AX, SI
	ADDQ BX, DI
	DECQ DX
	JNZ sad4_loop
sadsmall_done:
	MOVQ X2, AX
	MOVQ AX, ret+40(FP)
	RET
