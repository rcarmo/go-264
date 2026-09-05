//go:build arm64 && !purego && go1.27

#include "textflag.h"

// Four nonnegative bilinear weights sum to 64. Widen byte products to uint16:
// the accumulated sum is at most 255*64=16320, so rounding by 32 and shifting
// by 6 fits both signed 16-bit arithmetic and the resulting byte samples.
TEXT ·chromaBilinearNEON(SB), NOSPLIT, $0-72
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD stride+16(FP), R2
	MOVD w+24(FP), R3
	MOVD h+32(FP), R4
	MOVD wa+40(FP), R5
	VMOV R5, V4.B8
	MOVD wb+48(FP), R5
	VMOV R5, V5.B8
	MOVD wc+56(FP), R5
	VMOV R5, V6.B8
	MOVD wd+64(FP), R5
	VMOV R5, V7.B8
chroma_row:
	ADD R2, R1, R6
	CMP $8, R3
	BEQ chroma_load8
	CMP $4, R3
	BEQ chroma_load4
	MOVHU (R1), R5
	FMOVD R5, F0
	MOVHU 1(R1), R5
	FMOVD R5, F1
	MOVHU (R6), R5
	FMOVD R5, F2
	MOVHU 1(R6), R5
	FMOVD R5, F3
	B chroma_filter
chroma_load8:
	FMOVD (R1), F0
	FMOVD 1(R1), F1
	FMOVD (R6), F2
	FMOVD 1(R6), F3
	B chroma_filter
chroma_load4:
	FMOVS (R1), F0
	FMOVS 1(R1), F1
	FMOVS (R6), F2
	FMOVS 1(R6), F3
chroma_filter:
	VUMULL V4.B8, V0.B8, V0.H8
	VUMLAL V5.B8, V1.B8, V0.H8
	VUMLAL V6.B8, V2.B8, V0.H8
	VUMLAL V7.B8, V3.B8, V0.H8
	VSRSHR $6, V0.H8, V0.H8
	VUQXTN V0.H8, V0.B8
	CMP $8, R3
	BEQ chroma_store8
	CMP $4, R3
	BEQ chroma_store4
	FMOVD F0, R5
	MOVH R5, (R0)
	B chroma_next
chroma_store8:
	FMOVD F0, (R0)
	B chroma_next
chroma_store4:
	FMOVS F0, (R0)
chroma_next:
	ADD $8, R0
	ADD R2, R1
	SUBS $1, R4
	BNE chroma_row
	RET
