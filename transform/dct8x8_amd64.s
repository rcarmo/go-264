//go:build amd64

#include "textflag.h"

// 8×8 IDCT using scalar registers (unrolled for speed).
// The 8×8 butterfly has more operations but the same structure as 4×4.
// Each row/column: 4 even terms + 4 odd terms → 8 outputs.

// Helper macro: load row from block[off..off+7] into AX..R15
// block is at DI, off is byte offset
#define LOAD8(off) \
    MOVWLSX off+0(DI), AX; \
    MOVWLSX off+2(DI), BX; \
    MOVWLSX off+4(DI), CX; \
    MOVWLSX off+6(DI), DX; \
    MOVWLSX off+8(DI), R8; \
    MOVWLSX off+10(DI), R9; \
    MOVWLSX off+12(DI), R10; \
    MOVWLSX off+14(DI), R11

// func IDCT8x8_ASM(block *int16)
// Process horizontally (8 rows), then vertically (8 columns).
// Each direction applies the H.264 8×8 inverse butterfly.
TEXT ·IDCT8x8_ASM(SB), NOSPLIT, $64-8
    MOVQ block+0(FP), DI

    // === Horizontal pass: 8 rows ===
    XORL SI, SI   // row counter
hloop8:
    CMPL SI, $8
    JGE  hloop8_done
    
    // Row offset = SI * 16 bytes (8 int16)
    MOVL SI, R14
    SHLL $4, R14
    
    // Load 8 values
    LEAQ (DI)(R14*1), R15
    MOVWLSX 0(R15), AX     // r[0]
    MOVWLSX 2(R15), BX     // r[1]
    MOVWLSX 4(R15), CX     // r[2]
    MOVWLSX 6(R15), DX     // r[3]
    MOVWLSX 8(R15), R8     // r[4]
    MOVWLSX 10(R15), R9    // r[5]
    MOVWLSX 12(R15), R10   // r[6]
    MOVWLSX 14(R15), R11   // r[7]

    // Even part: a0=r0+r4, a2=r0-r4, a4=(r2>>1)-r6, a6=r2+(r6>>1)
    // Store intermediates on stack
    MOVL AX, R12; ADDL R8, R12    // a0 = r0+r4
    MOVL AX, R13; SUBL R8, R13    // a2 = r0-r4
    MOVL R12, 0(SP)               // save a0
    MOVL R13, 4(SP)               // save a2
    
    MOVL CX, R12; SARL $1, R12; SUBL R10, R12  // a4 = (r2>>1)-r6
    MOVL R10, R13; SARL $1, R13; ADDL CX, R13  // a6 = r2+(r6>>1)
    MOVL R12, 8(SP)               // save a4
    MOVL R13, 12(SP)              // save a6

    // b0=a0+a6, b2=a2+a4, b4=a2-a4, b6=a0-a6
    MOVL 0(SP), AX; ADDL 12(SP), AX   // b0
    MOVL 4(SP), CX; ADDL 8(SP), CX    // b2
    MOVL 4(SP), R8; SUBL 8(SP), R8    // b4
    MOVL 0(SP), R12; SUBL 12(SP), R12 // b6
    MOVL AX, 16(SP)  // b0
    MOVL CX, 20(SP)  // b2
    MOVL R8, 24(SP)  // b4
    MOVL R12, 28(SP) // b6

    // Odd part: use original r1,r3,r5,r7 (still in BX,DX,R9,R11)
    // a1 = -r3+r5-r7-(r7>>1)
    MOVL R11, AX; SARL $1, AX; ADDL R11, AX; NEGL AX  // -(r7+(r7>>1))
    ADDL R9, AX; SUBL DX, AX   // +r5-r3
    MOVL AX, 32(SP)  // a1

    // a3 = r1+r7-r3-(r3>>1)
    MOVL DX, AX; SARL $1, AX; ADDL DX, AX; NEGL AX  // -(r3+(r3>>1))
    ADDL BX, AX; ADDL R11, AX  // +r1+r7
    MOVL AX, 36(SP)  // a3

    // a5 = -r1+r7+r5+(r5>>1)
    MOVL R9, AX; SARL $1, AX; ADDL R9, AX  // r5+(r5>>1)
    ADDL R11, AX; SUBL BX, AX  // +r7-r1
    MOVL AX, 40(SP)  // a5

    // a7 = r3+r5+r1+(r1>>1)
    MOVL BX, AX; SARL $1, AX; ADDL BX, AX  // r1+(r1>>1)
    ADDL DX, AX; ADDL R9, AX  // +r3+r5
    MOVL AX, 44(SP)  // a7

    // b1=(a7>>2)+a1, b3=a3+(a5>>2), b5=(a3>>2)-a5, b7=a7-(a1>>2)
    MOVL 44(SP), AX; SARL $2, AX; ADDL 32(SP), AX  // b1
    MOVL 40(SP), CX; SARL $2, CX; ADDL 36(SP), CX  // b3
    MOVL 36(SP), R8; SARL $2, R8; SUBL 40(SP), R8   // b5
    MOVL 32(SP), R12; SARL $2, R12; MOVL 44(SP), R13; SUBL R12, R13  // b7

    // Output: r[i] = even[i] + odd[i]
    // r0=b0+b7, r1=b2+b5, r2=b4+b3, r3=b6+b1
    // r4=b6-b1, r5=b4-b3, r6=b2-b5, r7=b0-b7
    MOVL 16(SP), R12; ADDL R13, R12; MOVW R12, 0(R15)   // b0+b7
    MOVL 20(SP), R12; ADDL R8, R12;  MOVW R12, 2(R15)   // b2+b5
    MOVL 24(SP), R12; ADDL CX, R12;  MOVW R12, 4(R15)   // b4+b3
    MOVL 28(SP), R12; ADDL AX, R12;  MOVW R12, 6(R15)   // b6+b1
    MOVL 28(SP), R12; SUBL AX, R12;  MOVW R12, 8(R15)   // b6-b1
    MOVL 24(SP), R12; SUBL CX, R12;  MOVW R12, 10(R15)  // b4-b3
    MOVL 20(SP), R12; SUBL R8, R12;  MOVW R12, 12(R15)  // b2-b5
    MOVL 16(SP), R12; SUBL R13, R12; MOVW R12, 14(R15)  // b0-b7

    INCL SI
    JMP  hloop8
