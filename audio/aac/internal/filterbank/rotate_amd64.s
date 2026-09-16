//go:build amd64 && !purego

#include "textflag.h"

// Compute complex(c,0)*rotation without dropping signed zero terms,
// using packed real/imaginary lanes for each coefficient.
TEXT ·rotateInputSSE2(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ coeff+8(FP), SI
	MOVQ rotation+16(FP), DX
	MOVQ n+24(FP), CX
	XORPD X7, X7
ri_loop:
	TESTQ CX, CX
	JZ ri_done
	MOVSD (SI), X0
	UNPCKLPD X0, X0
	MOVUPD (DX), X1
	MULPD X1, X0
	SHUFPD $1, X1, X1
	MULPD X7, X1
	// low: c*real - 0*imag; high: c*imag + 0*real
	MOVAPD X0, X2
	SUBSD X1, X0
	ADDPD X1, X2
	SHUFPD $2, X2, X0
	MOVUPD X0, (DI)
	ADDQ $8, SI
	ADDQ $16, DX
	ADDQ $16, DI
	DECQ CX
	JMP ri_loop
ri_done:
	RET

// Packed products for two complex values followed by independent real
// differences. No horizontal sum or fused multiply-add changes rounding.
TEXT ·rotateOutputSSE2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ rotation+16(FP), DX
	MOVQ n+24(FP), CX
	MOVSD scale+32(FP), X7
	UNPCKLPD X7, X7
ro_loop:
	CMPQ CX, $2
	JL ro_tail
	MOVUPD (SI), X0
	MOVUPD (DX), X1
	MULPD X1, X0
	MOVUPD 16(SI), X2
	MOVUPD 16(DX), X3
	MULPD X3, X2
	MOVAPD X0, X1
	UNPCKLPD X2, X0
	UNPCKHPD X2, X1
	SUBPD X1, X0
	MULPD X7, X0
	MOVUPD X0, (DI)
	ADDQ $32, SI
	ADDQ $32, DX
	ADDQ $16, DI
	SUBQ $2, CX
	JMP ro_loop
ro_tail:
	TESTQ CX, CX
	JZ ro_done
	MOVUPD (SI), X0
	MOVUPD (DX), X1
	MULPD X1, X0
	MOVAPD X0, X1
	SHUFPD $1, X1, X1
	SUBSD X1, X0
	MULSD X7, X0
	MOVSD X0, (DI)
ro_done:
	RET
