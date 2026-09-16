//go:build arm64 && !purego && go1.27

#include "textflag.h"

// Transpose four rows of four int16 values in V0..V3. V4..V7 are scratch.
// H-lane interleaving forms pairs of rows; S-lane interleaving joins the pairs.
#define IDCT_TRANSPOSE \
	VZIP1 V1.H4, V0.H4, V4.H4; \
	VZIP2 V1.H4, V0.H4, V5.H4; \
	VZIP1 V3.H4, V2.H4, V6.H4; \
	VZIP2 V3.H4, V2.H4, V7.H4; \
	VZIP1 V6.S2, V4.S2, V0.S2; \
	VZIP2 V6.S2, V4.S2, V1.S2; \
	VZIP1 V7.S2, V5.S2, V2.S2; \
	VZIP2 V7.S2, V5.S2, V3.S2

// Apply four independent inverse butterflies to the signed int16 inputs in
// V0..V3. Widen before arithmetic so the final normalization cannot overflow.
// The result stays wide in V0..V3; V4..V7 hold e0..e3.
#define IDCT_BUTTERFLY \
	VSXTL V0.H4, V0.S4; \
	VSXTL V1.H4, V1.S4; \
	VSXTL V2.H4, V2.S4; \
	VSXTL V3.H4, V3.S4; \
	VADD V2.S4, V0.S4, V4.S4; \
	VSUB V2.S4, V0.S4, V5.S4; \
	VSSHR $1, V1.S4, V6.S4; \
	VSUB V3.S4, V6.S4, V6.S4; \
	VSSHR $1, V3.S4, V7.S4; \
	VADD V1.S4, V7.S4, V7.S4; \
	VADD V7.S4, V4.S4, V0.S4; \
	VADD V6.S4, V5.S4, V1.S4; \
	VSUB V6.S4, V5.S4, V2.S4; \
	VSUB V7.S4, V4.S4, V3.S4

// func IDCT4x4_NEON(block *int16)
TEXT ·IDCT4x4_NEON(SB), NOSPLIT, $0-8
	MOVD block+0(FP), R0
	FMOVD (R0), F0
	FMOVD 8(R0), F1
	FMOVD 16(R0), F2
	FMOVD 24(R0), F3

	// Columns put the four independent horizontal butterflies in vector lanes.
	IDCT_TRANSPOSE
	IDCT_BUTTERFLY
	// Match the first pass's int16 stores, including non-saturating wrap.
	VXTN V0.S4, V0.H4
	VXTN V1.S4, V1.H4
	VXTN V2.S4, V2.H4
	VXTN V3.S4, V3.H4

	// Restore rows for four independent vertical butterflies. Round only after
	// the wide second pass: signed rounded shift is exactly (value + 32) >> 6.
	IDCT_TRANSPOSE
	IDCT_BUTTERFLY
	VSRSHR $6, V0.S4, V0.S4
	VSRSHR $6, V1.S4, V1.S4
	VSRSHR $6, V2.S4, V2.S4
	VSRSHR $6, V3.S4, V3.S4
	VXTN V0.S4, V0.H4
	VXTN V1.S4, V1.H4
	VXTN V2.S4, V2.H4
	VXTN V3.S4, V3.H4
	FMOVD F0, (R0)
	FMOVD F1, 8(R0)
	FMOVD F2, 16(R0)
	FMOVD F3, 24(R0)
	RET

// func DCT4x4_NEON(block *int16)
// V0..V3 hold the four input columns, one row per signed16 lane. The first
// butterfly transforms all four rows in parallel with int16 wrapping, exactly
// matching Go. Store and reload the narrowed first pass to transpose it, then
// apply the same vector butterfly vertically.
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

// All scales fit int16; narrowing the product is exact modulo 2^16, matching
// coefficient storage. There is no rounding or saturation at this stage.
TEXT ·dequant4x4NEON(SB), NOSPLIT, $0-16
	MOVD block+0(FP), R0
	MOVD scale+8(FP), R1
	VLD1 (R0), [V0.H8, V1.H8]
	VLD1 (R1), [V2.H8, V3.H8]
	VMUL V2.H8, V0.H8, V0.H8
	VMUL V3.H8, V1.H8, V1.H8
	VST1 [V0.H8, V1.H8], (R0)
	RET

// Read coefficients without modifying them. An optional scale performs the
// same low-half products as Dequant4x4. Keep the existing first-pass narrowing
// and wide rounded second pass, then add prediction and clip directly to bytes.
TEXT ·reconstruct4x4NEON(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0
	MOVD prediction+8(FP), R1
	MOVD coeff+16(FP), R2
	MOVD scale+24(FP), R3
	MOVD dstStride+32(FP), R4
	MOVD predStride+40(FP), R5
	FMOVD (R2), F0
	FMOVD 8(R2), F1
	FMOVD 16(R2), F2
	FMOVD 24(R2), F3
	CBZ R3, fused_transform
	FMOVD (R3), F4
	FMOVD 8(R3), F5
	FMOVD 16(R3), F6
	FMOVD 24(R3), F7
	VMUL V4.H4, V0.H4, V0.H4
	VMUL V5.H4, V1.H4, V1.H4
	VMUL V6.H4, V2.H4, V2.H4
	VMUL V7.H4, V3.H4, V3.H4
fused_transform:
	IDCT_TRANSPOSE
	IDCT_BUTTERFLY
	VXTN V0.S4, V0.H4
	VXTN V1.S4, V1.H4
	VXTN V2.S4, V2.H4
	VXTN V3.S4, V3.H4
	IDCT_TRANSPOSE
	IDCT_BUTTERFLY
	VSRSHR $6, V0.S4, V0.S4
	VSRSHR $6, V1.S4, V1.S4
	VSRSHR $6, V2.S4, V2.S4
	VSRSHR $6, V3.S4, V3.S4
	VXTN V0.S4, V0.H4
	VXTN V1.S4, V1.H4
	VXTN V2.S4, V2.H4
	VXTN V3.S4, V3.H4
	FMOVS (R1), F4
	VUXTL V4.B8, V4.H8
	VSQADD V0.H8, V4.H8, V4.H8
	VSQXTUN V4.H8, V4.B8
	FMOVS F4, (R0)
	ADD R4, R0
	ADD R5, R1
	FMOVS (R1), F4
	VUXTL V4.B8, V4.H8
	VSQADD V1.H8, V4.H8, V4.H8
	VSQXTUN V4.H8, V4.B8
	FMOVS F4, (R0)
	ADD R4, R0
	ADD R5, R1
	FMOVS (R1), F4
	VUXTL V4.B8, V4.H8
	VSQADD V2.H8, V4.H8, V4.H8
	VSQXTUN V4.H8, V4.B8
	FMOVS F4, (R0)
	ADD R4, R0
	ADD R5, R1
	FMOVS (R1), F4
	VUXTL V4.B8, V4.H8
	VSQADD V3.H8, V4.H8, V4.H8
	VSQXTUN V4.H8, V4.B8
	FMOVS F4, (R0)
	RET
