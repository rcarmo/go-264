//go:build amd64 && !purego

#include "textflag.h"

// Two products per vector, accumulated in original index order. Unaligned
// loads and scalar odd tail are safe; no read past n and no stored pointers.
TEXT ·dotSSE2(SB),NOSPLIT,$0-32
 MOVQ a+0(FP), AX
 MOVQ b+8(FP), BX
 MOVQ n+16(FP), CX
 PXOR X0, X0
loop:
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
 JMP loop
tail:
 TESTQ CX, CX
 JE done
 MOVSD (AX), X1
 MULSD (BX), X1
 ADDSD X1, X0
done:
 MOVSD X0, ret+24(FP)
 RET
