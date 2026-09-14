//go:build arm64 && !purego && go1.27

#include "textflag.h"

// The input vectors hold p2,p1,p0,q0,q1,q2 byte samples; V6/V7/V8 hold
// alpha/beta/tc0 in halfword lanes. The output bytes are p1(V1), p0(V15),
// q0(V16), q1(V4). Callers select four or eight active lanes.
//
// First mask out lanes failing any alpha/beta threshold. ap/aq masks are -1,
// so subtracting them adds one to tc for each eligible second neighbour.
// Clip the rounded delta to +/-tc, then independently correct p1/q1 using the
// ORIGINAL p0/q0 average and +/-tc0. Masked deltas preserve disabled pixels;
// signed-to-unsigned saturation is the final Clip1 operation.
#define FILTER_NORMAL_THRESHOLDS \
	VUXTL V0.B8, V0.H8; \
	VUXTL V1.B8, V1.H8; \
	VUXTL V2.B8, V2.H8; \
	VUXTL V3.B8, V3.H8; \
	VUXTL V4.B8, V4.H8; \
	VUXTL V5.B8, V5.H8; \
	VSUB V3.H8, V2.H8, V12.H8; \
	VABS V12.H8, V12.H8; \
	VCMGT V12.H8, V6.H8, V9.H8; \
	VSUB V2.H8, V1.H8, V12.H8; \
	VABS V12.H8, V12.H8; \
	VCMGT V12.H8, V7.H8, V13.H8; \
	VAND V13.B16, V9.B16, V9.B16; \
	VSUB V3.H8, V4.H8, V12.H8; \
	VABS V12.H8, V12.H8; \
	VCMGT V12.H8, V7.H8, V13.H8; \
	VAND V13.B16, V9.B16, V9.B16

// Update enabled lanes after the caller checks its four- or eight-lane mask.
#define FILTER_NORMAL_UPDATE \
	VSUB V2.H8, V0.H8, V12.H8; \
	VABS V12.H8, V12.H8; \
	VCMGT V12.H8, V7.H8, V10.H8; \
	VSUB V3.H8, V5.H8, V12.H8; \
	VABS V12.H8, V12.H8; \
	VCMGT V12.H8, V7.H8, V11.H8; \
	VSUB V10.H8, V8.H8, V12.H8; \
	VSUB V11.H8, V12.H8, V12.H8; \
	VEOR V13.B16, V13.B16, V13.B16; \
	VSUB V12.H8, V13.H8, V13.H8; \
	VSUB V2.H8, V3.H8, V14.H8; \
	VSHL $2, V14.H8, V14.H8; \
	VADD V1.H8, V14.H8, V14.H8; \
	VSUB V4.H8, V14.H8, V14.H8; \
	VSRSHR $3, V14.H8, V14.H8; \
	VSMIN V12.H8, V14.H8, V14.H8; \
	VSMAX V13.H8, V14.H8, V14.H8; \
	VAND V9.B16, V14.B16, V14.B16; \
	VADD V14.H8, V2.H8, V15.H8; \
	VSUB V14.H8, V3.H8, V16.H8; \
	VURHADD V2.H8, V3.H8, V12.H8; \
	VEOR V13.B16, V13.B16, V13.B16; \
	VSUB V8.H8, V13.H8, V13.H8; \
	VADD V12.H8, V0.H8, V14.H8; \
	VSHL $1, V1.H8, V17.H8; \
	VSUB V17.H8, V14.H8, V14.H8; \
	VSSHR $1, V14.H8, V14.H8; \
	VSMIN V8.H8, V14.H8, V14.H8; \
	VSMAX V13.H8, V14.H8, V14.H8; \
	VAND V10.B16, V9.B16, V17.B16; \
	VAND V17.B16, V14.B16, V14.B16; \
	VADD V14.H8, V1.H8, V1.H8; \
	VADD V12.H8, V5.H8, V14.H8; \
	VSHL $1, V4.H8, V17.H8; \
	VSUB V17.H8, V14.H8, V14.H8; \
	VSSHR $1, V14.H8, V14.H8; \
	VSMIN V8.H8, V14.H8, V14.H8; \
	VSMAX V13.H8, V14.H8, V14.H8; \
	VAND V11.B16, V9.B16, V17.B16; \
	VAND V17.B16, V14.B16, V14.B16; \
	VADD V14.H8, V4.H8, V4.H8; \
	VSQXTUN V1.H8, V1.B8; \
	VSQXTUN V15.H8, V15.B8; \
	VSQXTUN V16.H8, V16.B8; \
	VSQXTUN V4.H8, V4.B8

