package panel

import (
	"testing"

	"open-sds/app/internal/engine"
)

// TestRailing pins the semantics the coarsen-on-rail guard (autoset step 3c)
// depends on: a channel counts as "railing" only when MORE than ~5% of its valid
// samples sit at an extreme code (≤1 or ≥254). This is the model-independent
// signal that a DC-heavy trace is parked off-screen on a range where the offset
// DAC can't pull it in, so autoset must coarsen the V/div.
func TestRailing(t *testing.T) {
	c, _, _ := newC(t)

	// helper: build an N-sample channel where `railed` of them are pinned high.
	mk := func(n, railed int, env bool) *engine.Frame {
		s := make([]uint8, n)
		for i := range s {
			s[i] = 128 // mid-screen, well clear of both rails
		}
		for i := 0; i < railed && i < n; i++ {
			s[i] = 255
		}
		return &engine.Frame{C1: s, C2: s, Valid: n, IsEnv: env}
	}
	use := func(f *engine.Frame) { c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) }) }

	// Clean trace: nothing at the rails → not railing.
	use(mk(1000, 0, false))
	if c.railing(0) {
		t.Errorf("clean trace reported as railing")
	}

	// Just under 5% pinned high (49/1000 = 4.9%) → still not railing.
	use(mk(1000, 49, false))
	if c.railing(0) {
		t.Errorf("4.9%% pinned reported as railing (threshold is >5%%)")
	}

	// Comfortably over 5% (80/1000 = 8%) → railing.
	use(mk(1000, 80, false))
	if !c.railing(0) {
		t.Errorf("8%% pinned NOT reported as railing")
	}

	// Bottom rail counts too (code 0).
	{
		s := make([]uint8, 1000)
		for i := range s {
			s[i] = 128
		}
		for i := 0; i < 80; i++ {
			s[i] = 0
		}
		use(&engine.Frame{C1: s, C2: s, Valid: 1000})
		if !c.railing(0) {
			t.Errorf("bottom-rail (code 0) not reported as railing")
		}
	}

	// Envelope frames are min/max bands, not per-sample rails → never railing,
	// even when the data looks pinned.
	use(mk(1000, 500, true))
	if c.railing(0) {
		t.Errorf("envelope frame reported as railing")
	}

	// Too few samples to judge → not railing.
	use(mk(4, 4, false))
	if c.railing(0) {
		t.Errorf("<8-sample frame reported as railing")
	}

	// Channel selection: rail C2 only (C1 clean) and check ch=1 sees it.
	{
		clean := make([]uint8, 1000)
		railed := make([]uint8, 1000)
		for i := range clean {
			clean[i] = 128
			railed[i] = 128
		}
		for i := 0; i < 80; i++ {
			railed[i] = 255
		}
		use(&engine.Frame{C1: clean, C2: railed, Valid: 1000})
		if c.railing(0) {
			t.Errorf("C1 clean but ch0 reported railing")
		}
		if !c.railing(1) {
			t.Errorf("C2 railed but ch1 did not report railing")
		}
	}
}

// TestOffScreenSaturated covers the front-end-amp saturation case that pure ADC-
// rail detection misses: a large DC on a sensitive range saturates the amp before
// the ADC, so the whole trace pins at the screen EDGE (raw-code midpoint ~251,
// ≈+4.9 div off centre) but NO code reaches 254/255 — railing() reads 0%. The 3c
// coarsen guard keys on offScreen(), which must still report "coarsen" here so the
// signal is walked down to an attenuated range where the offset can centre it.
func TestOffScreenSaturated(t *testing.T) {
	c, _, _ := newC(t)
	use := func(f *engine.Frame) { c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) }) }

	// Every sample at code 251: pinned at the top edge, but below the 254 rail.
	edge := make([]uint8, 1000)
	for i := range edge {
		edge[i] = 251
	}
	use(&engine.Frame{C1: edge, C2: edge, Valid: 1000})
	if c.railing(0) {
		t.Fatalf("code-251 edge should NOT count as hard ADC-railing (0%% at rails)")
	}
	if !c.offScreen(0) {
		t.Errorf("saturated trace at ~+4.9 div NOT flagged off-screen — guard would not coarsen")
	}

	// A legitimately centred full-screen swing (midpoint at 128) must NOT coarsen.
	centred := make([]uint8, 1000)
	for i := range centred {
		if i%2 == 0 {
			centred[i] = 30 // ~-3.9 div
		} else {
			centred[i] = 226 // ~+3.9 div → midpoint 128
		}
	}
	use(&engine.Frame{C1: centred, C2: centred, Valid: 1000})
	if c.offScreen(0) {
		t.Errorf("centred full-screen signal flagged off-screen — would false-coarsen")
	}
}
