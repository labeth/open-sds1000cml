// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the motion signals (proto_gen_test.go): a quadrature
// encoder's A and B, a stepper driver's STEP and DIR, a PWM line.

// quadMoves appends encoder steps to l, A on line a and B on line b: each
// entry of moves is +1 (A leads B) or -1, one whole cycle of four quarter
// periods of per each; the lines start and end at state ab (0..3 as A|B<<1)
// and rest rest seconds after the moves.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func quadMoves(l twoLine, moves []int, per, rest float64) {
	// the forward cycle: 00, A=1, A=B=1, B=1
	seq := [4][2]byte{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	ph := 0
	for _, m := range moves {
		for q := 0; q < 4; q++ {
			ph = ((ph+m)%4 + 4) % 4
			l.add(seq[ph][0], seq[ph][1], per/4)
		}
	}
	l.add(seq[ph][0], seq[ph][1], rest)
}

// stepperMoves appends STEP pulses (line a, high for a quarter period) at a
// period per with DIR (line b) set a period before each: dirs holds one
// direction (1 up, 0 down) per step; then rest seconds of idle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func stepperMoves(l twoLine, dirs []byte, per, rest float64) {
	for _, d := range dirs {
		l.add(0, d, per*3/4)
		l.add(1, d, per/4)
	}
	last := byte(0)
	if len(dirs) > 0 {
		last = dirs[len(dirs)-1]
	}
	l.add(0, last, rest)
}

// pwmWave appends PWM periods of per seconds, one duty cycle (0..1) each,
// high first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func pwmWave(w *timeline, per float64, duties []float64) {
	for _, d := range duties {
		w.add(1, d*per)
		w.add(0, (1-d)*per)
	}
}
