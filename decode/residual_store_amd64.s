//go:build amd64 && !purego

#include "textflag.h"

// func residualAddStoreSSE2(dst *byte, dstStride int, predicted *byte, predStride int, residual *int16, residualStride, w, h int)
TEXT ·residualAddStoreSSE2(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), R8
	MOVQ predicted+16(FP), SI
	MOVQ predStride+24(FP), R9
	MOVQ residual+32(FP), DX
	MOVQ residualStride+40(FP), R10
	SHLQ $1, R10
	MOVQ w+48(FP), R11
	MOVQ h+56(FP), CX
	PXOR X15, X15
row:
	CMPQ R11, $4
	JE load4
	MOVQ (SI), X0
	MOVOU (DX), X1
	JMP loaded
load4:
	MOVL (SI), X0
	MOVQ (DX), X1
loaded:
	// Prediction bytes -> unsigned words, then low/high unsigned dwords.
	PUNPCKLBW X15, X0
	MOVOU X0, X2
	PUNPCKLWL X15, X0
	PUNPCKHWL X15, X2
	// Signed residual words -> low/high signed dwords.
	MOVOU X1, X3
	PSRAW $15, X3
	MOVOU X1, X4
	PUNPCKLWL X3, X1
	PUNPCKHWL X3, X4
	PADDL X1, X0
	PADDL X4, X2
	PACKSSLW X2, X0
	PACKUSWB X0, X0
	CMPQ R11, $4
	JE store4
	MOVQ X0, (DI)
	JMP next
store4:
	MOVL X0, (DI)
next:
	ADDQ R8, DI
	ADDQ R9, SI
	ADDQ R10, DX
	DECQ CX
	JNZ row
	RET
