package engine

import (
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/iface"
)

// fakeClock advances instantly on Sleep so FSM waits are deterministic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) clock() Clock {
	return Clock{
		Now: func() time.Time {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.t
		},
		Sleep: func(d time.Duration) {
			c.mu.Lock()
			c.t = c.t.Add(d)
			c.mu.Unlock()
		},
	}
}

type wr struct {
	plane iface.Plane
	sel   uint16
	val   uint16
}

// fakeBus models the STANDARD FPGA's generated interface well enough to drive
// the owned FSM, keyed on iface selectors so it can never drift from the schema:
// the build-ID/VERSION handshake, the OPCODE arm/halt/idle model, RUN mode,
// stored/read-back DECIM/PRE/POST, STATUS_A per the armed/mode model, FILL that
// advances while filling and freezes on halt, a scripted TRIGPOS, the single
// auto-inc BURST drain (post-halt only), and the envelope result channel.
type fakeBus struct {
	mu          sync.Mutex
	writes      []wr
	doneOnGo    bool // STATUS_A.DONE asserts while armed
	trigOnGo    bool // STATUS_A.TRIG asserts while armed
	validOnGo   bool // STATUS_A.VALID asserts while armed (AUTO free-run timeout)
	fillAdvance bool
	confDone    uint16 // CS3 CONF_DONE value; bit7 = DONE
	wave        func(i int) (c1, c2 uint8)

	armed      bool
	halted     bool
	fill       uint16
	burstN     int  // BURST auto-inc pointer (samples from 0 after halt)
	burstReads int  // total BURST/ChannelInto pops (order/telemetry)
	earlyDrain bool // a BURST read before halt = the live-buffer trap
	armCount   int  // OP_GO writes; lets waves shift phase per capture

	trigIdx  uint16 // scripted TRIGPOS_HI (physical index)
	trigFrac uint16 // scripted TRIGPOS_LO (fractional word)

	// scripted envelope result channel (ENV_DATA/ENV_COUNT).
	envWords    []uint16 // packed record words, popped by ChannelInto/ENV_DATA
	envCount    int      // ENV_COUNT record count
	envPos      int
	envOverflow bool
	// envCoherent models the fabric's coherent gate: ENV_DATA (like the raw
	// BURST record) reads back the frozen fold only while coherent, which the
	// fabric sets on OP_HALT and CLEARS on OP_GO (the re-arm also wipes the
	// envelope FIFO). Reading ENV_DATA after a re-arm returns coherent-gated
	// zeros — the exact failure the envelope-ordering bug produces.
	envCoherent bool
}

func newFakeBus() *fakeBus {
	return &fakeBus{
		doneOnGo:    true,
		fillAdvance: true,
		confDone:    iface.Mask_CONF_DONE_DONE,
		// Square wave, period 256: edges everywhere, ptp = 144.
		wave: func(i int) (uint8, uint8) {
			if (i/128)%2 == 0 {
				return 200, 60
			}
			return 56, 190
		},
	}
}

// setEnvRecords scripts the envelope result channel: packs recs into ENV_DATA
// words and sets ENV_COUNT, so the envelope band's fabric-consuming path runs.
func (f *fakeBus) setEnvRecords(recs []iface.EnvelopeRecord, overflow bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.envWords = nil
	for _, r := range recs {
		f.envWords = append(f.envWords, r.Pack()...)
	}
	f.envCount = len(recs)
	f.envPos = 0
	f.envOverflow = overflow
}

