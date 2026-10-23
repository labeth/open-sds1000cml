#include "textflag.h"

// 64 bytes a turn with a preload 256 bytes ahead: the Cortex-A8 does not
// fetch ahead by itself, so a plain copy waits on every cache line it reads.
// A preload past the end is only a hint and never faults. R10 (Go's g
// register) and R11 (the linker's) are untouched.
TEXT ·copy64(SB), NOSPLIT, $0-12
	MOVW dst+0(FP), R0
	MOVW src+4(FP), R9
	MOVW n+8(FP), R12
	ADD R9, R12
loop:
	CMP R12, R9
	BHS done
	PLD 256(R9)
	MOVM.IA.W (R9), [R1-R8]
	MOVM.IA.W [R1-R8], (R0)
	MOVM.IA.W (R9), [R1-R8]
	MOVM.IA.W [R1-R8], (R0)
	B loop
done:
	RET
