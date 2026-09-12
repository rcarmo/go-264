//go:build arm64 && !purego

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
// V0..V3 hold the four input columns, one row per signed16 lane. The first
// butterfly transforms all four rows in parallel with int16 wrapping, exactly
// matching Go. Store it row-major, then retain the established scalar vertical
// pass; this avoids claiming a full vector transform before the cross-lane
// narrow-between-pass ordering is independently qualified.
TEXT ·DCT4x4_NEON(SB), NOSPLIT, $0-8
    MOVD block+0(FP), R0
    VLD4 (R0), [V0.H4, V1.H4, V2.H4, V3.H4]
    VADD V3.H4, V0.H4, V4.H4
    VADD V2.H4, V1.H4, V5.H4
    VSUB V2.H4, V1.H4, V6.H4
    VSUB V3.H4, V0.H4, V7.H4
    VADD V5.H4, V4.H4, V8.H4
    VSHL $1, V7.H4, V12.H4
    VADD V6.H4, V12.H4, V9.H4
    VSUB V5.H4, V4.H4, V10.H4
    VSHL $1, V6.H4, V12.H4
    VSUB V12.H4, V7.H4, V11.H4
    // Store columns consecutively, then structure-load them as four rows.
    // This packed in-place transpose retains the int16-narrowed first pass.
    VST1 [V8.H4, V9.H4, V10.H4, V11.H4], (R0)
    VLD4 (R0), [V0.H4, V1.H4, V2.H4, V3.H4]

    VADD V3.H4, V0.H4, V4.H4
    VADD V2.H4, V1.H4, V5.H4
    VSUB V2.H4, V1.H4, V6.H4
    VSUB V3.H4, V0.H4, V7.H4
    VADD V5.H4, V4.H4, V8.H4
    VSHL $1, V7.H4, V12.H4
    VADD V6.H4, V12.H4, V9.H4
    VSUB V5.H4, V4.H4, V10.H4
    VSHL $1, V6.H4, V12.H4
    VSUB V12.H4, V7.H4, V11.H4
    VST1 [V8.H4, V9.H4, V10.H4, V11.H4], (R0)
    RET
