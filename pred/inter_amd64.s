//go:build amd64

#include "textflag.h"

// func InterPred16x16Copy_ASM(dst *uint8, src *uint8, dstStride, srcStride int)
// Fast 16×16 block copy using SSE2.
TEXT ·InterPred16x16Copy_ASM(SB), NOSPLIT, $0-32
    MOVQ dst+0(FP), DI
    MOVQ src+8(FP), SI
    MOVQ dstStride+16(FP), R8
    MOVQ srcStride+24(FP), R9
    
    MOVL $16, CX
copy_loop:
    MOVOU (SI), X0
    MOVOU X0, (DI)
    ADDQ R8, DI
    ADDQ R9, SI
    DECL CX
    JNZ copy_loop
    RET
