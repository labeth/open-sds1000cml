package panel

import "testing"

func TestButtonEdges(t *testing.T) {
	c, eng, _ := newC(t)

	// RUN/STOP is sel 0x65 (idx 1) bit 2: press = 1→0 edge.
	m := idle()
	m[1] &^= 1 << 2
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0] != (call{"run", 0, 0}) {
		t.Fatalf("run/stop press: %v", eng.calls)
	}
	// Held (still low): no repeat.
	c.decode(m, true)
	if len(eng.calls) != 1 {
		t.Fatalf("held button repeated: %v", eng.calls)
	}
	// Release then press again: toggles back to running.
	c.decode(idle(), true)
	c.decode(m, true)
	if eng.calls[len(eng.calls)-1] != (call{"run", 1, 0}) {
		t.Fatalf("second press: %v", eng.calls)
	}
}

func TestSingleAndAuto(t *testing.T) {
	c, eng, _ := newC(t)
	m := idle()
	m[1] &^= 1 << 10 // SINGLE = 0x65 bit 10
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0] != (call{"single", 0, 0}) {
		t.Fatalf("single: %v", eng.calls)
	}
	// SINGLE LED set.
	if last := eng.leds[len(eng.leds)-1]; last&ledSingle == 0 {
		t.Fatalf("single LED not set: %#04x", last)
	}
	c.decode(idle(), true)
	m = idle()
	m[3] &^= 1 << 10 // AUTO = 0x67 bit 10
	c.decode(m, true)
	if eng.calls[len(eng.calls)-2] != (call{"norm", 0, 0}) {
		t.Fatalf("auto: %v", eng.calls)
	}
}

func TestKnobPriorityOneRowPerEvent(t *testing.T) {
	c, eng, fe := newC(t)
	// Two knobs "moving" at once: HORIZ POSITION (pri 1) must win over TIME/DIV
	// (pri 3) — exactly one knob is serviced per event, so trigpos is dispatched
	// and tdiv is NOT.
	m := idle()
	m[3] &^= 1 << 14 // horizpos CW
	m[2] &^= 1 << 14 // tdiv CW
	m[4] = 1
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0].what != "trigpos" {
		t.Fatalf("expected exactly one trigpos dispatch (horizpos wins priority), got %v", eng.calls)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("no front-end call expected, got %v", fe.calls)
	}
}

func TestTdivKnob(t *testing.T) {
	c, eng, _ := newC(t)
	// TIME/DIV CW (bit14 low): +1 detent (500µs → 1ms), stepped (0x69 ignored).
	m := idle()
	m[2] &^= 1 << 14
	m[4] = 37 // magnitude must be ignored on stepped knobs
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0].what != "tdiv" || eng.calls[0].a != int(1e-3*1e9) {
		t.Fatalf("tdiv CW: %v", eng.calls)
	}
	// Sustained rotation: bit stays low next event (no phase change) —
	// must still step (resting-bit decode, not edge decode).
	c.decode(idle(), true)
	c.decode(m, true)
	if len(eng.calls) != 2 {
		t.Fatalf("sustained rotation missed: %v", eng.calls)
	}
}

func TestVdivKnob(t *testing.T) {
	c, _, fe := newC(t)
	// CH1 V/DIV CCW (0x65 bit15 low): 1V (idx 8) → 500mV (idx 7).
	m := idle()
	m[1] &^= 1 << 15
	m[4] = 1
	c.decode(m, true)
	if len(fe.calls) != 1 || fe.calls[0] != (call{"vdiv", 0, 7}) {
		t.Fatalf("ch1 vdiv CCW: %v", fe.calls)
	}
}

func TestTrigLevelSign(t *testing.T) {
	c, eng, _ := newC(t)
	// TRIG LEVEL CW must LOWER the code: 31434 − 1·40·1 = 31394.
	m := idle()
	m[0] &^= 1 << 14
	m[4] = 1
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0] != (call{"triglevel", 31394, 0}) {
		t.Fatalf("trig level CW: %v", eng.calls)
	}
}

