package engine

import "testing"

func TestWedgeLadderNativeFast(t *testing.T) {
	fb := newFakeBus()
	fb.fillAdvance = false // 0x46 frozen: the wedge signature
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 } // flat drain
	fb.mu.Unlock()

	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(1e-6)
	e.band = b
	e.bringUp()

	// CONF_DONE high: dead runs accumulate and bring-up re-asserts, but a
	// flat input is indistinguishable from a partial wedge — never Wedged.
	for i := 0; i < 50; i++ {
		e.oneFrame(false)
	}
	s := e.Snapshot()
	if s.DeadRuns != 50 {
		t.Fatalf("DeadRuns = %d, want 50", s.DeadRuns)
	}
	if s.Wedged {
		t.Fatal("wedged with CONF_DONE high (healthy flat input would crash-loop)")
	}

	// CONF_DONE lost: the fabric is dead — the next 50-multiple marks Wedged.
	fb.mu.Lock()
	fb.confDone = 0x00
	fb.mu.Unlock()
	for i := 0; i < 50; i++ {
		e.oneFrame(false)
	}
	if s := e.Snapshot(); !s.Wedged {
		t.Fatal("CONF_DONE lost but not marked wedged")
	}
}

func TestWedgeLadderDecimated(t *testing.T) {
	// A real decimated wedge: fill frozen, drain flat, and the FPGA fabric
	// dead (CONF_DONE clear). AUTO now drains every frame, so the wedge is
	// caught on the DRAIN path (frozen fill + flat drain + CONF_DONE probe),
	// not the decimated hold path.
	fb := newFakeBus()
	fb.doneOnGo = false
	fb.fillAdvance = false
	fb.confDone = 0x00 // fabric configuration lost
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 } // flat drain
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	for i := 0; i < 50; i++ {
		e.oneFrame(false)
	}
	if s := e.Snapshot(); !s.Wedged {
		t.Fatal("frozen decimated fill + flat drain + dead CONF_DONE not marked wedged")
	}
}

func TestWedgeLadderResetsOnActivity(t *testing.T) {
	fb := newFakeBus()
	fb.fillAdvance = false
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(1e-6)
	e.band = b
	e.bringUp()
	for i := 0; i < 9; i++ {
		e.oneFrame(false)
	}
	if s := e.Snapshot(); s.DeadRuns != 9 {
		t.Fatalf("DeadRuns = %d, want 9", s.DeadRuns)
	}
	// Fill starts advancing again → ladder resets.
	fb.mu.Lock()
	fb.fillAdvance = true
	fb.mu.Unlock()
	e.oneFrame(false)
	if s := e.Snapshot(); s.DeadRuns != 0 {
		t.Fatalf("DeadRuns = %d after activity, want 0", s.DeadRuns)
	}
}
