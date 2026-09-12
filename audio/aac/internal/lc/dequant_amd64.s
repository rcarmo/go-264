//go:build amd64 && !purego

#include "textflag.h"

// Validated signed indices address the immutable table around tableZero.
// Scalar gathers feed two packed independent scale products. No FMA.
TEXT ·dequantSSE2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ quant+8(FP), SI
	MOVQ tableZero+16(FP), DX
	MOVQ n+24(FP), CX
	MOVSD scale+32(FP), X7
	UNPCKLPD X7, X7
dq_loop:
	CMPQ CX, $2
	JL dq_tail
	MOVLQSX (SI), AX
	MOVLQSX 4(SI), BX
	MOVSD (DX)(AX*8), X0
	MOVSD (DX)(BX*8), X1
	UNPCKLPD X1, X0
	MULPD X7, X0
	MOVUPD X0, (DI)
	ADDQ $8, SI
	ADDQ $16, DI
	SUBQ $2, CX
	JMP dq_loop
dq_tail:
	TESTQ CX, CX
	JZ dq_done
	MOVLQSX (SI), AX
	MOVSD (DX)(AX*8), X0
	MULSD X7, X0
	MOVSD X0, (DI)
dq_done:
	RET