// Four independent columns of normal luma deblocking. Pixel arithmetic stays
// signed 16-bit (largest delta numerator is 1275); threshold masks retain the
// original pixels on disabled lanes. p2/q2 never change for normal filtering.
TEXT ·filterLumaNormalHNEON(SB), NOSPLIT, $0-40
	MOVD src+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD alpha+16(FP), R2
	VMOV R2, V6.H8
	MOVD beta+24(FP), R2
	VMOV R2, V7.H8
	MOVD tc0+32(FP), R2
	VMOV R2, V8.H8
	MOVD R0, R3
	FMOVS (R3), F0
	ADD R1, R3
	FMOVS (R3), F1
	ADD R1, R3
	FMOVS (R3), F2
	ADD R1, R3
	FMOVS (R3), F3
	ADD R1, R3
	FMOVS (R3), F4
	ADD R1, R3
	FMOVS (R3), F5
	FILTER_NORMAL_THRESHOLDS
	FMOVD F9, R2
	CBZ R2, normal_h_done
	FILTER_NORMAL_UPDATE
	ADD R1, R0
	FMOVS F1, (R0)
	ADD R1, R0
	FMOVS F15, (R0)
	ADD R1, R0
	FMOVS F16, (R0)
	ADD R1, R0
	FMOVS F4, (R0)
normal_h_done:
	RET

// Transpose four eight-byte source rows into four-lane pixel columns before
// the same normal filter, then transpose its four updated columns back. Reads
// include p3/q3, but stores touch only p1,p0,q0,q1 in each original row.
TEXT ·filterLumaNormalVNEON(SB), NOSPLIT, $0-40
	MOVD src+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD R0, R3
	FMOVD (R3), F0
	ADD R1, R3
	FMOVD (R3), F1
	ADD R1, R3
	FMOVD (R3), F2
	ADD R1, R3
	FMOVD (R3), F3
	VZIP1 V1.B8, V0.B8, V4.B8
	VZIP2 V1.B8, V0.B8, V5.B8
	VZIP1 V3.B8, V2.B8, V6.B8
	VZIP2 V3.B8, V2.B8, V7.B8
	VZIP1 V6.H4, V4.H4, V0.H4
	VZIP2 V6.H4, V4.H4, V1.H4
	VZIP1 V7.H4, V5.H4, V2.H4
	VZIP2 V7.H4, V5.H4, V3.H4
	VUSHR $32, V0.D2, V0.D2
	VUSHR $32, V1.D2, V18.D2
	VUSHR $32, V2.D2, V4.D2
	VMOV V3.B8, V5.B8
	VMOV V2.B8, V3.B8
	VMOV V18.B8, V2.B8
	MOVD alpha+16(FP), R2
	VMOV R2, V6.H8
	MOVD beta+24(FP), R2
	VMOV R2, V7.H8
	MOVD tc0+32(FP), R2
	VMOV R2, V8.H8
	FILTER_NORMAL_THRESHOLDS
	FMOVD F9, R2
	CBZ R2, normal_v_done
	FILTER_NORMAL_UPDATE
	VZIP1 V15.B8, V1.B8, V5.B8
	VZIP1 V4.B8, V16.B8, V6.B8
	VZIP1 V6.H4, V5.H4, V0.H4
	VZIP2 V6.H4, V5.H4, V1.H4
	VUSHR $32, V0.D2, V2.D2
	VUSHR $32, V1.D2, V3.D2
	ADD $2, R0
	FMOVS F0, (R0)
	ADD R1, R0
	FMOVS F2, (R0)
	ADD R1, R0
	FMOVS F1, (R0)
	ADD R1, R0
	FMOVS F3, (R0)
normal_v_done:
	RET

