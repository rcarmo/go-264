//go:build arm64 && !purego && go1.27

#include "textflag.h"

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
	// Add in signed halfwords, then clip to bytes. Only positive overflow is
	// possible because prediction is nonnegative; saturating that to 32767
	// preserves the eventual 255 clip even for the full int16 residual range.
	VUXTL V0.B8, V0.H8
	VSQADD V1.H8, V0.H8, V0.H8
	VSQXTUN V0.H8, V9.B8
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
