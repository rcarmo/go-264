//go:build amd64 && !purego

#include "textflag.h"

// func chromaInter8SSE2(dst, src *byte, stride, fracX, fracY int)
// Separable bilinear interpolation. Horizontal values are <=2040 and final
// weighted sums <=16320, so unsigned word arithmetic is exact without overflow.
TEXT ·chromaInter8SSE2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ stride+16(FP), R8
	MOVQ fracX+24(FP), AX
	MOVL $8, BX
	SUBL AX, BX
	IMULL $65537, AX
	IMULL $65537, BX
	MOVD AX, X7
	MOVD BX, X8
	PSHUFL $0, X7, X7
	PSHUFL $0, X8, X8
	MOVQ fracY+32(FP), AX
	MOVL $8, BX
	SUBL AX, BX
	IMULL $65537, AX
	IMULL $65537, BX
	MOVD AX, X9
	MOVD BX, X10
	PSHUFL $0, X9, X9
	PSHUFL $0, X10, X10
	PXOR X6, X6
	MOVOU ·chromaRound32(SB), X11
	MOVQ $8, CX
row:
	MOVQ (SI), X0
	MOVQ 1(SI), X1
	PUNPCKLBW X6, X0
	PUNPCKLBW X6, X1
	PMULLW X8, X0
	PMULLW X7, X1
	PADDW X1, X0
	MOVQ (SI)(R8*1), X2
	MOVQ 1(SI)(R8*1), X3
	PUNPCKLBW X6, X2
	PUNPCKLBW X6, X3
	PMULLW X8, X2
	PMULLW X7, X3
	PADDW X3, X2
	PMULLW X10, X0
	PMULLW X9, X2
	PADDW X2, X0
	PADDW X11, X0
	PSRLW $6, X0
	PACKUSWB X6, X0
	MOVQ X0, (DI)
	ADDQ R8, SI
	ADDQ $8, DI
	DECQ CX
	JNZ row
	RET

DATA ·chromaRound32+0(SB)/8, $0x0020002000200020
DATA ·chromaRound32+8(SB)/8, $0x0020002000200020
GLOBL ·chromaRound32(SB), RODATA|NOPTR, $16