// Check all eight H lanes: the first four samples may fail the thresholds
// while the second group passes. Low64-only testing would lose that group.
#define LUMA_PAIR_ENABLED(done) \
	VEXT $8, V9.B16, V9.B16, V13.B16; \
	VORR V13.B16, V9.B16, V13.B16; \
	FMOVD F13, R2; \
	CBZ R2, done

// Six exact eight-byte row loads; four eight-byte updated rows are stored.
TEXT ·filterLumaPairHNEON(SB), NOSPLIT, $0-40
	MOVD src+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD R0, R3
	FMOVD (R3), F0
	ADD R1, R3
	FMOVD (R3), F1
	ADD R1, R3
	FMOVD (R3), F2
	ADD R1, R3
	FMOVD (R3), F3
	ADD R1, R3
	FMOVD (R3), F4
	ADD R1, R3
	FMOVD (R3), F5
	MOVD alpha+16(FP), R2
	VMOV R2, V6.H8
	MOVD beta+24(FP), R2
	VMOV R2, V7.H8
	MOVD limits+32(FP), R2
	FMOVD R2, F8
	VUXTL V8.B8, V8.H8
	FILTER_NORMAL_THRESHOLDS
	LUMA_PAIR_ENABLED(normal_pair_h_done)
	FILTER_NORMAL_UPDATE
	ADD R1, R0
	FMOVD F1, (R0)
	ADD R1, R0
	FMOVD F15, (R0)
	ADD R1, R0
	FMOVD F16, (R0)
	ADD R1, R0
	FMOVD F4, (R0)
normal_pair_h_done:
	RET

// Gather eight p3..q3 rows, then merge their four-row column pairs into the
// six eight-sample columns used by the shared filter. Only p1..q1 are written.
TEXT ·filterLumaPairVNEON(SB), NOSPLIT, $0-40
	MOVD src+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD R0, R3
	FMOVD (R3), F0
	ADD R1, R3
	FMOVD (R3), F1
	ADD R1, R3
	FMOVD (R3), F2
	ADD R1, R3
	FMOVD (R3), F3
	ADD R1, R3
	FMOVD (R3), F4
	ADD R1, R3
	FMOVD (R3), F5
	ADD R1, R3
	FMOVD (R3), F6
	ADD R1, R3
	FMOVD (R3), F7
	VZIP1 V1.B8, V0.B8, V16.B8
	VZIP2 V1.B8, V0.B8, V17.B8
	VZIP1 V3.B8, V2.B8, V18.B8
	VZIP2 V3.B8, V2.B8, V19.B8
	VZIP1 V18.H4, V16.H4, V8.H4
	VZIP2 V18.H4, V16.H4, V9.H4
	VZIP1 V19.H4, V17.H4, V10.H4
	VZIP2 V19.H4, V17.H4, V11.H4
	VZIP1 V5.B8, V4.B8, V16.B8
	VZIP2 V5.B8, V4.B8, V17.B8
	VZIP1 V7.B8, V6.B8, V18.B8
	VZIP2 V7.B8, V6.B8, V19.B8
	VZIP1 V18.H4, V16.H4, V12.H4
	VZIP2 V18.H4, V16.H4, V13.H4
	VZIP1 V19.H4, V17.H4, V14.H4
	VZIP2 V19.H4, V17.H4, V15.H4
	VZIP2 V12.S2, V8.S2, V0.S2
	VZIP1 V13.S2, V9.S2, V1.S2
	VZIP2 V13.S2, V9.S2, V2.S2
	VZIP1 V14.S2, V10.S2, V3.S2
	VZIP2 V14.S2, V10.S2, V4.S2
	VZIP1 V15.S2, V11.S2, V5.S2
	MOVD alpha+16(FP), R2
	VMOV R2, V6.H8
	MOVD beta+24(FP), R2
	VMOV R2, V7.H8
	MOVD limits+32(FP), R2
	FMOVD R2, F8
	VUXTL V8.B8, V8.H8
	FILTER_NORMAL_THRESHOLDS
	LUMA_PAIR_ENABLED(normal_pair_v_done)
	FILTER_NORMAL_UPDATE
	// Transpose four filtered columns into four pairs of original rows.
	VZIP1 V15.B8, V1.B8, V5.B8
	VZIP1 V4.B8, V16.B8, V6.B8
	VZIP1 V6.H4, V5.H4, V0.H4
	VZIP2 V6.H4, V5.H4, V2.H4
	VZIP2 V15.B8, V1.B8, V5.B8
	VZIP2 V4.B8, V16.B8, V6.B8
	VZIP1 V6.H4, V5.H4, V3.H4
	VZIP2 V6.H4, V5.H4, V7.H4
	ADD $2, R0
	FMOVD F0, R2
	MOVW R2, (R0)
	ADD R1, R0
	LSR $32, R2
	MOVW R2, (R0)
	ADD R1, R0
	FMOVD F2, R2
	MOVW R2, (R0)
	ADD R1, R0
	LSR $32, R2
	MOVW R2, (R0)
	ADD R1, R0
	FMOVD F3, R2
	MOVW R2, (R0)
	ADD R1, R0
	LSR $32, R2
	MOVW R2, (R0)
	ADD R1, R0
	FMOVD F7, R2
	MOVW R2, (R0)
	ADD R1, R0
	LSR $32, R2
	MOVW R2, (R0)
