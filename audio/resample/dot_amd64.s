//go:build amd64 && !purego

#include "textflag.h"

// Four products are issued as two independent vectors, then accumulated one
// scalar lane at a time in original index order. No FMA, horizontal reduction,
// alternate accumulator or reassociation changes the scalar oracle.
TEXT ·dotSSE2(SB),NOSPLIT,$0-32
 MOVQ a+0(FP), AX
 MOVQ b+8(FP), BX
 MOVQ n+16(FP), CX
 PXOR X0, X0
quad:
 CMPQ CX, $4
 JL pair
 MOVUPD (AX), X1
 MOVUPD (BX), X2
 MULPD X2, X1
 MOVUPD 16(AX), X3
 MOVUPD 16(BX), X4
 MULPD X4, X3
 ADDSD X1, X0
 UNPCKHPD X1, X1
 ADDSD X1, X0
 ADDSD X3, X0
 UNPCKHPD X3, X3
 ADDSD X3, X0
 ADDQ $32, AX
 ADDQ $32, BX
 SUBQ $4, CX
 JMP quad
pair:
 CMPQ CX, $2
 JL tail
 MOVUPD (AX), X1
 MOVUPD (BX), X2
 MULPD X2, X1
 ADDSD X1, X0
 UNPCKHPD X1, X1
 ADDSD X1, X0
 ADDQ $16, AX
 ADDQ $16, BX
 SUBQ $2, CX
tail:
 TESTQ CX, CX
 JE done
 MOVSD (AX), X1
 MULSD (BX), X1
 ADDSD X1, X0
done:
 MOVSD X0, ret+24(FP)
 RET
