//go:build arm64 && !purego

#include "textflag.h"

// func monoToStereoNEON(dst, src *float64, frames int)
// Duplicate each 64-bit sample; this is a bit-copy, not FP arithmetic.
TEXT ·monoToStereoNEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD frames+16(FP), R2
mts_pairs:
	CMP $2, R2
	BLT mts_tail
	VLD1 (R1), [V0.D2]
	VZIP1 V0.D2, V0.D2, V1.D2
	VZIP2 V0.D2, V0.D2, V2.D2
	VST1 [V1.D2, V2.D2], (R0)
	ADD $16, R1
	ADD $32, R0
	SUB $2, R2
	B mts_pairs
mts_tail:
	CBZ R2, mts_done
	MOVD (R1), R3
	MOVD R3, (R0)
	MOVD R3, 8(R0)
mts_done:
	RET

// func interleaveStereoNEON(dst, left, right *float64, frames int)
// Zip two two-sample vectors into L0,R0,L1,R1.
TEXT ·interleaveStereoNEON(SB), NOSPLIT, $0-32
	MOVD dst+0(FP), R0
	MOVD left+8(FP), R1
	MOVD right+16(FP), R2
	MOVD frames+24(FP), R3
is_pairs:
	CMP $2, R3
	BLT is_tail
	VLD1 (R1), [V0.D2]
	VLD1 (R2), [V1.D2]
	VZIP1 V1.D2, V0.D2, V2.D2
	VZIP2 V1.D2, V0.D2, V3.D2
	VST1 [V2.D2, V3.D2], (R0)
	ADD $16, R1
	ADD $16, R2
	ADD $32, R0
	SUB $2, R3
	B is_pairs
is_tail:
	CBZ R3, is_done
	MOVD (R1), R4
	MOVD (R2), R5
	MOVD R4, (R0)
	MOVD R5, 8(R0)
is_done:
	RET