normal_pair_v_done:
	RET

// Eight independent chroma samples. V0..V3 contain p1,p0,q0,q1 bytes;
// V4/V5 are alpha/beta H8, V6 is per-sample strength, V7 is the normal tc.
// Strength 0 and 4 use tc=0, so the normal delta cannot change those samples.
// The separate strong correction applies only where strength=4 and all three
// threshold tests pass. All arithmetic fits signed16 (numerators <=1275).
// Outputs V1/V2 contain the filtered p0/q0 bytes; p1/q1 never change.
#define FILTER_CHROMA \
	VUXTL V0.B8, V0.H8; \
	VUXTL V1.B8, V1.H8; \
	VUXTL V2.B8, V2.H8; \
	VUXTL V3.B8, V3.H8; \
	VSUB V2.H8, V1.H8, V10.H8; \
	VABS V10.H8, V10.H8; \
	VCMGT V10.H8, V4.H8, V9.H8; \
	VSUB V1.H8, V0.H8, V10.H8; \
	VABS V10.H8, V10.H8; \
	VCMGT V10.H8, V5.H8, V11.H8; \
	VAND V11.B16, V9.B16, V9.B16; \
	VSUB V2.H8, V3.H8, V10.H8; \
	VABS V10.H8, V10.H8; \
	VCMGT V10.H8, V5.H8, V11.H8; \
	VAND V11.B16, V9.B16, V9.B16; \
	MOVD $4, R2; \
	VMOV R2, V12.H8; \
	VCMEQ V6.H8, V12.H8, V12.H8; \
	VAND V9.B16, V12.B16, V12.B16; \
	VEOR V8.B16, V8.B16, V8.B16; \
	VSUB V7.H8, V8.H8, V11.H8; \
	VSUB V1.H8, V2.H8, V10.H8; \
	VSHL $2, V10.H8, V10.H8; \
	VADD V0.H8, V10.H8, V10.H8; \
	VSUB V3.H8, V10.H8, V10.H8; \
	VSRSHR $3, V10.H8, V10.H8; \
	VSMIN V7.H8, V10.H8, V10.H8; \
	VSMAX V11.H8, V10.H8, V10.H8; \
	VAND V9.B16, V10.B16, V10.B16; \
	VADD V10.H8, V1.H8, V13.H8; \
	VSUB V10.H8, V2.H8, V14.H8; \
	VSHL $1, V0.H8, V15.H8; \
	VADD V1.H8, V15.H8, V15.H8; \
	VADD V3.H8, V15.H8, V15.H8; \
	VSRSHR $2, V15.H8, V15.H8; \
	VSUB V1.H8, V15.H8, V15.H8; \
	VAND V12.B16, V15.B16, V15.B16; \
	VADD V15.H8, V13.H8, V13.H8; \
	VSHL $1, V3.H8, V16.H8; \
	VADD V2.H8, V16.H8, V16.H8; \
	VADD V0.H8, V16.H8, V16.H8; \
	VSRSHR $2, V16.H8, V16.H8; \
	VSUB V2.H8, V16.H8, V16.H8; \
	VAND V12.B16, V16.B16, V16.B16; \
	VADD V16.H8, V14.H8, V14.H8; \
	VSQXTUN V13.H8, V1.B8; \
	VSQXTUN V14.H8, V2.B8

