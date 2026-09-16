//go:build amd64 && !purego

#include "textflag.h"

DATA ·two+0(SB)/8, $2.0
DATA ·two+8(SB)/8, $2.0
GLOBL ·two(SB), RODATA|NOPTR, $16

// Packed lanes preserve the scalar operation order: (current + previous) * 2.
TEXT ·overlapAddSSE2(SB), NOSPLIT, $0-32
	MOVQ output+0(FP), DI
	MOVQ transformed+8(FP), SI
	MOVQ previous+16(FP), DX
	MOVQ n+24(FP), CX
	MOVUPD ·two(SB), X7
loop:
	CMPQ CX, $2
	JL tail
	MOVUPD (SI), X0
	MOVUPD (DX), X1
	ADDPD X1, X0
	MULPD X7, X0
	MOVUPD X0, (DI)
	ADDQ $16, SI
	ADDQ $16, DI
	ADDQ $16, DX
	SUBQ $2, CX
	JMP loop
tail:
	TESTQ CX, CX
	JZ done
	MOVSD (SI), X0
	ADDSD (DX), X0
	MULSD X7, X0
	MOVSD X0, (DI)
done:
	RET
