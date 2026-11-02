#include "textflag.h"

// roundQ8Block: sixteen samples at a time on NEON (each WORD's instruction
// beside it), the rounding narrow saturating at 255 as RoundQ8 clamps; the
// last n%16 one at a time. R10 (g) and R11 (the linker's) are untouched.
TEXT ·roundQ8Block(SB), NOSPLIT, $0-12
	MOVW src+0(FP), R0
	MOVW dst+4(FP), R1
	MOVW n+8(FP), R12
sixteen:
	CMP $16, R12
	BLT one
	WORD $0xf420024d	// vld1.16 {d0-d3}, [r0]!
	WORD $0xf3884950	// vqrshrn.u16 d4, q0, #8
	WORD $0xf3885952	// vqrshrn.u16 d5, q1, #8
	WORD $0xf4014a0d	// vst1.8 {d4-d5}, [r1]!
	SUB $16, R12
	B sixteen
one:
	CMP $0, R12
	BLE done
	MOVHU.P 2(R0), R2
	ADD $128, R2
	MOVW R2>>8, R2
	CMP $255, R2
	MOVW.GT $255, R2
	MOVB.P R2, 1(R1)
	SUB $1, R12
	B one
done:
	RET

// splitQ8Block: eight pairs at a time on NEON, the pairs parted by a
// two-element load; the last n%8 one at a time. R10 and R11 are untouched.
TEXT ·splitQ8Block(SB), NOSPLIT, $0-24
	MOVW b+0(FP), R0
	MOVW q1+4(FP), R1
	MOVW q2+8(FP), R2
	MOVW c1+12(FP), R3
	MOVW c2+16(FP), R4
	MOVW n+20(FP), R12
eight:
	CMP $8, R12
	BLT pair
	WORD $0xf420034d	// vld2.16 {d0-d3}, [r0]!
	WORD $0xf4010a4d	// vst1.16 {d0-d1}, [r1]!
	WORD $0xf4022a4d	// vst1.16 {d2-d3}, [r2]!
	WORD $0xf3884950	// vqrshrn.u16 d4, q0, #8
	WORD $0xf3885952	// vqrshrn.u16 d5, q1, #8
	WORD $0xf403470d	// vst1.8 {d4}, [r3]!
	WORD $0xf404570d	// vst1.8 {d5}, [r4]!
	SUB $8, R12
	B eight
pair:
	CMP $0, R12
	BLE splitdone
	MOVHU.P 2(R0), R5
	MOVHU.P 2(R0), R6
	MOVH.P R5, 2(R1)
	MOVH.P R6, 2(R2)
	ADD $128, R5
	MOVW R5>>8, R5
	CMP $255, R5
	MOVW.GT $255, R5
	MOVB.P R5, 1(R3)
	ADD $128, R6
	MOVW R6>>8, R6
	CMP $255, R6
	MOVW.GT $255, R6
	MOVB.P R6, 1(R4)
	SUB $1, R12
	B pair
splitdone:
	RET
