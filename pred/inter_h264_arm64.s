//go:build arm64 && !purego && go1.27

#include "textflag.h"

// Sum [1,-5,20,20,-5,1] without multiplication:
// (a+f) + 5*(4*(c+d) - (b+e)). Byte input fits signed 16-bit throughout.
// V0..V5 contain eight unsigned bytes; V0 returns eight signed raw sums.
#define LUMA_TAP_BYTES \
	VUXTL V0.B8, V0.H8; \
	VUXTL V1.B8, V1.H8; \
	VUXTL V2.B8, V2.H8; \
	VUXTL V3.B8, V3.H8; \
	VUXTL V4.B8, V4.H8; \
	VUXTL V5.B8, V5.H8; \
	VADD V5.H8, V0.H8, V0.H8; \
	VADD V4.H8, V1.H8, V1.H8; \
	VADD V3.H8, V2.H8, V2.H8; \
	VSHL $2, V2.H8, V2.H8; \
	VSUB V1.H8, V2.H8, V2.H8; \
	VSHL $2, V2.H8, V3.H8; \
	VADD V3.H8, V2.H8, V2.H8; \
	VADD V2.H8, V0.H8, V0.H8

// lumaHalfNEON filters horizontal or vertical byte samples. src points to the
// first tap (two samples before the integer position); tapStep is 1 or stride.
// fraction=2 writes the clipped half sample; 1/3 average it with its integer
// neighbor using unsigned rounding. Width is 4, 8 or 16; h is positive.
TEXT ·lumaHalfNEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD dstStride+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
	MOVD tapStep+48(FP), R6
	MOVD fraction+56(FP), R7
	LSR $1, R7, R13
	ADD $2, R13
	MUL R6, R13, R13
	MOVD $8, R8
	CMP $4, R4
	BNE half_row
	MOVD $4, R8
half_row:
	MOVD R4, R9
	MOVD R1, R11
	MOVD R0, R12
half_chunk:
	MOVD R11, R10
	CMP $4, R8
	BEQ half_load4
	FMOVD (R10), F0
	ADD R6, R10
	FMOVD (R10), F1
	ADD R6, R10
	FMOVD (R10), F2
	ADD R6, R10
	FMOVD (R10), F3
	ADD R6, R10
	FMOVD (R10), F4
	ADD R6, R10
	FMOVD (R10), F5
	B half_filter
half_load4:
	FMOVS (R10), F0
	ADD R6, R10
	FMOVS (R10), F1
	ADD R6, R10
	FMOVS (R10), F2
	ADD R6, R10
	FMOVS (R10), F3
	ADD R6, R10
	FMOVS (R10), F4
	ADD R6, R10
	FMOVS (R10), F5
half_filter:
	LUMA_TAP_BYTES
	VSRSHR $5, V0.H8, V0.H8
	VSQXTUN V0.H8, V0.B8
	CMP $2, R7
	BEQ half_store
	ADD R13, R11, R10
	CMP $4, R8
	BEQ half_integer4
	FMOVD (R10), F1
	B half_average
half_integer4:
	FMOVS (R10), F1
half_average:
	VURHADD V1.B8, V0.B8, V0.B8
half_store:
	CMP $4, R8
	BEQ half_store4
	FMOVD F0, (R12)
	B half_next
half_store4:
	FMOVS F0, (R12)
half_next:
	ADD R8, R11
	ADD R8, R12
	SUBS R8, R9
	BNE half_chunk
	ADD R2, R0
	ADD R3, R1
	SUBS $1, R5
	BNE half_row
	RET

