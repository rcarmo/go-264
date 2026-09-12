//go:build arm64 && !purego

#include "textflag.h"

// Go's ARM64 assembler does not expose vector float64 add/sub mnemonics.
// Fixed-register encodings below are verified by go tool objdump.
#define FADD_V2_V4_V4 WORD $0x4e62d484
#define FSUB_V2_V5_V5 WORD $0x4ee2d4a5

// fftStageNEON mirrors the compiler's scalar complex-multiply sequence exactly
// (FMUL/FMSUB, FMUL/FMADD), then uses NEON for packed u+v and u-v.
TEXT ·fftStageNEON(SB), NOSPLIT, $16-40
	MOVD x+0(FP), R0
	MOVD roots+8(FP), R1
	MOVD n+16(FP), R2
	MOVD half+24(FP), R3
	MOVD stride+32(FP), R4
	LSL $4, R2, R2
	LSL $4, R3, R3
	LSL $4, R4, R4
	ADD R2, R0, R5
fft_block:
	CMP R5, R0
	BHS fft_done
	ADD R3, R0, R6
	MOVD R1, R7
	MOVD R3, R8
fft_butterfly:
	FMOVD (R6), F2
	FMOVD 8(R6), F3
	FMOVD (R7), F4
	FMOVD 8(R7), F5
	FMULD F4, F2, F6
	FMSUBD F3, F6, F5, F6
	FMULD F5, F2, F2
	FMADDD F3, F2, F4, F2
	FMOVD F6, 0(RSP)
	FMOVD F2, 8(RSP)
	VLD1 (RSP), [V2.D2]
	VLD1 (R0), [V4.D2]
	VMOV V4.B16, V5.B16
	FADD_V2_V4_V4
	FSUB_V2_V5_V5
	VST1 [V4.D2], (R0)
	VST1 [V5.D2], (R6)
	ADD $16, R0
	ADD $16, R6
	ADD R4, R7
	SUB $16, R8
	CBNZ R8, fft_butterfly
	MOVD R6, R0
	B fft_block
fft_done:
	RET