func TestPositionKnobAccel(t *testing.T) {
	c, _, fe := newC(t)
	// CH1 POSITION (continuous) with raw 0x69 = 25 → 100 steps. Each step is
	// 20 codes = 20/100 V at the fake's 1 V/div slope; CW (+1) → +100·20/100 V.
	m := idle()
	m[0] &^= 1 << 6
	m[4] = 25
	c.decode(m, true)
	want := 100 * 20.0 / 100.0
	if len(fe.calls) != 1 || fe.calls[0].what != "offset" || fe.offReqV[0] < want-1e-9 || fe.offReqV[0] > want+1e-9 {
		t.Fatalf("ch1 pos accel: fe=%+v want volts %v", fe.calls, want)
	}
}

func TestKnobResyncFromEngine(t *testing.T) {
	c, eng, fe := newC(t)
	// Web/SCPI moved trigger level to 30000 and V/div to idx 5 behind the
	// panel's back; the next knob step must start from THAT, not a stale
	// shadow.
	eng.stats.TrigCode = 30000
	fe.idx = [2]int{5, 5}
	m := idle()
	m[0] &^= 1 << 14 // TRIG LEVEL CW: 30000 − 40 = 29960
	m[4] = 1
	c.decode(m, true)
	if eng.calls[len(eng.calls)-1] != (call{"triglevel", 29960, 0}) {
		t.Fatalf("resync trig: %v", eng.calls)
	}
	// V/div CW from the resynced idx 5 → 6, not from the stale boot idx 8.
	m = idle()
	m[1] &^= 1 << 14
	m[4] = 1
	c.decode(m, true)
	if fe.calls[len(fe.calls)-1] != (call{"vdiv", 0, 6}) {
		t.Fatalf("resync vdiv: %v", fe.calls)
	}
}

func TestAccelMap(t *testing.T) {
	cases := map[uint16]int{0: 0, 5: 5, 9: 9, 10: 50, 19: 50, 20: 100, 150: 100, 1000: 100}
	for raw, want := range cases {
		if got := accel(raw); got != want {
			t.Errorf("accel(%d) = %d, want %d", raw, got, want)
		}
	}
}

func TestKnobGateOnZeroMagnitude(t *testing.T) {
	c, eng, fe := newC(t)
	// Phase bit low but 0x69 == 0: plain button interrupt, no knob move.
	m := idle()
	m[2] &^= 1 << 14
	m[4] = 0
	c.decode(m, true)
	if len(eng.calls) != 0 || len(fe.calls) != 0 {
		t.Fatalf("knob moved with zero magnitude: %v %v", eng.calls, fe.calls)
	}
}

func TestResyncButtonsOnly(t *testing.T) {
	c, eng, _ := newC(t)
	// Knob phase low + magnitude on a BUTTONS-ONLY decode (40 ms tick):
	// no knob dispatch.
	m := idle()
	m[2] &^= 1 << 14
	m[4] = 3
	c.decode(m, false)
	if len(eng.calls) != 0 {
		t.Fatalf("knob decoded on the re-sync tick: %v", eng.calls)
	}
}

func TestKnobPushTrigger(t *testing.T) {
	c, eng, _ := newC(t)
	last := func() call { return eng.calls[len(eng.calls)-1] }
	// Push CH1 V/DIV (0x65:9 → m[1] bit 9) → trigger source C1.
	m := idle()
	m[1] &^= 1 << 9
	c.decode(m, true)
	if got := last(); got != (call{"src", 0, 0}) {
		t.Fatalf("CH1 V/DIV push → trig source C1: got %v", got)
	}
	c.decode(idle(), true) // release
	// Push CH2 V/DIV (0x66:1 → m[2] bit 1) → trigger source C2.
	m = idle()
	m[2] &^= 1 << 1
	c.decode(m, true)
	if got := last(); got != (call{"src", 1, 0}) {
		t.Fatalf("CH2 V/DIV push → trig source C2: got %v", got)
	}
	c.decode(idle(), true)
	// Push TRIG LEVEL (0x64:9 → m[0] bit 9) → flip slope (default rising=false → true).
	m = idle()
	m[0] &^= 1 << 9
	c.decode(m, true)
	if got := last(); got != (call{"slope", 1, 0}) {
		t.Fatalf("TRIG LEVEL push → flip slope to rising: got %v", got)
	}
}