func (f *fakeBus) Read(plane iface.Plane, sel uint16) (uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if plane == iface.CS3 {
		if sel == iface.SelCONF_DONE {
			return f.confDone, nil
		}
		return 0, nil
	}
	switch sel {
	case iface.SelVERSION:
		return 0x0052, nil
	case iface.SelBUILDID_LO:
		return uint16(iface.BuildID & 0xffff), nil
	case iface.SelBUILDID_HI:
		return uint16(iface.BuildID >> 16), nil
	case iface.SelSTATUS_A:
		var s uint16
		if f.armed && f.doneOnGo {
			s |= statDone
		}
		if f.armed && f.trigOnGo {
			s |= statTrig
		}
		if f.armed && f.validOnGo {
			s |= statValid
		}
		return s, nil
	case iface.SelFILL:
		if f.armed && !f.halted && f.fillAdvance {
			f.fill += 64
			if f.fill > fillMask {
				f.fill = fillMask
			}
		}
		return f.fill, nil
	case iface.SelTRIGPOS_LO:
		return f.trigFrac, nil
	case iface.SelTRIGPOS_HI:
		return f.trigIdx, nil
	case iface.SelBURST:
		// Single fixed auto-inc port: successive samples from 0 after halt. A
		// read before halt is a live-buffer read — trip the trap.
		if !f.halted {
			f.earlyDrain = true
		}
		c1, c2 := f.wave(f.burstN)
		f.burstN++
		f.burstReads++
		return uint16(c1)<<8 | uint16(c2), nil
	case iface.SelBURST_REMAIN:
		return iface.Mask_BURST_REMAIN_READY, nil // DMA-ready, count elided
	case iface.SelENV_COUNT:
		w := uint16(f.envCount) & iface.Mask_ENV_COUNT_COUNT
		if f.envOverflow {
			w |= iface.Mask_ENV_COUNT_OVERFLOW
		}
		return w, nil
	case iface.SelENV_DATA:
		if !f.envCoherent { // coherent-gated: re-armed fabric returns zeros
			return 0, nil
		}
		if f.envPos < len(f.envWords) {
			v := f.envWords[f.envPos]
			f.envPos++
			return v, nil
		}
		return 0, nil
	}
	return 0, nil
}

func (f *fakeBus) Write(plane iface.Plane, sel, val uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, wr{plane, sel, val})
	if plane == iface.CS1 && sel == iface.SelOPCODE {
		switch val {
		case opGo:
			// Re-arm wipes the frozen record + envelope FIFO and drops coherent:
			// any ENV_DATA/record read after this returns coherent-gated zeros.
			f.armed, f.halted, f.fill = true, false, 0
			f.armCount++
			f.burstN = 0
			f.envPos = 0
			f.envCoherent = false
		case opHalt:
			// Halt freezes the record and resets the read pointer: each frame
			// drains the wave from sample 0, and the fold becomes coherent.
			f.halted = true
			f.burstN = 0
			f.envPos = 0
			f.envCoherent = true
		case opReset:
			f.armed, f.halted, f.fill = false, false, 0
			f.envCoherent = false
		}
	}
	return nil
}

// BurstInto is the single auto-inc BURST drain: sequential samples from the
// current pointer (hi byte C1, lo byte C2), each read popping one word.
func (f *fakeBus) BurstInto(c1, c2 []uint8, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.halted {
		f.earlyDrain = true
	}
	for i := 0; i < n; i++ {
		cc1, cc2 := f.wave(f.burstN)
		f.burstN++
		f.burstReads++
		c1[i] = cc1
		c2[i] = cc2
	}
}

// ChannelInto pops packed result-channel words (ENV_DATA).
func (f *fakeBus) ChannelInto(sel uint16, dst []uint16, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < n; i++ {
		if f.envCoherent && f.envPos < len(f.envWords) { // coherent-gated
			dst[i] = f.envWords[f.envPos]
			f.envPos++
		} else {
			dst[i] = 0
		}
	}
}

func (f *fakeBus) MmapDrain() bool { return true }

func (f *fakeBus) snapWrites() []wr {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wr, len(f.writes))
	copy(out, f.writes)
	return out
}

func (f *fakeBus) clearWrites() {
	f.mu.Lock()
	f.writes = nil
	f.mu.Unlock()
}

func newTestEngine(t *testing.T, fb *fakeBus) (*Engine, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := New(Config{Bus: fb, Clock: clk.clock(), Logf: t.Logf})
	return e, clk
}

func wantWrites(t *testing.T, got, want []wr) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("write count = %d, want %d\ngot: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("write[%d] = {cs%d %#04x=%#04x}, want {cs%d %#04x=%#04x}",
				i, got[i].plane, got[i].sel, got[i].val,
				want[i].plane, want[i].sel, want[i].val)
		}
	}
}

