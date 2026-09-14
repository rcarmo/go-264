//go:build amd64 && !purego

#include "textflag.h"

// func chromaBilinearSSE2(dst, src *byte, stride, w, h, wa, wb, wc, wd int)
// Exact-width loads cover only the documented (w+1)x(h+1) footprint. Products
// and sums fit unsigned 16-bit lanes because wa+wb+wc+wd == 64.
TEXT ·chromaBilinearSSE2(SB), NOSPLIT, $0-72
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ stride+16(FP), R8
	MOVQ w+24(FP), R9
	MOVQ h+32(FP), CX
	MOVQ wa+40(FP), AX
	IMULL $65537, AX
	MOVD AX, X8
	PSHUFL $0, X8, X8
	MOVQ wb+48(FP), AX
	IMULL $65537, AX
	MOVD AX, X9
	PSHUFL $0, X9, X9
	MOVQ wc+56(FP), AX
	IMULL $65537, AX
	MOVD AX, X10
	PSHUFL $0, X10, X10
	MOVQ wd+64(FP), AX
	IMULL $65537, AX
	MOVD AX, X11
	PSHUFL $0, X11, X11
	PXOR X7, X7
	MOVOU ·chromaRound32(SB), X12
row:
	CMPQ R9, $8
	JE load8
	CMPQ R9, $4
	JE load4
	MOVW (SI), AX
	MOVD AX, X0
	MOVW 1(SI), AX
	MOVD AX, X1
	MOVW (SI)(R8*1), AX
	MOVD AX, X2
	MOVW 1(SI)(R8*1), AX
	MOVD AX, X3
	JMP loaded
load4:
	MOVL (SI), AX
	MOVD AX, X0
	MOVL 1(SI), AX
	MOVD AX, X1
	MOVL (SI)(R8*1), AX
	MOVD AX, X2
	MOVL 1(SI)(R8*1), AX
	MOVD AX, X3
	JMP loaded
load8:
	MOVQ (SI), X0
	MOVQ 1(SI), X1
	MOVQ (SI)(R8*1), X2
	MOVQ 1(SI)(R8*1), X3
loaded:
	PUNPCKLBW X7, X0
	PUNPCKLBW X7, X1
	PUNPCKLBW X7, X2
	PUNPCKLBW X7, X3
	PMULLW X8, X0
	PMULLW X9, X1
	PMULLW X10, X2
	PMULLW X11, X3
	PADDW X1, X0
	PADDW X3, X2
	PADDW X2, X0
	PADDW X12, X0
	PSRLW $6, X0
	PACKUSWB X7, X0
	CMPQ R9, $8
	JE store8
	MOVD X0, AX
	CMPQ R9, $4
	JE store4
	MOVW AX, (DI)
	JMP stored
store4:
	MOVL AX, (DI)
	JMP stored
store8:
	MOVQ X0, (DI)
stored:
	ADDQ R8, SI
	ADDQ $8, DI
	DECQ CX
	JNZ row
	RET
