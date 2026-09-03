//go:build arm64

#include "textflag.h"

// 4×4 IDCT using scalar ARM64 instructions (NEON not needed for 4 elements).
// Same butterfly as amd64 version but with ARM64 register names.

// func IDCT4x4_NEON(block *int16)
TEXT ·IDCT4x4_NEON(SB), NOSPLIT, $0-8
    MOVD block+0(FP), R0

    // Horizontal pass: 4 rows
    MOVD $4, R10
hloop:
    MOVH (R0), R1       // c0
    MOVH 2(R0), R2      // c1
    MOVH 4(R0), R3      // c2
    MOVH 6(R0), R4      // c3
    // Sign extend
    SXTH R1, R1; SXTH R2, R2; SXTH R3, R3; SXTH R4, R4

    ADD R1, R3, R5      // e0 = c0+c2
    SUB R3, R1, R6      // e1 = c0-c2
    ASR $1, R2, R7; SUB R4, R7, R7  // e2 = (c1>>1)-c3
    ASR $1, R4, R8; ADD R2, R8, R8  // e3 = c1+(c3>>1)

    ADD R5, R8, R1; MOVH R1, (R0)      // e0+e3
    ADD R6, R7, R1; MOVH R1, 2(R0)     // e1+e2
    SUB R7, R6, R1; MOVH R1, 4(R0)     // e1-e2
    SUB R8, R5, R1; MOVH R1, 6(R0)     // e0-e3

    ADD $8, R0
    SUBS $1, R10
    BNE hloop

    SUB $32, R0  // back to start

    // Vertical pass: 4 columns
    MOVD $4, R10
vloop:
    MOVH (R0), R1        // c0
    MOVH 8(R0), R2       // c1 (stride=8)
    MOVH 16(R0), R3      // c2
    MOVH 24(R0), R4      // c3
    SXTH R1, R1; SXTH R2, R2; SXTH R3, R3; SXTH R4, R4

    ADD R1, R3, R5       // e0
    SUB R3, R1, R6       // e1
    ASR $1, R2, R7; SUB R4, R7, R7  // e2
    ASR $1, R4, R8; ADD R2, R8, R8  // e3

    ADD R5, R8, R1; ADD $32, R1; ASR $6, R1; MOVH R1, (R0)
    ADD R6, R7, R1; ADD $32, R1; ASR $6, R1; MOVH R1, 8(R0)
    SUB R7, R6, R1; ADD $32, R1; ASR $6, R1; MOVH R1, 16(R0)
    SUB R8, R5, R1; ADD $32, R1; ASR $6, R1; MOVH R1, 24(R0)

    ADD $2, R0
    SUBS $1, R10
    BNE vloop
    RET

// func DCT4x4_NEON(block *int16)
TEXT ·DCT4x4_NEON(SB), NOSPLIT, $0-8
    MOVD block+0(FP), R0

    MOVD $4, R10
dh_loop:
    MOVH (R0), R1; MOVH 2(R0), R2; MOVH 4(R0), R3; MOVH 6(R0), R4
    SXTH R1, R1; SXTH R2, R2; SXTH R3, R3; SXTH R4, R4

    ADD R1, R4, R5       // s0=r0+r3
    ADD R2, R3, R6       // s1=r1+r2
    SUB R3, R2, R7       // s2=r1-r2
    SUB R4, R1, R8       // s3=r0-r3

    ADD R5, R6, R1; MOVH R1, (R0)
    LSL $1, R8, R1; ADD R7, R1; MOVH R1, 2(R0)
    SUB R6, R5, R1; MOVH R1, 4(R0)
    LSL $1, R7, R1; SUB R1, R8, R1; MOVH R1, 6(R0)

    ADD $8, R0
    SUBS $1, R10
    BNE dh_loop

    SUB $32, R0

    MOVD $4, R10
dv_loop:
    MOVH (R0), R1; MOVH 8(R0), R2; MOVH 16(R0), R3; MOVH 24(R0), R4
    SXTH R1, R1; SXTH R2, R2; SXTH R3, R3; SXTH R4, R4

    ADD R1, R4, R5; ADD R2, R3, R6; SUB R3, R2, R7; SUB R4, R1, R8

    ADD R5, R6, R1; MOVH R1, (R0)
    LSL $1, R8, R1; ADD R7, R1; MOVH R1, 8(R0)
    SUB R6, R5, R1; MOVH R1, 16(R0)
    LSL $1, R7, R1; SUB R1, R8, R1; MOVH R1, 24(R0)

    ADD $2, R0
    SUBS $1, R10
    BNE dv_loop
    RET