func TestHoldoffPacing(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetFramePeriod(0) // no base pacing floor, so holdoff is the only delay
	if got := e.SetHoldoff(0.2); got != 0.2 {
		t.Fatalf("SetHoldoff returned %v, want 0.2", got)
	}
	if e.Snapshot().HoldoffS != 0.2 {
		t.Fatalf("holdoff not reflected in stats: %v", e.Snapshot().HoldoffS)
	}
	// A triggered frame is held off by ~200 ms before the next arm.
	start := e.clk.Now()
	e.paceHold(start, true)
	if d := e.clk.Now().Sub(start); d < 200*time.Millisecond {
		t.Fatalf("triggered holdoff not applied: waited %v, want ≥200ms", d)
	}
	// An untriggered/AUTO frame is NOT held off (frame period is 0).
	start = e.clk.Now()
	e.paceHold(start, false)
	if d := e.clk.Now().Sub(start); d != 0 {
		t.Fatalf("untriggered frame held off %v, want 0", d)
	}
	// Clamp to [0,10] s and 0 disables.
	if got := e.SetHoldoff(-1); got != 0 {
		t.Fatalf("negative holdoff = %v, want 0", got)
	}
	if got := e.SetHoldoff(99); got != 10 {
		t.Fatalf("holdoff clamp = %v, want 10", got)
	}
}

func TestBringUpWriteOrder(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	// Spec 03 §5.1 — idle, RUN{mode+run}, then DECIM / PRE / POST (lo,hi each).
	// Default band 500 µs/div: decim 0x0190, capWindow decimDrain=6144 → pre/post
	// 0x0C00 each; RUN = mode AUTO (0) + run bit = 0x0004.
	wantWrites(t, fb.snapWrites(), []wr{
		{1, selOpcode, opReset},
		{1, selRun, iface.RunWithMode(modeAuto) | iface.RunWithRun(true)},
		{1, selDecimLo, 0x0190}, {1, selDecimHi, 0x0000},
		{1, selPreLo, 0x0c00}, {1, selPreHi, 0x0000},
		{1, selPostLo, 0x0c00}, {1, selPostHi, 0x0000},
	})
}

// progPrePost reconstructs the last PRE/POST-trigger depths bringUp programmed.
func progPrePost(t *testing.T, ws []wr) (pre, post uint32) {
	t.Helper()
	var preLo, preHi, postLo, postHi uint32
	for _, w := range ws {
		switch w.sel {
		case selPreLo:
			preLo = uint32(w.val)
		case selPreHi:
			preHi = uint32(w.val)
		case selPostLo:
			postLo = uint32(w.val)
		case selPostHi:
			postHi = uint32(w.val)
		}
	}
	return preHi<<16 | preLo, postHi<<16 | postLo
}

// A decimated band with a deep memory depth must size the FABRIC record from
// effDrainCols(), not the fixed decimDrain — otherwise the drain over-reads a
// dead tail past the captured record (the deep-memory/stream/SINGLE HIGH bug).
func TestBringUpDeepMemorySizesRecord(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb) // default band is decimated (500 µs/div)
	e.SetMemDepth(deepRecord)    // deep memory: the drain wants the full record
	fb.snapWrites()              // clear
	e.bringUp()
	pre, post := progPrePost(t, fb.snapWrites())
	want := e.effDrainCols() // the count oneFrame will actually drain
	if want > deepRecord-2 {
		want = deepRecord - 2 // exact-window clamp
	}
	if sum := int(pre + post); sum != want {
		t.Fatalf("deep-memory decimated pre+post = %d, want %d (record must track effDrainCols=%d, not decimDrain=%d)",
			sum, want, e.effDrainCols(), decimDrain)
	}
}

// Envelope/roll bands must arm in AUTO even when the user selected NORM — a
// NORM trigger is impossible for a flat/aliased min/max band, so under NORM the
// record would never cohere and the band would be a coherent-gated blank. A
// decimated band under NORM must still program NORM.
func TestEnvelopeRollForceAuto(t *testing.T) {
	autoRun := iface.RunWithMode(modeAuto) | iface.RunWithRun(true)
	normRun := iface.RunWithMode(modeNorm) | iface.RunWithRun(true)
	cases := []struct {
		name string
		tdiv float64
		want uint16
	}{
		{"envelope", 5e-3, autoRun},
		{"roll", 100e-3, autoRun},
		{"decimated", 500e-6, normRun}, // control: NORM is honored here
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBus()
			e, _ := newTestEngine(t, fb)
			e.SetNorm(true) // user selects NORM
			b, ok := PlanTdiv(tc.tdiv)
			if !ok {
				t.Fatalf("PlanTdiv(%g) rejected", tc.tdiv)
			}
			e.band = b
			fb.snapWrites()
			e.bringUp()
			var run uint16
			found := false
			for _, w := range fb.snapWrites() {
				if w.sel == selRun {
					run, found = w.val, true
				}
			}
			if !found {
				t.Fatal("bringUp wrote no RUN word")
			}
			if run != tc.want {
				t.Fatalf("%s under NORM programmed RUN=%#04x, want %#04x", tc.name, run, tc.want)
			}
		})
	}
}

