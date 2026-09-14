//go:build arm64 && !purego && !go1.27

#include "textflag.h"

// Pre-Go 1.27 NEON implementation using the older assembler instruction set.
// Signed widening and shifts are expanded into supported vector operations.

// func IDCT4x4_NEON(block *int16)
// Widen signed H4 lanes explicitly: zero-extend then subtract sign<<16.
// Outputs narrow modulo int16 after each pass, matching historical assembly.
TEXT ·IDCT4x4_NEON(SB), NOSPLIT, $0-8
    MOVD block+0(FP), R0
    VLD4 (R0), [V0.H4, V1.H4, V2.H4, V3.H4]
    VUSHR $15, V0.H4, V12.H4; VUXTL V0.H4, V0.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V0.S4, V0.S4
    VUSHR $15, V1.H4, V12.H4; VUXTL V1.H4, V1.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V1.S4, V1.S4
    VUSHR $15, V2.H4, V12.H4; VUXTL V2.H4, V2.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V2.S4, V2.S4
    VUSHR $15, V3.H4, V12.H4; VUXTL V3.H4, V3.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V3.S4, V3.S4

    VADD V2.S4, V0.S4, V4.S4
    VSUB V2.S4, V0.S4, V5.S4
    // arithmetic V1>>1 = logical shift - sign<<31
    VUSHR $31, V1.S4, V12.S4; VUSHR $1, V1.S4, V6.S4; VSHL $31, V12.S4, V12.S4; VSUB V12.S4, V6.S4, V6.S4; VSUB V3.S4, V6.S4, V6.S4
    VUSHR $31, V3.S4, V12.S4; VUSHR $1, V3.S4, V7.S4; VSHL $31, V12.S4, V12.S4; VSUB V12.S4, V7.S4, V7.S4; VADD V7.S4, V1.S4, V7.S4
    VADD V7.S4, V4.S4, V8.S4
    VADD V6.S4, V5.S4, V9.S4
    VSUB V6.S4, V5.S4, V10.S4
    VSUB V7.S4, V4.S4, V11.S4
    VUZP1 V8.H8, V8.H8, V8.H8; VUZP1 V9.H8, V9.H8, V9.H8; VUZP1 V10.H8, V10.H8, V10.H8; VUZP1 V11.H8, V11.H8, V11.H8
    VST1 [V8.H4, V9.H4, V10.H4, V11.H4], (R0)
    VLD4 (R0), [V0.H4, V1.H4, V2.H4, V3.H4]

    VUSHR $15, V0.H4, V12.H4; VUXTL V0.H4, V0.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V0.S4, V0.S4
    VUSHR $15, V1.H4, V12.H4; VUXTL V1.H4, V1.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V1.S4, V1.S4
    VUSHR $15, V2.H4, V12.H4; VUXTL V2.H4, V2.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V2.S4, V2.S4
    VUSHR $15, V3.H4, V12.H4; VUXTL V3.H4, V3.S4; VUXTL V12.H4, V12.S4; VSHL $16, V12.S4, V12.S4; VSUB V12.S4, V3.S4, V3.S4
    VADD V2.S4, V0.S4, V4.S4
    VSUB V2.S4, V0.S4, V5.S4
    VUSHR $31, V1.S4, V12.S4; VUSHR $1, V1.S4, V6.S4; VSHL $31, V12.S4, V12.S4; VSUB V12.S4, V6.S4, V6.S4; VSUB V3.S4, V6.S4, V6.S4
    VUSHR $31, V3.S4, V12.S4; VUSHR $1, V3.S4, V7.S4; VSHL $31, V12.S4, V12.S4; VSUB V12.S4, V7.S4, V7.S4; VADD V7.S4, V1.S4, V7.S4
    VADD V7.S4, V4.S4, V8.S4
    VADD V6.S4, V5.S4, V9.S4
    VSUB V6.S4, V5.S4, V10.S4
    VSUB V7.S4, V4.S4, V11.S4
    MOVD $32, R1; VDUP R1, V13.S4
    VADD V13.S4, V8.S4, V8.S4; VADD V13.S4, V9.S4, V9.S4; VADD V13.S4, V10.S4, V10.S4; VADD V13.S4, V11.S4, V11.S4
    // arithmetic >>6 = logical >>6 - sign<<26
    VUSHR $31, V8.S4, V12.S4; VUSHR $6, V8.S4, V8.S4; VSHL $26, V12.S4, V12.S4; VSUB V12.S4, V8.S4, V8.S4
    VUSHR $31, V9.S4, V12.S4; VUSHR $6, V9.S4, V9.S4; VSHL $26, V12.S4, V12.S4; VSUB V12.S4, V9.S4, V9.S4
    VUSHR $31, V10.S4, V12.S4; VUSHR $6, V10.S4, V10.S4; VSHL $26, V12.S4, V12.S4; VSUB V12.S4, V10.S4, V10.S4
    VUSHR $31, V11.S4, V12.S4; VUSHR $6, V11.S4, V11.S4; VSHL $26, V12.S4, V12.S4; VSUB V12.S4, V11.S4, V11.S4
    VUZP1 V8.H8, V8.H8, V8.H8; VUZP1 V9.H8, V9.H8, V9.H8; VUZP1 V10.H8, V10.H8, V10.H8; VUZP1 V11.H8, V11.H8, V11.H8
    VST1 [V8.H4, V9.H4, V10.H4, V11.H4], (R0)
    RET

// func DCT4x4_NEON(block *int16)
// V0..V3 hold the four input columns, one row per signed16 lane. The first
// butterfly transforms all four rows in parallel with int16 wrapping, exactly
// matching Go. A store/reload transpose prepares the vector column pass
// while retaining the int16-narrowed first-pass values.
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
