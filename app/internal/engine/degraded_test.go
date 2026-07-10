package engine

import "testing"

// mkTail builds a record: live square for [0,split), then a period-5 dead tail
// (the frozen stream-port repeat) with optional ±1 read-noise and sparse
// glitches — the shapes the bench actually produced in the stuck-FSM state.
func mkTail(n, split int, noisy bool, glitchEvery int) []uint8 {
	sig := make([]uint8, n)
	base := [5]uint8{185, 171, 159, 153, 155}
	for i := 0; i < n; i++ {
		if i < split {
			if (i/64)%2 == 0 {
				sig[i] = 200
			} else {
				sig[i] = 60
			}
			continue
		}
		v := base[i%5]
		if noisy && i%3 == 0 {
			v++ // ±1 LSB read noise on the frozen ports
		}
		if glitchEvery > 0 && i%glitchEvery == 0 {
			v += 9 // an isolated glitch inside the dead tail
		}
		sig[i] = v
	}
	return sig
}

func TestRealDepthTolerantTail(t *testing.T) {
	const n, split = 4096, 2048
	cases := []struct {
		name string
		sig  []uint8
		half bool // must classify as a half record (realDepth*4 < n*3)
	}{
		{"exact period-5 tail", mkTail(n, split, false, 0), true},
		{"noisy ±1 tail", mkTail(n, split, true, 0), true},
		{"noisy tail + sparse glitches", mkTail(n, split, true, 257), true},
		{"full live record", mkTail(n, n, false, 0), false},
		{"flat record", make([]uint8, n), false}, // ptp<8: legitimate quiet screen
	}
	for _, c := range cases {
		rd := realDepth(c.sig)
		half := rd*4 < n*3
		if half != c.half {
			t.Errorf("%s: realDepth=%d/%d half=%v, want half=%v", c.name, rd, n, half, c.half)
		}
		// the detector must never report a dead tail eating into the live half
		if rd < split-8 && c.sig[0] != 0 {
			t.Errorf("%s: realDepth=%d ate into the live region (split=%d)", c.name, rd, split)
		}
	}
}

func TestDegradedFlagAndStuckEscalation(t *testing.T) {
	// A native-fast record whose dead tail survives every re-capture retry must
	// publish with Degraded=true; a long consecutive run must raise StuckSuspect
	// (the bench stuck-FSM state that only a power-cycle clears); one clean
	// capture clears both.
	fb := newFakeBus()
	fb.trigOnGo = true
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		// live square then a NOISY period-5 dead tail on both channels
		base := [5]uint8{185, 171, 159, 153, 155}
		if i < deepRecord/2 {
			if (i/512)%2 == 0 {
				return 200, 60
			}
			return 56, 190
		}
		v := base[i%5]
		if i%3 == 0 {
			v++
		}
		return v, v
	}
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	b, ok := PlanTdiv(1e-6)
	if !ok {
		t.Fatal("1 µs not in ladder")
	}
	e.band = b
	e.bringUp()

	e.oneFrame(false)
	s := e.Snapshot()
	if !s.Degraded || s.DegradedRun != 1 {
		t.Fatalf("degraded capture not flagged: degraded=%v run=%d", s.Degraded, s.DegradedRun)
	}
	if s.StuckSuspect {
		t.Fatal("stuck suspected after a single degraded capture")
	}
	if f, fresh := e.Consume(); fresh && !f.Degraded {
		t.Fatal("published frame does not carry Degraded")
	}

	for i := 0; i < stuckSuspectRuns+2; i++ {
		e.oneFrame(false)
	}
	if s := e.Snapshot(); !s.StuckSuspect {
		t.Fatalf("stuck state not suspected after %d consecutive degraded captures (run=%d)",
			stuckSuspectRuns+3, s.DegradedRun)
	}

	// One clean capture clears the flag and the run.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		if (i/512)%2 == 0 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	e.oneFrame(false)
	if s := e.Snapshot(); s.Degraded || s.DegradedRun != 0 || s.StuckSuspect {
		t.Fatalf("clean capture did not clear: degraded=%v run=%d stuck=%v",
			s.Degraded, s.DegradedRun, s.StuckSuspect)
	}
}
