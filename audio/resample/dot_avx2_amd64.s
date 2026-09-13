//go:build amd64 && !purego

#include "textflag.h"

TEXT ·dotCPUHasAVX2(SB), NOSPLIT, $0-1
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

// Four products per YMM multiply, extracted and accumulated lane 0,1,2,3.
// No FMA, horizontal sum, alternate accumulator or reassociation is used.
TEXT ·dotAVX2(SB), NOSPLIT, $0-32
 MOVQ a+0(FP), AX
 MOVQ b+8(FP), BX
 MOVQ n+16(FP), CX
 VXORPD X0, X0, X0
quad:
 CMPQ CX, $4
 JL pair
 VMOVUPD (AX), Y1
 VMULPD (BX), Y1, Y1
 VEXTRACTF128 $1, Y1, X2
 VADDSD X1, X0, X0
 VPERMILPD $1, X1, X1
 VADDSD X1, X0, X0
 VADDSD X2, X0, X0
 VPERMILPD $1, X2, X2
 VADDSD X2, X0, X0
 ADDQ $32, AX
 ADDQ $32, BX
 SUBQ $4, CX
 JMP quad
pair:
 CMPQ CX, $2
 JL tail
 VMOVUPD (AX), X1
 VMULPD (BX), X1, X1
 VADDSD X1, X0, X0
 VPERMILPD $1, X1, X1
 VADDSD X1, X0, X0
 ADDQ $16, AX
 ADDQ $16, BX
 SUBQ $2, CX
tail:
 TESTQ CX, CX
 JE done
 VMOVSD (AX), X1
 VMULSD (BX), X1, X1
 VADDSD X1, X0, X0
done:
 VMOVSD X0, ret+24(FP)
 VZEROUPPER
 RET
