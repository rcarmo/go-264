//go:build arm64 && !purego

#include "textflag.h"

// A64 encodings used because Go's assembler does not expose vector float64
// multiply/add mnemonics. Registers are fixed below and verified by objdump.
// FMUL V1.2D,V0.2D,V0.2D = 0x6e61dc00
#define FMUL_V1_V0_V0 WORD $0x6e61dc00

// flags bit0 reverses weights. Addition remains scalar for exact ARM behavior.
TEXT ·windowNEON(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD weights+16(FP), R2
	MOVD n+24(FP), R3
	MOVD flags+32(FP), R4
	MOVD $16, R5
	TBZ $0, R4, window_loop
	ADD R3<<3, R2, R2
	SUB $8, R2
	MOVD $-16, R5
window_loop:
	CMP $2, R3
	BLT window_tail
	VLD1 (R1), [V0.D2]
	TBZ $0, R4, window_forward
	SUB $8, R2, R6
	VLD1 (R6), [V1.D2]
	VEXT $8, V1.B16, V1.B16, V1.B16
	B window_mul
window_forward:
	VLD1 (R2), [V1.D2]
window_mul:
	FMUL_V1_V0_V0
	VST1 [V0.D2], (R0)
	ADD $16, R1
	ADD $16, R0
	ADD R5, R2
	SUB $2, R3
	B window_loop
window_tail:
	CBZ R3, window_done
	FMOVD (R1), F0
	FMOVD (R2), F1
	FMULD F1, F0
	TBZ $1, R4, window_tail_store
	FMOVD (R0), F2
	FADDD F0, F2, F0
window_tail_store:
	FMOVD F0, (R0)
window_done:
	RET

