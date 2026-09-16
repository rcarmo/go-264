//go:build amd64 && !purego

#include "textflag.h"

DATA ·dequantRound2+0(SB)/4, $2
DATA ·dequantRound2+4(SB)/4, $2
DATA ·dequantRound2+8(SB)/4, $2
DATA ·dequantRound2+12(SB)/4, $2
GLOBL ·dequantRound2(SB), RODATA|NOPTR, $16

TEXT ·dequant4SSE2(SB), NOSPLIT, $0-16
	MOVQ block+0(FP), DI
	MOVQ scale+8(FP), SI
	MOVUPD (DI), X0
	MOVUPD (SI), X1
	PMULLW X1, X0
	MOVUPD X0, (DI)
	MOVUPD 16(DI), X0
	MOVUPD 16(SI), X1
	PMULLW X1, X0
	MOVUPD X0, 16(DI)
	RET

// Signed 16x16 ->32 using high/low products, +2 then arithmetic>>2,
// wrap narrowing (not saturation) matches the existing H264 lowQP rule.
TEXT ·dequant8SSE2(SB), NOSPLIT, $0-16
	MOVQ block+0(FP), DI
	MOVQ scale+8(FP), SI
	MOVUPD ·dequantRound2(SB), X7
	MOVQ $8, CX
d8_loop:
	MOVUPD (DI), X0
	MOVUPD (SI), X1
	MOVAPD X0, X2
	PMULLW X1, X0
	PMULHW X1, X2
	MOVAPD X0, X3
	PUNPCKLWL X2, X0
	PUNPCKHWL X2, X3
	PADDL X7, X0
	PADDL X7, X3
	PSRAL $2, X0
	PSRAL $2, X3
	PSLLL $16, X0
	PSRAL $16, X0
	PSLLL $16, X3
	PSRAL $16, X3
	PACKSSLW X3, X0
	MOVUPD X0, (DI)
	ADDQ $16, DI
	ADDQ $16, SI
	DECQ CX
	JNZ d8_loop
	RET
