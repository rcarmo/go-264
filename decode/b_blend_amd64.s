//go:build amd64 && !purego

#include "textflag.h"

// func biBlendAvgSSE2(dst, predL0, predL1 *byte, stride, w, h int)
TEXT ·biBlendAvgSSE2(SB), NOSPLIT, $0-48
	MOVQ dst+0(FP), DI
	MOVQ predL0+8(FP), SI
	MOVQ predL1+16(FP), DX
	MOVQ stride+24(FP), R8
	MOVQ w+32(FP), R9
	MOVQ h+40(FP), CX
avg_row:
	CMPQ R9, $16
	JE avg_16
	CMPQ R9, $8
	JE avg_8
	MOVL (SI), X0
	MOVL (DX), X1
	PAVGB X1, X0
	MOVL X0, (DI)
	JMP avg_next
avg_8:
	MOVQ (SI), X0
	MOVQ (DX), X1
	PAVGB X1, X0
	MOVQ X0, (DI)
	JMP avg_next
avg_16:
	MOVOU (SI), X0
	MOVOU (DX), X1
	PAVGB X1, X0
	MOVOU X0, (DI)
avg_next:
	ADDQ R8, DI
	ADDQ R8, SI
	ADDQ R8, DX
	DECQ CX
	JNZ avg_row
	RET

// func biBlendWeightedSSE2(dst, predL0, predL1 *byte, stride, w, h, w0, w1, round, shift, offset int)
// Interleave unsigned prediction words as (L0,L1) pairs, then PMADDWD with
// alternating signed weights. Each dword is one exact L0*w0 + L1*w1 sum.
TEXT ·biBlendWeightedSSE2(SB), NOSPLIT, $0-88
	MOVQ dst+0(FP), DI
	MOVQ predL0+8(FP), SI
	MOVQ predL1+16(FP), DX
	MOVQ stride+24(FP), R8
	MOVQ w+32(FP), R9
	MOVQ h+40(FP), CX
	MOVQ w0+48(FP), AX
	MOVQ w1+56(FP), BX
	ANDQ $65535, AX
	SHLQ $16, BX
	ORQ BX, AX
	MOVD AX, X12
	PSHUFL $0, X12, X12
	MOVQ round+64(FP), AX
	MOVD AX, X14
	PSHUFL $0, X14, X14
	MOVQ shift+72(FP), AX
	MOVD AX, X11
	MOVQ offset+80(FP), AX
	MOVD AX, X10
	PSHUFL $0, X10, X10
	PXOR X15, X15
weighted_row:
	XORQ R10, R10
weighted_chunk:
	CMPQ R9, $4
	JE weighted_load4
	MOVQ (SI)(R10*1), X0
	MOVQ (DX)(R10*1), X1
	JMP weighted_loaded
weighted_load4:
	MOVL (SI), X0
	MOVL (DX), X1
weighted_loaded:
	PUNPCKLBW X15, X0
	PUNPCKLBW X15, X1
	MOVOU X0, X2
	PUNPCKLWL X1, X0
	PUNPCKHWL X1, X2
	PMADDWL X12, X0
	PMADDWL X12, X2
	PADDL X14, X0
	PADDL X14, X2
	PSRAL X11, X0
	PSRAL X11, X2
	PADDL X10, X0
	PADDL X10, X2
	PACKSSLW X2, X0
	PACKUSWB X0, X0
	CMPQ R9, $4
	JE weighted_store4
	MOVQ X0, (DI)(R10*1)
	ADDQ $8, R10
	CMPQ R10, R9
	JLT weighted_chunk
	JMP weighted_next
weighted_store4:
	MOVL X0, (DI)
weighted_next:
	ADDQ R8, DI
	ADDQ R8, SI
	ADDQ R8, DX
	DECQ CX
	JNZ weighted_row
	RET
