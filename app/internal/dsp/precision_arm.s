#include "textflag.h"

// Exact 63-tap symmetric dot product. R10 (Go's g register) is untouched.
TEXT ·precisionWindow(SB), NOSPLIT, $0-12
	MOVW window+0(FP), R0
	ADD $124, R0, R1
	MOVW $·precisionTaps(SB), R2
	MOVHU 62(R0), R6
	MOVW 124(R2), R8
	MULL R6, R8, (R5, R4)
	MOVW $31, R3
loop:
	MOVHU.P 2(R0), R6
	MOVHU.P -2(R1), R7
	MOVW.P 4(R2), R8
	ADD R7, R6, R6
	MULAL R6, R8, (R5, R4)
	SUB.S $1, R3
	BNE loop
	MOVW R4, ret_lo+4(FP)
	MOVW R5, ret_hi+8(FP)
	RET
