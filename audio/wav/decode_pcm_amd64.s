//go:build amd64 && !purego

#include "textflag.h"

DATA ·pcm8Bias+0(SB)/2, $128
DATA ·pcm8Bias+2(SB)/2, $128
DATA ·pcm8Bias+4(SB)/2, $128
DATA ·pcm8Bias+6(SB)/2, $128
GLOBL ·pcm8Bias(SB), RODATA|NOPTR, $8
DATA ·pcm8Scale+0(SB)/8, $0.0078125
DATA ·pcm8Scale+8(SB)/8, $0.0078125
GLOBL ·pcm8Scale(SB), RODATA|NOPTR, $16
DATA ·pcm16Scale+0(SB)/8, $0.000030517578125
DATA ·pcm16Scale+8(SB)/8, $0.000030517578125
GLOBL ·pcm16Scale(SB), RODATA|NOPTR, $16
DATA ·pcm32Scale+0(SB)/8, $0.0000000004656612873077392578125
DATA ·pcm32Scale+8(SB)/8, $0.0000000004656612873077392578125
GLOBL ·pcm32Scale(SB), RODATA|NOPTR, $16

// Four unsigned PCM8 samples -> signed int32 -> float64, exactly matching
// (float64(b)-128)/128. n is divisible by four.
TEXT ·decodePCM8SSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	PXOR X7, X7
	MOVQ ·pcm8Bias(SB), X6
	MOVUPD ·pcm8Scale(SB), X5
pcm8_loop:
	MOVL (SI), AX
	MOVD AX, X0
	PUNPCKLBW X7, X0
	PSUBW X6, X0
	MOVAPD X7, X1
	PCMPGTW X0, X1
	PUNPCKLWL X1, X0
	MOVAPD X0, X2
	PSHUFL $0xee, X2, X2
	CVTPL2PD X0, X3
	CVTPL2PD X2, X4
	MULPD X5, X3
	MULPD X5, X4
	MOVUPD X3, (DI)
	MOVUPD X4, 16(DI)
	ADDQ $4, SI
	ADDQ $32, DI
	SUBQ $4, CX
	JNZ pcm8_loop
	RET

// Four little-endian signed PCM16 samples -> float64. n is divisible by four.
TEXT ·decodePCM16SSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	PXOR X7, X7
	MOVUPD ·pcm16Scale(SB), X5
pcm16_loop:
	MOVQ (SI), X0
	MOVAPD X7, X1
	PCMPGTW X0, X1
	PUNPCKLWL X1, X0
	MOVAPD X0, X2
	PSHUFL $0xee, X2, X2
	CVTPL2PD X0, X3
	CVTPL2PD X2, X4
	MULPD X5, X3
	MULPD X5, X4
	MOVUPD X3, (DI)
	MOVUPD X4, 16(DI)
	ADDQ $8, SI
	ADDQ $32, DI
	SUBQ $4, CX
	JNZ pcm16_loop
	RET

// Four little-endian signed PCM32 samples -> float64. n is divisible by four.
TEXT ·decodePCM32SSE2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	MOVUPD ·pcm32Scale(SB), X5
pcm32_loop:
	MOVUPD (SI), X0
	MOVAPD X0, X1
	PSHUFL $0xee, X1, X1
	CVTPL2PD X0, X2
	CVTPL2PD X1, X3
	MULPD X5, X2
	MULPD X5, X3
	MOVUPD X2, (DI)
	MOVUPD X3, 16(DI)
	ADDQ $16, SI
	ADDQ $32, DI
	SUBQ $4, CX
	JNZ pcm32_loop
	RET
