//go:build amd64 && !purego

#include "textflag.h"

// One SIMD register holds the real/imaginary lanes of one complex value.
// MULPD computes independent products; SHUFPD supplies the cross products.
// Separate adds/subtracts preserve the Go complex operation order (no FMA).
TEXT ·fftStageSSE2(SB), NOSPLIT, $0-40
	MOVQ x+0(FP), AX
	MOVQ roots+8(FP), BX
	MOVQ n+16(FP), CX
	MOVQ half+24(FP), DX
	MOVQ stride+32(FP), SI
	SHLQ $4, CX
	SHLQ $4, DX
	SHLQ $4, SI
	LEAQ (AX)(CX*1), R8
fft_block:
	CMPQ AX, R8
	JAE fft_done
	LEAQ (AX)(DX*1), R9
	MOVQ BX, R10
	MOVQ DX, R11
fft_butterfly:
	MOVUPD (R9), X0
	MOVUPD (R10), X1
	MOVAPD X0, X2
	MULPD X1, X2
	SHUFPD $1, X1, X1
	MULPD X1, X0
	MOVAPD X2, X3
	SHUFPD $1, X3, X3
	SUBSD X3, X2
	MOVAPD X0, X3
	SHUFPD $1, X3, X3
	ADDSD X3, X0
	UNPCKLPD X0, X2
	MOVUPD (AX), X4
	MOVAPD X4, X5
	ADDPD X2, X4
	SUBPD X2, X5
	MOVUPD X4, (AX)
	MOVUPD X5, (R9)
	ADDQ $16, AX
	ADDQ $16, R9
	ADDQ SI, R10
	SUBQ $16, R11
	JNZ fft_butterfly
	MOVQ R9, AX
	JMP fft_block
fft_done:
	RET
