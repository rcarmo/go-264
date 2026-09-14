//go:build amd64 && !purego

#include "textflag.h"

// func reconstructAdd4x4SSE2(dst, prediction *byte, residual *int16,
//     dstStride, predStride int)
// Add four signed residual words to four prediction bytes per row and saturate.
TEXT ·reconstructAdd4x4SSE2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ prediction+8(FP), SI
	MOVQ residual+16(FP), AX
	MOVQ dstStride+24(FP), R8
	MOVQ predStride+32(FP), R9
	PXOR X7, X7
	MOVQ $4, CX
row:
	MOVQ (AX), X0
	MOVL (SI), BX
	MOVD BX, X1
	PUNPCKLBW X7, X1
	PADDSW X1, X0
	PACKUSWB X7, X0
	MOVD X0, BX
	MOVL BX, (DI)
	ADDQ $8, AX
	ADDQ R9, SI
	ADDQ R8, DI
	DECQ CX
	JNZ row
	RET