// Four exact eight-byte row loads; only p0 and q0 rows are written.
TEXT ·filterChromaHNEON(SB), NOSPLIT, $0-48
	MOVD src+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD R0, R3
	FMOVD (R3), F0
	ADD R1, R3
	FMOVD (R3), F1
	ADD R1, R3
	FMOVD (R3), F2
	ADD R1, R3
	FMOVD (R3), F3
	MOVD alpha+16(FP), R2
	VMOV R2, V4.H8
	MOVD beta+24(FP), R2
	VMOV R2, V5.H8
	MOVD strengths+32(FP), R2
	FMOVD R2, F6
	VUXTL V6.B8, V6.H8
	MOVD limits+40(FP), R2
	FMOVD R2, F7
	VUXTL V7.B8, V7.H8
	FILTER_CHROMA
	ADD R1, R0
	FMOVD F1, (R0)
	ADD R1, R0
	FMOVD F2, (R0)
	RET

// Eight exact four-byte row loads become the four eight-sample columns. The
// filtered p0/q0 columns are interleaved into two-byte stores for each row.
TEXT ·filterChromaVNEON(SB), NOSPLIT, $0-48
	MOVD src+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD R0, R3
	FMOVS (R3), F0
	ADD R1, R3
	FMOVS (R3), F1
	ADD R1, R3
	FMOVS (R3), F2
	ADD R1, R3
	FMOVS (R3), F3
	ADD R1, R3
	FMOVS (R3), F4
	ADD R1, R3
	FMOVS (R3), F5
	ADD R1, R3
	FMOVS (R3), F6
	ADD R1, R3
	FMOVS (R3), F7
	VZIP1 V1.B8, V0.B8, V8.B8
	VZIP1 V3.B8, V2.B8, V9.B8
	VZIP1 V9.H4, V8.H4, V0.H4
	VZIP2 V9.H4, V8.H4, V1.H4
	VZIP1 V5.B8, V4.B8, V8.B8
	VZIP1 V7.B8, V6.B8, V9.B8
	VZIP1 V9.H4, V8.H4, V2.H4
	VZIP2 V9.H4, V8.H4, V3.H4
	VZIP1 V2.S2, V0.S2, V4.S2
	VZIP2 V2.S2, V0.S2, V5.S2
	VZIP1 V3.S2, V1.S2, V6.S2
	VZIP2 V3.S2, V1.S2, V7.S2
	VMOV V4.B8, V0.B8
	VMOV V5.B8, V1.B8
	VMOV V6.B8, V2.B8
	VMOV V7.B8, V3.B8
	MOVD alpha+16(FP), R2
	VMOV R2, V4.H8
	MOVD beta+24(FP), R2
	VMOV R2, V5.H8
	MOVD strengths+32(FP), R2
	FMOVD R2, F6
	VUXTL V6.B8, V6.H8
	MOVD limits+40(FP), R2
	FMOVD R2, F7
	VUXTL V7.B8, V7.H8
	FILTER_CHROMA
	VZIP1 V2.B8, V1.B8, V0.B8
	VZIP2 V2.B8, V1.B8, V3.B8
	ADD $1, R0
	FMOVD F0, R2
	MOVH R2, (R0)
	ADD R1, R0
	LSR $16, R2
	MOVH R2, (R0)
	ADD R1, R0
	LSR $16, R2
	MOVH R2, (R0)
	ADD R1, R0
	LSR $16, R2
	MOVH R2, (R0)
	ADD R1, R0
	FMOVD F3, R2
	MOVH R2, (R0)
	ADD R1, R0
	LSR $16, R2
	MOVH R2, (R0)
	ADD R1, R0
	LSR $16, R2
	MOVH R2, (R0)
	ADD R1, R0
	LSR $16, R2
	MOVH R2, (R0)
	RET