// lumaHorizontalRawNEON stores unrounded horizontal sums as int16. dstStride
// is in bytes; the caller provides h+5 rows for the following vertical pass.
TEXT ·lumaHorizontalRawNEON(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD dstStride+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
	MOVD $8, R8
	CMP $4, R4
	BNE rawh_row
	MOVD $4, R8
rawh_row:
	MOVD R4, R9
	MOVD R1, R11
	MOVD R0, R12
rawh_chunk:
	CMP $4, R8
	BEQ rawh_load4
	FMOVD (R11), F0
	FMOVD 1(R11), F1
	FMOVD 2(R11), F2
	FMOVD 3(R11), F3
	FMOVD 4(R11), F4
	FMOVD 5(R11), F5
	B rawh_filter
rawh_load4:
	FMOVS (R11), F0
	FMOVS 1(R11), F1
	FMOVS 2(R11), F2
	FMOVS 3(R11), F3
	FMOVS 4(R11), F4
	FMOVS 5(R11), F5
rawh_filter:
	LUMA_TAP_BYTES
	CMP $4, R8
	BEQ rawh_store4
	VST1 [V0.H8], (R12)
	B rawh_next
rawh_store4:
	FMOVD F0, (R12)
rawh_next:
	ADD R8, R11
	ADD R8<<1, R12
	SUBS R8, R9
	BNE rawh_chunk
	ADD R2, R0
	ADD R3, R1
	SUBS $1, R5
	BNE rawh_row
	RET

// lumaVerticalRawNEON applies the second six-tap pass to four signed int16
// samples per vector. Widen before arithmetic: raw sums fit int16, but their
// second-pass weighted sum requires int32. Signed rounding by 10 then unsigned
// saturation implements Clip1Y((sum+512)>>10), with no intermediate clipping.
TEXT ·lumaVerticalRawNEON(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD dstStride+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
rawv_row:
	MOVD R4, R9
	MOVD R1, R11
	MOVD R0, R12
rawv_chunk:
	MOVD R11, R10
	FMOVD (R10), F0
	ADD R3, R10
	FMOVD (R10), F1
	ADD R3, R10
	FMOVD (R10), F2
	ADD R3, R10
	FMOVD (R10), F3
	ADD R3, R10
	FMOVD (R10), F4
	ADD R3, R10
	FMOVD (R10), F5
	VSXTL V0.H4, V0.S4
	VSXTL V1.H4, V1.S4
	VSXTL V2.H4, V2.S4
	VSXTL V3.H4, V3.S4
	VSXTL V4.H4, V4.S4
	VSXTL V5.H4, V5.S4
	VADD V5.S4, V0.S4, V0.S4
	VADD V4.S4, V1.S4, V1.S4
	VADD V3.S4, V2.S4, V2.S4
	VSHL $2, V2.S4, V2.S4
	VSUB V1.S4, V2.S4, V2.S4
	VSHL $2, V2.S4, V3.S4
	VADD V3.S4, V2.S4, V2.S4
	VADD V2.S4, V0.S4, V0.S4
	VSRSHR $10, V0.S4, V0.S4
	VSQXTUN V0.S4, V0.H4
	VUQXTN V0.H8, V0.B8
	FMOVS F0, (R12)
	ADD $8, R11
	ADD $4, R12
	SUBS $4, R9
	BNE rawv_chunk
	ADD R2, R0
	ADD R3, R1
	SUBS $1, R5
	BNE rawv_row
	RET

// lumaAverageNEON computes (dst+src+1)>>1 on already clipped predictions.
// Narrow rows never read or write bytes outside the requested rectangle.
TEXT ·lumaAverageNEON(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD dstStride+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
	MOVD $8, R8
	CMP $4, R4
	BNE average_row
	MOVD $4, R8
average_row:
	MOVD R4, R9
	MOVD R1, R11
	MOVD R0, R12
average_chunk:
	CMP $4, R8
	BEQ average4
	FMOVD (R11), F1
	FMOVD (R12), F0
	VURHADD V1.B8, V0.B8, V0.B8
	FMOVD F0, (R12)
	B average_next
average4:
	FMOVS (R11), F1
	FMOVS (R12), F0
	VURHADD V1.B8, V0.B8, V0.B8
	FMOVS F0, (R12)
average_next:
	ADD R8, R11
	ADD R8, R12
	SUBS R8, R9
	BNE average_chunk
	ADD R2, R0
	ADD R3, R1
	SUBS $1, R5
	BNE average_row
	RET
