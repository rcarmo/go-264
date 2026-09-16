//go:build arm64 && !purego

#include "textflag.h"

// Go's assembler does not name URHADD, vector MUL or SQXTUN. These encodings
// are derived from the Arm fields vendored in x/arch/arm64/arm64asm/inst.json.
#define URHADD_B8_V2_V0_V1  WORD $0x2e211402
#define URHADD_B16_V2_V0_V1 WORD $0x6e211402
#define MUL_S4_V4_V4_V12    WORD $0x4eac9c84
#define MUL_S4_V5_V5_V12    WORD $0x4eac9ca5
#define MUL_S4_V6_V6_V13    WORD $0x4ead9cc6
#define MUL_S4_V7_V7_V13    WORD $0x4ead9ce7
#define SQXTUN_H4_V8_V4     WORD $0x2e612888
#define SQXTUN2_H8_V8_V5    WORD $0x6e6128a8
#define SQXTUN_H4_V10_V6    WORD $0x2e6128ca
#define SQXTUN2_H8_V10_V7   WORD $0x6e6128ea
#define SQXTUN_B8_V9_V8     WORD $0x2e212909
#define SQXTUN_B8_V11_V10   WORD $0x2e21294b
#define SSHL_S4_V4_V4_V15   WORD $0x4eaf4484
#define SSHL_S4_V5_V5_V15   WORD $0x4eaf44a5

// func biBlendAvgNEON(dst, predL0, predL1 *byte, stride, w, h int)
TEXT ·biBlendAvgNEON(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0
	MOVD predL0+8(FP), R1
	MOVD predL1+16(FP), R2
	MOVD stride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
avg_row:
	CMP $16, R4
	BEQ avg_16
	CMP $8, R4
	BEQ avg_8
	MOVWU (R1), R6
	MOVWU (R2), R7
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VMOV R6, V0.S[0]
	VMOV R7, V1.S[0]
	URHADD_B8_V2_V0_V1
	VMOV V2.S[0], R6
	MOVW R6, (R0)
	B avg_next
avg_8:
	VLD1 (R1), [V0.B8]
	VLD1 (R2), [V1.B8]
	URHADD_B8_V2_V0_V1
	VST1 [V2.B8], (R0)
	B avg_next
avg_16:
	VLD1 (R1), [V0.B16]
	VLD1 (R2), [V1.B16]
	URHADD_B16_V2_V0_V1
	VST1 [V2.B16], (R0)
avg_next:
	ADD R3, R0
	ADD R3, R1
	ADD R3, R2
	SUBS $1, R5
	BNE avg_row
	RET

// func biBlendWeightedNEON(dst, predL0, predL1 *byte, stride, w, h, w0, w1, round, shift, offset int)
TEXT ·biBlendWeightedNEON(SB), NOSPLIT, $0-88
	MOVD dst+0(FP), R0
	MOVD predL0+8(FP), R1
	MOVD predL1+16(FP), R2
	MOVD stride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
	MOVD w0+48(FP), R6
	MOVD w1+56(FP), R7
	VDUP R6, V12.S4
	VDUP R7, V13.S4
	MOVD round+64(FP), R8
	VDUP R8, V14.S4
	MOVD shift+72(FP), R8
	NEG R8, R8
	VDUP R8, V15.S4
	MOVD offset+80(FP), R8
	VDUP R8, V16.S4
weighted_row:
	MOVD $0, R9
weighted_chunk:
	CMP $4, R4
	BEQ weighted_load4
	ADD R9, R1, R10
	ADD R9, R2, R11
	VLD1 (R10), [V0.B8]
	VLD1 (R11), [V1.B8]
	B weighted_loaded
weighted_load4:
	MOVWU (R1), R10
	MOVWU (R2), R11
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VMOV R10, V0.S[0]
	VMOV R11, V1.S[0]
weighted_loaded:
	VUXTL V0.B8, V0.H8
	VUXTL V1.B8, V1.H8
	VUXTL V0.H4, V4.S4
	VUXTL2 V0.H8, V5.S4
	VUXTL V1.H4, V6.S4
	VUXTL2 V1.H8, V7.S4
	MUL_S4_V4_V4_V12
	MUL_S4_V5_V5_V12
	MUL_S4_V6_V6_V13
	MUL_S4_V7_V7_V13
	VADD V6.S4, V4.S4, V4.S4
	VADD V7.S4, V5.S4, V5.S4
	VADD V14.S4, V4.S4, V4.S4
	VADD V14.S4, V5.S4, V5.S4
	// Negative signed shift counts perform exact arithmetic right shifts.
	SSHL_S4_V4_V4_V15
	SSHL_S4_V5_V5_V15
	VADD V16.S4, V4.S4, V4.S4
	VADD V16.S4, V5.S4, V5.S4
	SQXTUN_H4_V8_V4
	SQXTUN2_H8_V8_V5
	SQXTUN_B8_V9_V8
	CMP $4, R4
	BEQ weighted_store4
	ADD R9, R0, R10
	VST1 [V9.B8], (R10)
	ADD $8, R9
	CMP R4, R9
	BLT weighted_chunk
	B weighted_next
weighted_store4:
	VMOV V9.S[0], R10
	MOVW R10, (R0)
weighted_next:
	ADD R3, R0
	ADD R3, R1
	ADD R3, R2
	SUBS $1, R5
	BNE weighted_row
	RET
