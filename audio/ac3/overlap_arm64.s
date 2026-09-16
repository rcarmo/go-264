//go:build arm64 && !purego

#include "textflag.h"

// Encodings are verified with go tool objdump:
// FADD V1.2D,V0.2D,V0.2D; FADD V0.2D,V0.2D,V0.2D.
#define FADD_V1_V0_V0 WORD $0x4e61d400
#define FADD_V0_V0_V0 WORD $0x4e60d400

TEXT ·overlapAddNEON(SB), NOSPLIT, $0-32
	MOVD output+0(FP), R0
	MOVD transformed+8(FP), R1
	MOVD previous+16(FP), R2
	MOVD n+24(FP), R3
loop:
	CMP $2, R3
	BLT tail
	VLD1 (R1), [V0.D2]
	VLD1 (R2), [V1.D2]
	FADD_V1_V0_V0
	FADD_V0_V0_V0
	VST1 [V0.D2], (R0)
	ADD $16, R1
	ADD $16, R2
	ADD $16, R0
	SUB $2, R3
	B loop
tail:
	CBZ R3, done
	FMOVD (R1), F0
	FMOVD (R2), F1
	FADDD F1, F0
	FADDD F0, F0
	FMOVD F0, (R0)
done:
	RET
