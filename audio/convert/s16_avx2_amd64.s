//go:build amd64 && !purego

#include "textflag.h"

DATA ·scaleS16AVX2+0(SB)/8, $32768.0
DATA ·scaleS16AVX2+8(SB)/8, $32768.0
DATA ·scaleS16AVX2+16(SB)/8, $32768.0
DATA ·scaleS16AVX2+24(SB)/8, $32768.0
GLOBL ·scaleS16AVX2(SB), RODATA|NOPTR, $32
DATA ·halfAVX2+0(SB)/8, $0.5
DATA ·halfAVX2+8(SB)/8, $0.5
DATA ·halfAVX2+16(SB)/8, $0.5
DATA ·halfAVX2+24(SB)/8, $0.5
GLOBL ·halfAVX2(SB), RODATA|NOPTR, $32
DATA ·maxS16AVX2+0(SB)/8, $32767.0
DATA ·maxS16AVX2+8(SB)/8, $32767.0
DATA ·maxS16AVX2+16(SB)/8, $32767.0
DATA ·maxS16AVX2+24(SB)/8, $32767.0
GLOBL ·maxS16AVX2(SB), RODATA|NOPTR, $32
DATA ·absMaskAVX2+0(SB)/8, $0x7fffffffffffffff
DATA ·absMaskAVX2+8(SB)/8, $0x7fffffffffffffff
DATA ·absMaskAVX2+16(SB)/8, $0x7fffffffffffffff
DATA ·absMaskAVX2+24(SB)/8, $0x7fffffffffffffff
GLOBL ·absMaskAVX2(SB), RODATA|NOPTR, $32

// Complete AVX2/OS support check: maximum CPUID leaf, AVX+OSXSAVE,
// XCR0 XMM/YMM state, then AVX2.
TEXT ·s16CPUHasAVX2(SB), NOSPLIT, $0-1
	XORL AX, AX
	CPUID
	CMPL AX, $7
	JB no_avx2
	MOVL $1, AX
	CPUID
	MOVL CX, R8
	ANDL $0x18000000, R8
	CMPL R8, $0x18000000
	JNE no_avx2
	XORL CX, CX
	XGETBV
	ANDL $6, AX
	CMPL AX, $6
	JNE no_avx2
	MOVL $7, AX
	XORL CX, CX
	CPUID
	BTL $5, BX
	JNC no_avx2
	MOVB $1, ret+0(FP)
	RET
no_avx2:
	MOVB $0, ret+0(FP)
	RET

// s16AVX2 handles n divisible by four. It is the SSE2 algorithm lane-for-lane:
// scale, clip, abs/truncate, exact residual >= .5 increment, restore sign, pack.
TEXT ·s16AVX2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	VMOVUPD ·scaleS16AVX2(SB), Y7
	VMOVUPD ·absMaskAVX2(SB), Y6
	VMOVUPD ·halfAVX2(SB), Y5
	VMOVUPD ·maxS16AVX2(SB), Y4
	VXORPD Y3, Y3, Y3
s16_avx_loop:
	VMOVUPD (SI), Y0
	VMULPD Y7, Y0, Y0
	VSUBPD Y7, Y3, Y1
	VMAXPD Y1, Y0, Y0
	VMINPD Y4, Y0, Y0
	VCMPPD $1, Y3, Y0, Y8
	VANDPD Y6, Y0, Y0
	VCVTTPD2DQY Y0, X1
	VCVTDQ2PD X1, Y2
	VSUBPD Y2, Y0, Y0
	VCMPPD $2, Y0, Y5, Y2
	VEXTRACTI128 $1, Y2, X10
	VPACKSSDW X10, X2, X2
	VPSUBD X2, X1, X1
	VEXTRACTI128 $1, Y8, X9
	VPACKSSDW X9, X8, X8
	VPXOR X8, X1, X1
	VPSUBD X8, X1, X1
	VPACKSSDW X1, X1, X1
	VMOVQ X1, (DI)
	ADDQ $32, SI
	ADDQ $8, DI
	SUBQ $4, CX
	JNZ s16_avx_loop
	VZEROUPPER
	RET
