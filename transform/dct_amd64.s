//go:build amd64 && !purego

#include "textflag.h"

// func cpuidHasAVX2() bool
TEXT ·cpuidHasAVX2(SB), NOSPLIT, $0-1
    MOVL $7, AX
    XORL CX, CX
    CPUID
    SHRL $5, BX
    ANDL $1, BX
    MOVB BX, ret+0(FP)
    RET

// Macro: process one row of horizontal IDCT butterfly
// Input: DI points to row start (4 int16 = 8 bytes)
// Uses: AX, BX, CX, DX, R8-R11
#define HROW_IDCT(off) \
    MOVWLSX off+0(DI), AX; \
    MOVWLSX off+2(DI), BX; \
    MOVWLSX off+4(DI), CX; \
    MOVWLSX off+6(DI), DX; \
    MOVL AX, R8; ADDL CX, R8; \
    MOVL AX, R9; SUBL CX, R9; \
    MOVL BX, R10; SARL $1, R10; SUBL DX, R10; \
    MOVL DX, R11; SARL $1, R11; ADDL BX, R11; \
    MOVL R8, AX; ADDL R11, AX; MOVW AX, off+0(DI); \
    MOVL R9, AX; ADDL R10, AX; MOVW AX, off+2(DI); \
    MOVL R9, AX; SUBL R10, AX; MOVW AX, off+4(DI); \
    MOVL R8, AX; SUBL R11, AX; MOVW AX, off+6(DI)

// func IDCT4x4_AVX2(block *int16)
TEXT ·IDCT4x4_AVX2(SB), NOSPLIT, $0-8
    MOVQ block+0(FP), DI

    // Horizontal pass: 4 rows
    HROW_IDCT(0)
    HROW_IDCT(8)
    HROW_IDCT(16)
    HROW_IDCT(24)

    // Vertical pass: 4 columns
    XORL SI, SI
vloop_idct:
    CMPL SI, $4
    JGE  done_idct

    LEAQ (DI)(SI*2), R12
    MOVWLSX (R12), AX
    MOVWLSX 8(R12), BX
    MOVWLSX 16(R12), CX
    MOVWLSX 24(R12), DX

    MOVL AX, R8; ADDL CX, R8
    MOVL AX, R9; SUBL CX, R9
    MOVL BX, R10; SARL $1, R10; SUBL DX, R10
    MOVL DX, R11; SARL $1, R11; ADDL BX, R11

    MOVL R8, AX; ADDL R11, AX; ADDL $32, AX; SARL $6, AX; MOVW AX, (R12)
    MOVL R9, AX; ADDL R10, AX; ADDL $32, AX; SARL $6, AX; MOVW AX, 8(R12)
    MOVL R9, AX; SUBL R10, AX; ADDL $32, AX; SARL $6, AX; MOVW AX, 16(R12)
    MOVL R8, AX; SUBL R11, AX; ADDL $32, AX; SARL $6, AX; MOVW AX, 24(R12)

    INCL SI
    JMP  vloop_idct
done_idct:
    RET

// Macro: process one row of horizontal DCT butterfly
#define HROW_DCT(off) \
    MOVWLSX off+0(DI), AX; \
    MOVWLSX off+2(DI), BX; \
    MOVWLSX off+4(DI), CX; \
    MOVWLSX off+6(DI), DX; \
    MOVL AX, R8; ADDL DX, R8; \
    MOVL BX, R9; ADDL CX, R9; \
    MOVL BX, R10; SUBL CX, R10; \
    MOVL AX, R11; SUBL DX, R11; \
    MOVL R8, AX; ADDL R9, AX; MOVW AX, off+0(DI); \
    MOVL R11, AX; SHLL $1, AX; ADDL R10, AX; MOVW AX, off+2(DI); \
    MOVL R8, AX; SUBL R9, AX; MOVW AX, off+4(DI); \
    MOVL R10, AX; SHLL $1, AX; MOVL R11, BX; SUBL AX, BX; MOVW BX, off+6(DI)

// func DCT4x4_AVX2(block *int16)
TEXT ·DCT4x4_AVX2(SB), NOSPLIT, $0-8
    MOVQ block+0(FP), DI

    HROW_DCT(0)
    HROW_DCT(8)
    HROW_DCT(16)
    HROW_DCT(24)

    // Vertical pass
    XORL SI, SI
vloop_dct:
    CMPL SI, $4
    JGE  done_dct

    LEAQ (DI)(SI*2), R12
    MOVWLSX (R12), AX
    MOVWLSX 8(R12), BX
    MOVWLSX 16(R12), CX
    MOVWLSX 24(R12), DX

    MOVL AX, R8; ADDL DX, R8
    MOVL BX, R9; ADDL CX, R9
    MOVL BX, R10; SUBL CX, R10
    MOVL AX, R11; SUBL DX, R11

    MOVL R8, AX; ADDL R9, AX; MOVW AX, (R12)
    MOVL R11, AX; SHLL $1, AX; ADDL R10, AX; MOVW AX, 8(R12)
    MOVL R8, AX; SUBL R9, AX; MOVW AX, 16(R12)
    MOVL R10, AX; SHLL $1, AX; MOVL R11, BX; SUBL AX, BX; MOVW BX, 24(R12)

    INCL SI
    JMP  vloop_dct
done_dct:
    RET
