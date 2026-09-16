//go:build amd64 && !purego

#include "textflag.h"

// fftCPUHasAVX2 checks CPU and OS support before any YMM instruction executes:
// CPUID.1:ECX AVX+OSXSAVE, XGETBV XCR0 XMM+YMM, then CPUID.7:EBX AVX2.
TEXT ·fftCPUHasAVX2(SB), NOSPLIT, $0-1
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

// fftStageAVX2 processes two independent complex butterflies in the two 128-bit
// lanes of each YMM register. Arithmetic remains MUL/MUL/SUB/ADD then ADD/SUB;
// there is no FMA or horizontal reassociation. Callers use SSE2 for half < 2.
TEXT ·fftStageAVX2(SB), NOSPLIT, $0-40
	MOVQ x+0(FP), AX
	MOVQ roots+8(FP), BX
	MOVQ n+16(FP), CX
	MOVQ half+24(FP), DX
	MOVQ stride+32(FP), SI
	SHLQ $4, CX
	SHLQ $4, DX
	SHLQ $4, SI
	LEAQ (AX)(CX*1), R8
avx_block:
	CMPQ AX, R8
	JAE avx_done
	LEAQ (AX)(DX*1), R9
	MOVQ BX, R10
	MOVQ DX, R11
avx_pair:
	// Pack consecutive v/u values and their possibly strided twiddles.
	VMOVUPD (R9), X0
	VINSERTF128 $1, 16(R9), Y0, Y0
	VMOVUPD (R10), X1
	VINSERTF128 $1, (R10)(SI*1), Y1, Y1

	// Complex multiply in each 128-bit lane, matching the SSE2 sequence.
	VMULPD Y1, Y0, Y2
	VPERMILPD $5, Y1, Y1
	VMULPD Y1, Y0, Y0
	VPERMILPD $5, Y2, Y3
	VSUBPD Y3, Y2, Y2
	VPERMILPD $5, Y0, Y3
	VADDPD Y3, Y0, Y0
	VSHUFPD $0, Y0, Y2, Y2

	VMOVUPD (AX), X4
	VINSERTF128 $1, 16(AX), Y4, Y4
	VADDPD Y2, Y4, Y5
	VSUBPD Y2, Y4, Y4
	VMOVUPD X5, (AX)
	VEXTRACTF128 $1, Y5, 16(AX)
	VMOVUPD X4, (R9)
	VEXTRACTF128 $1, Y4, 16(R9)

	ADDQ $32, AX
	ADDQ $32, R9
	LEAQ (R10)(SI*2), R10
	SUBQ $32, R11
	JNZ avx_pair
	MOVQ R9, AX
	JMP avx_block
avx_done:
	VZEROUPPER
	RET
