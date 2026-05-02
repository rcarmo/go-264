//go:build amd64

#include "textflag.h"

// func IntraPred16x16DC_ASM(pred *uint8, dc uint8)
// Fill 256 bytes with the DC value using SSE2 (16 bytes per store).
TEXT ·IntraPred16x16DC_ASM(SB), NOSPLIT, $0-9
    MOVQ  pred+0(FP), DI
    MOVBLZX dc+8(FP), AX

    // Broadcast byte to all 16 lanes of XMM0
    MOVD  AX, X0
    PUNPCKLBW X0, X0    // 1→2 bytes
    PUNPCKLWL X0, X0    // 2→4 bytes
    PSHUFL $0, X0, X0   // 4→16 bytes

    // Store 16 rows × 16 bytes = 256 bytes
    MOVOU X0, 0(DI)
    MOVOU X0, 16(DI)
    MOVOU X0, 32(DI)
    MOVOU X0, 48(DI)
    MOVOU X0, 64(DI)
    MOVOU X0, 80(DI)
    MOVOU X0, 96(DI)
    MOVOU X0, 112(DI)
    MOVOU X0, 128(DI)
    MOVOU X0, 144(DI)
    MOVOU X0, 160(DI)
    MOVOU X0, 176(DI)
    MOVOU X0, 192(DI)
    MOVOU X0, 208(DI)
    MOVOU X0, 224(DI)
    MOVOU X0, 240(DI)
    RET

// func IntraPred16x16V_ASM(pred *uint8, top *uint8)
// Copy top row to all 16 rows.
TEXT ·IntraPred16x16V_ASM(SB), NOSPLIT, $0-16
    MOVQ  pred+0(FP), DI
    MOVQ  top+8(FP), SI
    MOVOU (SI), X0         // load 16-byte top row
    MOVOU X0, 0(DI)
    MOVOU X0, 16(DI)
    MOVOU X0, 32(DI)
    MOVOU X0, 48(DI)
    MOVOU X0, 64(DI)
    MOVOU X0, 80(DI)
    MOVOU X0, 96(DI)
    MOVOU X0, 112(DI)
    MOVOU X0, 128(DI)
    MOVOU X0, 144(DI)
    MOVOU X0, 160(DI)
    MOVOU X0, 176(DI)
    MOVOU X0, 192(DI)
    MOVOU X0, 208(DI)
    MOVOU X0, 224(DI)
    MOVOU X0, 240(DI)
    RET

// func IntraPred16x16H_ASM(pred *uint8, left *uint8)
// For each row, broadcast left[row] to 16 bytes.
TEXT ·IntraPred16x16H_ASM(SB), NOSPLIT, $0-16
    MOVQ  pred+0(FP), DI
    MOVQ  left+8(FP), SI

    XORL  CX, CX
hloop:
    CMPL  CX, $16
    JGE   hdone
    MOVBLZX (SI)(CX*1), AX
    MOVD  AX, X0
    PUNPCKLBW X0, X0
    PUNPCKLWL X0, X0
    PSHUFL $0, X0, X0
    MOVOU X0, (DI)
    ADDQ  $16, DI
    INCL  CX
    JMP   hloop
hdone:
    RET
