#include "textflag.h"

// minMax8Block: 32 codes a step into two running minima and maxima on NEON
// (each WORD's instruction beside it), folded pairwise at the end. R10 (g)
// and R11 (the linker's) are untouched.
TEXT ·minMax8Block(SB), NOSPLIT, $0-10
	MOVW p+0(FP), R0
	MOVW n+4(FP), R12
	WORD $0xf3c70e5f	// vmov.i8 q8, #255
	WORD $0xf3c72e5f	// vmov.i8 q9, #255
	WORD $0xf2c04e50	// vmov.i8 q10, #0
	WORD $0xf2c06e50	// vmov.i8 q11, #0
step:
	WORD $0xf420020d	// vld1.8 {d0-d3}, [r0]!
	WORD $0xf34006d0	// vmin.u8 q8, q8, q0
	WORD $0xf34226d2	// vmin.u8 q9, q9, q1
	WORD $0xf34446c0	// vmax.u8 q10, q10, q0
	WORD $0xf34666c2	// vmax.u8 q11, q11, q1
	SUB $32, R12
	CMP $0, R12
	BGT step
	WORD $0xf34006f2	// vmin.u8 q8, q8, q9
	WORD $0xf34446e6	// vmax.u8 q10, q10, q11
	WORD $0xf3400ab1	// vpmin.u8 d16, d16, d17
	WORD $0xf3400ab0	// vpmin.u8 d16, d16, d16
	WORD $0xf3400ab0	// vpmin.u8 d16, d16, d16
	WORD $0xf3400ab0	// vpmin.u8 d16, d16, d16
	WORD $0xf3444aa5	// vpmax.u8 d20, d20, d21
	WORD $0xf3444aa4	// vpmax.u8 d20, d20, d20
	WORD $0xf3444aa4	// vpmax.u8 d20, d20, d20
	WORD $0xf3444aa4	// vpmax.u8 d20, d20, d20
	WORD $0xeed02b90	// vmov.u8 r2, d16[0]
	WORD $0xeed43b90	// vmov.u8 r3, d20[0]
	MOVB R2, lo+8(FP)
	MOVB R3, hi+9(FP)
	RET
