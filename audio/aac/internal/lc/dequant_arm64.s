//go:build arm64 && !purego

#include "textflag.h"

// FMUL V7.2D,V0.2D,V0.2D; Go assembler has no vector-float mnemonic.
#define FMUL_V7_V0_V0 WORD $0x6e67dc00

// func dequantNEON(dst *float64, quant *int32, tableZero *float64, n int, scale float64)
// Validated signed table indices are gathered scalarly; only independent scale
// products are packed. Separate FMUL preserves the reference operation.
TEXT ·dequantNEON(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD quant+8(FP), R1
	MOVD tableZero+16(FP), R2
	MOVD n+24(FP), R3
	FMOVD scale+32(FP), F2
	FMOVD F2, R5
	VDUP R5, V7.D2
loop:
	CMP $2, R3
	BLT tail
	MOVW (R1), R4
	SXTW R4, R4
	FMOVD (R2)(R4<<3), F0
	MOVW 4(R1), R4
	SXTW R4, R4
	FMOVD (R2)(R4<<3), F1
	VMOV V1.D[0], V0.D[1]
	FMUL_V7_V0_V0
	VST1 [V0.D2], (R0)
	ADD $8, R1
	ADD $16, R0
	SUB $2, R3
	B loop
tail:
	CBZ R3, done
	MOVW (R1), R4
	SXTW R4, R4
	FMOVD (R2)(R4<<3), F0
	FMULD F2, F0
	FMOVD F0, (R0)
done:
	RET
