//go:build amd64 && !purego

#include "textflag.h"

DATA ·scaleS16+0(SB)/8, $32768.0
DATA ·scaleS16+8(SB)/8, $32768.0
GLOBL ·scaleS16(SB), RODATA|NOPTR, $16
DATA ·half+0(SB)/8, $0.5
DATA ·half+8(SB)/8, $0.5
GLOBL ·half(SB), RODATA|NOPTR, $16
DATA ·maxS16+0(SB)/8, $32767.0
DATA ·maxS16+8(SB)/8, $32767.0
GLOBL ·maxS16(SB), RODATA|NOPTR, $16
DATA ·absMask+0(SB)/8, $0x7fffffffffffffff
DATA ·absMask+8(SB)/8, $0x7fffffffffffffff
GLOBL ·absMask(SB), RODATA|NOPTR, $16
DATA ·maxFinite+0(SB)/8, $0x7fefffffffffffff
DATA ·maxFinite+8(SB)/8, $0x7fefffffffffffff
GLOBL ·maxFinite(SB), RODATA|NOPTR, $16

// finiteSSE2 returns false for NaN and either infinity without reading beyond
// src. Clearing the sign and comparing as float64 against MaxFloat64 classifies
// all finite bit patterns, including signed zero and subnormals.
TEXT ·finiteSSE2(SB), NOSPLIT, $0-17
	MOVQ src+0(FP), SI
	MOVQ n+8(FP), CX
	MOVUPD ·absMask(SB), X6
	MOVUPD ·maxFinite(SB), X7
finite_pairs:
	CMPQ CX, $2
	JL finite_tail
	MOVUPD (SI), X0
	ANDPD X6, X0
	MOVAPD X7, X1
	CMPPD X0, X1, $1
	MOVAPD X0, X2
	CMPPD X0, X2, $3
	ORPD X2, X1
	MOVMSKPD X1, AX
	TESTQ AX, AX
	JNZ finite_bad
	ADDQ $16, SI
	SUBQ $2, CX
	JMP finite_pairs
finite_tail:
	TESTQ CX, CX
	JZ finite_good
	MOVSD (SI), X0
	ANDPD X6, X0
	MOVAPD X7, X1
	CMPPD X0, X1, $1
	MOVAPD X0, X2
	CMPPD X0, X2, $3
	ORPD X2, X1
	MOVMSKPD X1, AX
	TESTQ AX, AX
	JNZ finite_bad
finite_good:
	MOVB $1, ret+16(FP)
	RET
finite_bad:
	MOVB $0, ret+16(FP)
	RET

// Exact ties-away-from-zero without adding0.5 to the input: truncate abs(x),
// compare its exact fractional residual with0.5, increment, then restore sign.
// This avoids rounding nextafter(k+0.5,-Inf) up accidentally. Finite validation
// is performed in Go before entry. Clip before integer conversion to avoid overflow.
TEXT ·s16SSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	MOVUPD ·scaleS16(SB), X7
	MOVUPD ·absMask(SB), X6
	MOVUPD ·half(SB), X5
	MOVUPD ·maxS16(SB), X4
	XORPD X3, X3
s16_loop:
	TESTQ CX, CX
	JZ s16_done
	CMPQ CX, $2
	JL s16_loadtail
	MOVUPD (SI), X0
	JMP s16_convert
s16_loadtail:
	MOVSD (SI), X0
s16_convert:
	MULPD X7, X0
	MOVAPD X3, X1
	SUBPD X7, X1
	MAXPD X1, X0
	MINPD X4, X0
	MOVAPD X0, X8
	CMPPD X3, X8, $1
	MOVAPD X8, X9
	PSHUFL $0x88, X9, X9
	ANDPD X6, X0
	CVTTPD2PL X0, X1
	CVTPL2PD X1, X2
	SUBPD X2, X0
	MOVAPD X5, X2
	CMPPD X0, X2, $2
	PSHUFL $0x88, X2, X2
	PSUBL X2, X1
	PXOR X9, X1
	PSUBL X9, X1
	PACKSSLW X1, X1
	MOVQ X1, AX
	MOVW AX, (DI)
	CMPQ CX, $2
	JL s16_done
	SHRQ $16, AX
	MOVW AX, 2(DI)
	ADDQ $16, SI
	ADDQ $4, DI
	SUBQ $2, CX
	JMP s16_loop
s16_done:
	RET

TEXT ·stereoToMonoSSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ frames+16(FP), CX
	MOVUPD ·half(SB), X7
stm_loop:
	CMPQ CX, $2
	JL stm_tail
	MOVUPD (SI), X0
	MOVUPD 16(SI), X1
	MOVAPD X0, X2
	UNPCKLPD X1, X0
	UNPCKHPD X1, X2
	ADDPD X2, X0
	MULPD X7, X0
	MOVUPD X0, (DI)
	ADDQ $32, SI
	ADDQ $16, DI
	SUBQ $2, CX
	JMP stm_loop
stm_tail:
	TESTQ CX, CX
	JZ stm_done
	MOVSD (SI), X0
	ADDSD 8(SI), X0
	MULSD X7, X0
	MOVSD X0, (DI)
stm_done:
	RET

TEXT ·monoToStereoSSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ frames+16(FP), CX
mts_loop:
	TESTQ CX, CX
	JZ mts_done
	MOVSD (SI), X0
	UNPCKLPD X0, X0
	MOVUPD X0, (DI)
	ADDQ $8, SI
	ADDQ $16, DI
	DECQ CX
	JMP mts_loop
mts_done:
	RET

TEXT ·interleaveStereoSSE2(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ left+8(FP), SI
	MOVQ right+16(FP), DX
	MOVQ frames+24(FP), CX
is_loop:
	CMPQ CX, $2
	JL is_tail
	MOVUPD (SI), X0
	MOVUPD (DX), X1
	MOVAPD X0, X2
	UNPCKLPD X1, X0
	UNPCKHPD X1, X2
	MOVUPD X0, (DI)
	MOVUPD X2, 16(DI)
	ADDQ $16, SI
	ADDQ $16, DX
	ADDQ $32, DI
	SUBQ $2, CX
	JMP is_loop
is_tail:
	TESTQ CX, CX
	JZ is_done
	MOVSD (SI), X0
	MOVSD (DX), X1
	UNPCKLPD X1, X0
	MOVUPD X0, (DI)
is_done:
	RET
