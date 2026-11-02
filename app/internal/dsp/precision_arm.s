#include "textflag.h"

// Exact 63-tap symmetric dot product. R10 (Go's g register) is untouched.
TEXT ·precisionWindow(SB), NOSPLIT, $0-12
	MOVW window+0(FP), R0
	ADD $124, R0, R1
	MOVW $·precisionTaps(SB), R2
	MOVHU 62(R0), R6
	MOVW 124(R2), R8
	MULL R6, R8, (R5, R4)
	// Unroll the fixed 31 symmetric pairs; preserve exact accumulation order.
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVW R4, ret_lo+4(FP)
	MOVW R5, ret_hi+8(FP)
	RET

// precisionBlock filters n samples: dst[k] is the rounded, clamped window
// sum centred on src[k+31], the very arithmetic of precisionWindow and
// ConditionQ8, with the loop here so a sample costs no Go call. R10 (g)
// and R11 (the linker's) are untouched.
//
// Eight samples at a time on NEON (the Go assembler has no NEON mnemonics:
// each WORD's instruction is beside it): the pair sums of eight windows in
// two widening adds, two 64-bit lanes of products per multiply, the next
// pair's loads issued ahead of this pair's multiplies, and the rounding shift
// and the clamp to 0..65535 as saturating narrows - the same integer sums,
// so the same samples as one window at a time. The scalar loop filters the
// last n%8. On the Cortex-A8 a record filters in under a quarter of the time
// one MULAL chain a sample took.
TEXT ·precisionBlock(SB), NOSPLIT, $0-12
	MOVW src+0(FP), R3
	MOVW dst+4(FP), R9
	MOVW n+8(FP), R12
	// The taps' first 32 (31 pairs and the centre) into D0-D15: a by-scalar
	// multiply's 32-bit scalar must lie there.
	MOVW $·precisionTaps(SB), R2
	WORD $0xf422028d	// vld1.32 {d0-d3}, [r2]!
	WORD $0xf422428d	// vld1.32 {d4-d7}, [r2]!
	WORD $0xf422828d	// vld1.32 {d8-d11}, [r2]!
	WORD $0xf422c28d	// vld1.32 {d12-d15}, [r2]!
	MOVW $2, R5
	MOVW $-2, R6