hloop8_done:

    // === Vertical pass: 8 columns ===
    XORL SI, SI
vloop8:
    CMPL SI, $8
    JGE  vloop8_done

    LEAQ (DI)(SI*2), R15   // column base

    // Load column (stride=16 bytes)
    MOVWLSX 0(R15), AX
    MOVWLSX 16(R15), BX
    MOVWLSX 32(R15), CX
    MOVWLSX 48(R15), DX
    MOVWLSX 64(R15), R8
    MOVWLSX 80(R15), R9
    MOVWLSX 96(R15), R10
    MOVWLSX 112(R15), R11

    // Same butterfly as horizontal, but with >>6 rounding at output
    // Even
    MOVL AX, R12; ADDL R8, R12    // a0
    MOVL AX, R13; SUBL R8, R13    // a2
    MOVL R12, 0(SP); MOVL R13, 4(SP)
    MOVL CX, R12; SARL $1, R12; SUBL R10, R12  // a4
    MOVL R10, R13; SARL $1, R13; ADDL CX, R13  // a6
    MOVL R12, 8(SP); MOVL R13, 12(SP)
    
    MOVL 0(SP), AX; ADDL 12(SP), AX; MOVL AX, 16(SP)  // b0
    MOVL 4(SP), AX; ADDL 8(SP), AX;  MOVL AX, 20(SP)  // b2
    MOVL 4(SP), AX; SUBL 8(SP), AX;  MOVL AX, 24(SP)  // b4
    MOVL 0(SP), AX; SUBL 12(SP), AX; MOVL AX, 28(SP)  // b6

    // Odd
    MOVL R11, AX; SARL $1, AX; ADDL R11, AX; NEGL AX; ADDL R9, AX; SUBL DX, AX
    MOVL AX, 32(SP)
    MOVL DX, AX; SARL $1, AX; ADDL DX, AX; NEGL AX; ADDL BX, AX; ADDL R11, AX
    MOVL AX, 36(SP)
    MOVL R9, AX; SARL $1, AX; ADDL R9, AX; ADDL R11, AX; SUBL BX, AX
    MOVL AX, 40(SP)
    MOVL BX, AX; SARL $1, AX; ADDL BX, AX; ADDL DX, AX; ADDL R9, AX
    MOVL AX, 44(SP)

    MOVL 44(SP), AX; SARL $2, AX; ADDL 32(SP), AX      // b1
    MOVL 40(SP), CX; SARL $2, CX; ADDL 36(SP), CX      // b3
    MOVL 36(SP), R8; SARL $2, R8; SUBL 40(SP), R8       // b5
    MOVL 32(SP), R12; SARL $2, R12; MOVL 44(SP), R13; SUBL R12, R13  // b7

    // Output with >>6 rounding
    MOVL 16(SP), R12; ADDL R13, R12; ADDL $32, R12; SARL $6, R12; MOVW R12, 0(R15)
    MOVL 20(SP), R12; ADDL R8, R12;  ADDL $32, R12; SARL $6, R12; MOVW R12, 16(R15)
    MOVL 24(SP), R12; ADDL CX, R12;  ADDL $32, R12; SARL $6, R12; MOVW R12, 32(R15)
    MOVL 28(SP), R12; ADDL AX, R12;  ADDL $32, R12; SARL $6, R12; MOVW R12, 48(R15)
    MOVL 28(SP), R12; SUBL AX, R12;  ADDL $32, R12; SARL $6, R12; MOVW R12, 64(R15)
    MOVL 24(SP), R12; SUBL CX, R12;  ADDL $32, R12; SARL $6, R12; MOVW R12, 80(R15)
    MOVL 20(SP), R12; SUBL R8, R12;  ADDL $32, R12; SARL $6, R12; MOVW R12, 96(R15)
    MOVL 16(SP), R12; SUBL R13, R12; ADDL $32, R12; SARL $6, R12; MOVW R12, 112(R15)

    INCL SI
    JMP  vloop8
vloop8_done:
    RET

// func DCT8x8_ASM(block *int16)
// Forward 8×8 DCT — same butterfly structure, different output ordering.
// For now, delegate to Go scalar (8×8 forward DCT is encoder-only, less critical).
TEXT ·DCT8x8_ASM(SB), NOSPLIT, $0-8
    RET
