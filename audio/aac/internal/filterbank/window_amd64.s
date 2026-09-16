//go:build amd64 && !purego

#include "textflag.h"

// flags bit0: reverse weights, bit1: add to destination. Two independent
// float64 lanes, separate multiply and addition, scalar tail, unaligned-safe.
TEXT ·windowSSE2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ weights+16(FP), DX
	MOVQ n+24(FP), CX
	MOVQ flags+32(FP), R8
	MOVQ $16, R9
	TESTQ $1, R8
	JZ window_loop
	LEAQ -8(DX)(CX*8), DX
	MOVQ $-16, R9
window_loop:
	CMPQ CX, $2
	JL window_tail
	MOVUPD (SI), X0
	TESTQ $1, R8
	JNZ window_reverse
	MOVUPD (DX), X1
	JMP window_multiply
window_reverse:
	MOVUPD -8(DX), X1
	SHUFPD $1, X1, X1
window_multiply:
	MULPD X1, X0
	TESTQ $2, R8
	JZ window_store
	MOVUPD (DI), X2
	ADDPD X0, X2
	MOVAPD X2, X0
window_store:
	MOVUPD X0, (DI)
	ADDQ $16, SI
	ADDQ $16, DI
	ADDQ R9, DX
	SUBQ $2, CX
	JMP window_loop
window_tail:
	TESTQ CX, CX
	JZ window_done
	MOVSD (SI), X0
	MULSD (DX), X0
	TESTQ $2, R8
	JZ window_tail_store
	MOVSD (DI), X2
	ADDSD X0, X2
	MOVAPD X2, X0
window_tail_store:
	MOVSD X0, (DI)
window_done:
	RET

TEXT ·overlapSSE2(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ previous+16(FP), DX
	MOVQ n+24(FP), CX
ov_loop:
	CMPQ CX, $2
	JL ov_tail
	MOVUPD (SI), X0
	MOVUPD (DX), X1
	ADDPD X1, X0
	MOVUPD X0, (DI)
	ADDQ $16, SI
	ADDQ $16, DI
	ADDQ $16, DX
	SUBQ $2, CX
	JMP ov_loop
ov_tail:
	TESTQ CX, CX
	JZ ov_done
	MOVSD (SI), X0
	ADDSD (DX), X0
	MOVSD X0, (DI)
ov_done:
	RET
