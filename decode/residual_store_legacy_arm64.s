//go:build arm64 && !purego && !go1.27

#include "textflag.h"

// Before Go 1.27, the assembler does not name SQXTUN/SQXTUN2. Encodings use the
// Arm fields vendored in x/arch/arm64/arm64asm/inst.json.
#define SQXTUN_H4_V8_V4  WORD $0x2e612888
#define SQXTUN2_H8_V8_V5 WORD $0x6e6128a8
#define UQXTN_B8_V9_V8   WORD $0x2e214909

// func residualAddStoreNEON(dst *byte, dstStride int, predicted *byte, predStride int, residual *int16, residualStride, w, h int)
TEXT ·residualAddStoreNEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD predicted+16(FP), R2
	MOVD predStride+24(FP), R3
	MOVD residual+32(FP), R4
	MOVD residualStride+40(FP), R5
	LSL $1, R5, R5
	MOVD w+48(FP), R6
	MOVD h+56(FP), R7
row:
	CMP $4, R6
	BEQ load4
	VLD1 (R2), [V0.B8]
	VLD1 (R4), [V1.H8]
	B loaded
load4:
	MOVWU (R2), R8
	VEOR V0.B16, V0.B16, V0.B16
	VMOV R8, V0.S[0]
	VLD1 (R4), [V1.H4]
loaded:
	// Prediction bytes -> unsigned dwords.
	VUXTL V0.B8, V0.H8
	VUXTL V0.H4, V8.S4
	VUXTL2 V0.H8, V9.S4
	// Residual signed words -> signed dwords.
	VUSHR $15, V1.H8, V2.H8
	VUXTL V1.H4, V4.S4
	VUXTL2 V1.H8, V5.S4
	VUXTL V2.H4, V6.S4
	VUXTL2 V2.H8, V7.S4
	VSHL $16, V6.S4, V6.S4
	VSHL $16, V7.S4, V7.S4
	VSUB V6.S4, V4.S4, V4.S4
	VSUB V7.S4, V5.S4, V5.S4
	VADD V8.S4, V4.S4, V4.S4
	VADD V9.S4, V5.S4, V5.S4
	SQXTUN_H4_V8_V4
	SQXTUN2_H8_V8_V5
	UQXTN_B8_V9_V8
	CMP $4, R6
	BEQ store4
	VST1 [V9.B8], (R0)
	B next
store4:
	VMOV V9.S[0], R8
	MOVW R8, (R0)
next:
	ADD R1, R0
	ADD R3, R2
	ADD R5, R4
	SUBS $1, R7
	BNE row
	RET