eight:
	CMP $8, R12
	BLT block
	MOVW R3, R0
	ADD $124, R3, R1
	WORD $0xf34001f0	// veor q8, q8, q8
	WORD $0xf34221f2	// veor q9, q9, q9
	WORD $0xf34441f4	// veor q10, q10, q10
	WORD $0xf34661f6	// veor q11, q11, q11
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c0	// vmlal.s32 q8, d24, d0[0]
	WORD $0xf2e922c0	// vmlal.s32 q9, d25, d0[0]
	WORD $0xf2ea42c0	// vmlal.s32 q10, d26, d0[0]
	WORD $0xf2eb62c0	// vmlal.s32 q11, d27, d0[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e0	// vmlal.s32 q8, d24, d0[1]
	WORD $0xf2e922e0	// vmlal.s32 q9, d25, d0[1]
	WORD $0xf2ea42e0	// vmlal.s32 q10, d26, d0[1]
	WORD $0xf2eb62e0	// vmlal.s32 q11, d27, d0[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c1	// vmlal.s32 q8, d24, d1[0]
	WORD $0xf2e922c1	// vmlal.s32 q9, d25, d1[0]
	WORD $0xf2ea42c1	// vmlal.s32 q10, d26, d1[0]
	WORD $0xf2eb62c1	// vmlal.s32 q11, d27, d1[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e1	// vmlal.s32 q8, d24, d1[1]
	WORD $0xf2e922e1	// vmlal.s32 q9, d25, d1[1]
	WORD $0xf2ea42e1	// vmlal.s32 q10, d26, d1[1]
	WORD $0xf2eb62e1	// vmlal.s32 q11, d27, d1[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c2	// vmlal.s32 q8, d24, d2[0]
	WORD $0xf2e922c2	// vmlal.s32 q9, d25, d2[0]
	WORD $0xf2ea42c2	// vmlal.s32 q10, d26, d2[0]
	WORD $0xf2eb62c2	// vmlal.s32 q11, d27, d2[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e2	// vmlal.s32 q8, d24, d2[1]
	WORD $0xf2e922e2	// vmlal.s32 q9, d25, d2[1]
	WORD $0xf2ea42e2	// vmlal.s32 q10, d26, d2[1]
	WORD $0xf2eb62e2	// vmlal.s32 q11, d27, d2[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c3	// vmlal.s32 q8, d24, d3[0]
	WORD $0xf2e922c3	// vmlal.s32 q9, d25, d3[0]
	WORD $0xf2ea42c3	// vmlal.s32 q10, d26, d3[0]
	WORD $0xf2eb62c3	// vmlal.s32 q11, d27, d3[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e3	// vmlal.s32 q8, d24, d3[1]
	WORD $0xf2e922e3	// vmlal.s32 q9, d25, d3[1]
	WORD $0xf2ea42e3	// vmlal.s32 q10, d26, d3[1]
	WORD $0xf2eb62e3	// vmlal.s32 q11, d27, d3[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c4	// vmlal.s32 q8, d24, d4[0]
	WORD $0xf2e922c4	// vmlal.s32 q9, d25, d4[0]
	WORD $0xf2ea42c4	// vmlal.s32 q10, d26, d4[0]
	WORD $0xf2eb62c4	// vmlal.s32 q11, d27, d4[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e4	// vmlal.s32 q8, d24, d4[1]
	WORD $0xf2e922e4	// vmlal.s32 q9, d25, d4[1]
	WORD $0xf2ea42e4	// vmlal.s32 q10, d26, d4[1]
	WORD $0xf2eb62e4	// vmlal.s32 q11, d27, d4[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c5	// vmlal.s32 q8, d24, d5[0]
	WORD $0xf2e922c5	// vmlal.s32 q9, d25, d5[0]
	WORD $0xf2ea42c5	// vmlal.s32 q10, d26, d5[0]
	WORD $0xf2eb62c5	// vmlal.s32 q11, d27, d5[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e5	// vmlal.s32 q8, d24, d5[1]
	WORD $0xf2e922e5	// vmlal.s32 q9, d25, d5[1]
	WORD $0xf2ea42e5	// vmlal.s32 q10, d26, d5[1]
	WORD $0xf2eb62e5	// vmlal.s32 q11, d27, d5[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c6	// vmlal.s32 q8, d24, d6[0]
	WORD $0xf2e922c6	// vmlal.s32 q9, d25, d6[0]
	WORD $0xf2ea42c6	// vmlal.s32 q10, d26, d6[0]
	WORD $0xf2eb62c6	// vmlal.s32 q11, d27, d6[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e6	// vmlal.s32 q8, d24, d6[1]
	WORD $0xf2e922e6	// vmlal.s32 q9, d25, d6[1]
	WORD $0xf2ea42e6	// vmlal.s32 q10, d26, d6[1]
	WORD $0xf2eb62e6	// vmlal.s32 q11, d27, d6[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c7	// vmlal.s32 q8, d24, d7[0]
	WORD $0xf2e922c7	// vmlal.s32 q9, d25, d7[0]
	WORD $0xf2ea42c7	// vmlal.s32 q10, d26, d7[0]
	WORD $0xf2eb62c7	// vmlal.s32 q11, d27, d7[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e7	// vmlal.s32 q8, d24, d7[1]
	WORD $0xf2e922e7	// vmlal.s32 q9, d25, d7[1]
	WORD $0xf2ea42e7	// vmlal.s32 q10, d26, d7[1]
	WORD $0xf2eb62e7	// vmlal.s32 q11, d27, d7[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c8	// vmlal.s32 q8, d24, d8[0]
	WORD $0xf2e922c8	// vmlal.s32 q9, d25, d8[0]
	WORD $0xf2ea42c8	// vmlal.s32 q10, d26, d8[0]
	WORD $0xf2eb62c8	// vmlal.s32 q11, d27, d8[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e8	// vmlal.s32 q8, d24, d8[1]
	WORD $0xf2e922e8	// vmlal.s32 q9, d25, d8[1]
	WORD $0xf2ea42e8	// vmlal.s32 q10, d26, d8[1]
	WORD $0xf2eb62e8	// vmlal.s32 q11, d27, d8[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802c9	// vmlal.s32 q8, d24, d9[0]
	WORD $0xf2e922c9	// vmlal.s32 q9, d25, d9[0]
	WORD $0xf2ea42c9	// vmlal.s32 q10, d26, d9[0]
	WORD $0xf2eb62c9	// vmlal.s32 q11, d27, d9[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802e9	// vmlal.s32 q8, d24, d9[1]
	WORD $0xf2e922e9	// vmlal.s32 q9, d25, d9[1]
	WORD $0xf2ea42e9	// vmlal.s32 q10, d26, d9[1]
	WORD $0xf2eb62e9	// vmlal.s32 q11, d27, d9[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802ca	// vmlal.s32 q8, d24, d10[0]
	WORD $0xf2e922ca	// vmlal.s32 q9, d25, d10[0]
	WORD $0xf2ea42ca	// vmlal.s32 q10, d26, d10[0]
	WORD $0xf2eb62ca	// vmlal.s32 q11, d27, d10[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802ea	// vmlal.s32 q8, d24, d10[1]
	WORD $0xf2e922ea	// vmlal.s32 q9, d25, d10[1]
	WORD $0xf2ea42ea	// vmlal.s32 q10, d26, d10[1]
	WORD $0xf2eb62ea	// vmlal.s32 q11, d27, d10[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802cb	// vmlal.s32 q8, d24, d11[0]
	WORD $0xf2e922cb	// vmlal.s32 q9, d25, d11[0]
	WORD $0xf2ea42cb	// vmlal.s32 q10, d26, d11[0]
	WORD $0xf2eb62cb	// vmlal.s32 q11, d27, d11[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802eb	// vmlal.s32 q8, d24, d11[1]
	WORD $0xf2e922eb	// vmlal.s32 q9, d25, d11[1]
	WORD $0xf2ea42eb	// vmlal.s32 q10, d26, d11[1]
	WORD $0xf2eb62eb	// vmlal.s32 q11, d27, d11[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802cc	// vmlal.s32 q8, d24, d12[0]
	WORD $0xf2e922cc	// vmlal.s32 q9, d25, d12[0]
	WORD $0xf2ea42cc	// vmlal.s32 q10, d26, d12[0]
	WORD $0xf2eb62cc	// vmlal.s32 q11, d27, d12[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802ec	// vmlal.s32 q8, d24, d12[1]
	WORD $0xf2e922ec	// vmlal.s32 q9, d25, d12[1]
	WORD $0xf2ea42ec	// vmlal.s32 q10, d26, d12[1]
	WORD $0xf2eb62ec	// vmlal.s32 q11, d27, d12[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802cd	// vmlal.s32 q8, d24, d13[0]
	WORD $0xf2e922cd	// vmlal.s32 q9, d25, d13[0]
	WORD $0xf2ea42cd	// vmlal.s32 q10, d26, d13[0]
	WORD $0xf2eb62cd	// vmlal.s32 q11, d27, d13[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802ed	// vmlal.s32 q8, d24, d13[1]
	WORD $0xf2e922ed	// vmlal.s32 q9, d25, d13[1]
	WORD $0xf2ea42ed	// vmlal.s32 q10, d26, d13[1]
	WORD $0xf2eb62ed	// vmlal.s32 q11, d27, d13[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802ce	// vmlal.s32 q8, d24, d14[0]
	WORD $0xf2e922ce	// vmlal.s32 q9, d25, d14[0]
	WORD $0xf2ea42ce	// vmlal.s32 q10, d26, d14[0]
	WORD $0xf2eb62ce	// vmlal.s32 q11, d27, d14[0]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca45	// vld1.16 {d28-d29}, [r0], r5
	WORD $0xf461ea46	// vld1.16 {d30-d31}, [r1], r6
	WORD $0xf2e802ee	// vmlal.s32 q8, d24, d14[1]
	WORD $0xf2e922ee	// vmlal.s32 q9, d25, d14[1]
	WORD $0xf2ea42ee	// vmlal.s32 q10, d26, d14[1]
	WORD $0xf2eb62ee	// vmlal.s32 q11, d27, d14[1]
	WORD $0xf3dc80ae	// vaddl.u16 q12, d28, d30
	WORD $0xf3dda0af	// vaddl.u16 q13, d29, d31
	WORD $0xf460ca4f	// vld1.16 {d28-d29}, [r0]
	WORD $0xf2e802cf	// vmlal.s32 q8, d24, d15[0]
	WORD $0xf2e922cf	// vmlal.s32 q9, d25, d15[0]
	WORD $0xf2ea42cf	// vmlal.s32 q10, d26, d15[0]
	WORD $0xf2eb62cf	// vmlal.s32 q11, d27, d15[0]
	WORD $0xf3d08a3c	// vmovl.u16 q12, d28
	WORD $0xf3d0aa3d	// vmovl.u16 q13, d29
	WORD $0xf2e802ef	// vmlal.s32 q8, d24, d15[1]
	WORD $0xf2e922ef	// vmlal.s32 q9, d25, d15[1]
	WORD $0xf2ea42ef	// vmlal.s32 q10, d26, d15[1]
	WORD $0xf2eb62ef	// vmlal.s32 q11, d27, d15[1]
	WORD $0xf2ea0970	// vqrshrn.s64 d16, q8, #22
	WORD $0xf2ea1972	// vqrshrn.s64 d17, q9, #22
	WORD $0xf2ea2974	// vqrshrn.s64 d18, q10, #22
	WORD $0xf2ea3976	// vqrshrn.s64 d19, q11, #22
	WORD $0xf3f60260	// vqmovun.s32 d16, q8
	WORD $0xf3f61262	// vqmovun.s32 d17, q9
	WORD $0xf4490a4d	// vst1.16 {d16-d17}, [r9]!
	ADD $16, R3
	SUB $8, R12
	B eight
block:
	CMP $0, R12
	BLE blockdone
	MOVW R3, R0
	ADD $124, R0, R1
	MOVW $·precisionTaps(SB), R2
	MOVHU 62(R0), R6
	MOVW 124(R2), R8
	MULL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	// (sum + 1<<21) >> 22, which fits 32 bits, clamped to 0..65535.
	ADD.S $0x200000, R4
	ADC $0, R5
	MOVW R4>>22, R6
	ORR R5<<10, R6
	CMP $0, R6
	MOVW.LT $0, R6
	MOVW $65535, R7
	CMP R7, R6
	MOVW.GT R7, R6
	MOVH R6, (R9)
	ADD $2, R9
	ADD $2, R3
	SUB $1, R12
	B block
blockdone:
	RET