// The programmed envelope column count must fit the fabric FIFO (2048 words / 6
// words-per-column = 341 columns); above that the fabric drops the tail.
func TestEnvFabricColsFitsFIFO(t *testing.T) {
	const fifoCols = 2048 / 6 // 341
	if envFabricCols > fifoCols {
		t.Fatalf("envFabricCols=%d exceeds the fabric FIFO capacity of %d columns", envFabricCols, fifoCols)
	}
}

func TestArmSequence(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.armEngine()
	// Owned arm is a single OPCODE = OP_GO strobe (spec 03 §5.1).
	wantWrites(t, fb.snapWrites(), []wr{
		{1, selOpcode, opGo},
	})
}

func TestBandChangeAppliedAtBoundary(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	go e.Run()
	defer e.Stop(2 * time.Second)

	waitFor(t, func() bool { return e.Snapshot().Published >= 2 })

	if _, ok := e.SetTdiv(1e-6); !ok {
		t.Fatal("SetTdiv(1µs) rejected")
	}
	waitFor(t, func() bool {
		f, _ := e.Consume()
		return f != nil && f.TdivS == 1e-6 && f.Valid == deepRecord
	})
	if got := e.Snapshot().TdivS; got != 1e-6 {
		t.Fatalf("stats tdiv = %v, want 1µs", got)
	}
}

func TestStopKeepsHeartbeatAndServicesCommands(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	go e.Run()
	defer e.Stop(2 * time.Second)

	waitFor(t, func() bool { return e.Snapshot().Published >= 1 })
	e.SetRunning(false)
	// An iteration already past the running gate may still publish; wait for
	// two more loop boundaries so the stop has definitely taken effect.
	f0 := e.Snapshot().Frames
	waitFor(t, func() bool { return e.Snapshot().Frames > f0+2 })

	base := e.Snapshot()
	fb.clearWrites()
	waitFor(t, func() bool { return e.Snapshot().Frames > base.Frames+3 })
	pub := e.Snapshot().Published
	if pub != base.Published {
		t.Fatalf("published advanced while stopped: %d → %d", base.Published, pub)
	}
	for _, w := range fb.snapWrites() {
		if w.plane == iface.CS1 && w.sel == selOpcode && w.val == opGo {
			t.Fatalf("arm opcode written while stopped: %#v", w)
		}
	}

	// Commands are still serviced while stopped.
	e.SetTrigLevelCode(30000)
	waitFor(t, func() bool {
		for _, w := range fb.snapWrites() {
			if w.plane == 3 && w.sel == cs3LevelALo {
				return true
			}
		}
		return false
	})

	// RUN resumes publishing.
	e.SetRunning(true)
	waitFor(t, func() bool { return e.Snapshot().Published > pub })
}

func TestMatrixAndLEDService(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)

	// Queue a matrix request, then service the boundary from another
	// goroutine's perspective: ReadMatrix blocks until serviceCommands runs.
	done := make(chan [5]uint16, 1)
	go func() {
		m, ok := e.ReadMatrix()
		if ok {
			done <- m
		}
		close(done)
	}()
	waitFor(t, func() bool { return len(e.matrixReq) == 1 })
	e.SetLEDs(0x2030)
	fb.clearWrites()
	e.serviceCommands()
	if _, ok := <-done; !ok {
		t.Fatal("ReadMatrix not served at the boundary")
	}
	// LED strobe: 0x0b=0, 0x0a=hi, 0x09=lo, 0x0b=1 — one indivisible burst.
	wantWrites(t, fb.snapWrites(), []wr{
		{3, 0x0b, 0}, {3, 0x0a, 0x20}, {3, 0x09, 0x30}, {3, 0x0b, 1},
	})

	// Same LED word again: compare-on-change suppresses the strobe.
	fb.clearWrites()
	e.SetLEDs(0x2030)
	e.serviceCommands()
	if n := len(fb.snapWrites()); n != 0 {
		t.Fatalf("identical LED word re-strobed %d writes", n)
	}
}

func TestRunStopsAtBoundary(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	go e.Run()
	waitFor(t, func() bool { return e.Snapshot().Frames >= 1 })
	if !e.Stop(2 * time.Second) {
		t.Fatal("engine did not stop at the boundary")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached within 5s")
}
