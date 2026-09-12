package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/bus"
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
	plane uint8
	sel   uint16
	val   uint16
}

// fakeBus models the acq2 default fabric well enough to drive the FSM: the
// identity registers answer the schema, STATUS_A/FILL respond to the armed
// state, BURST_REMAIN/BURST serve the programmed record only after HALT (or
// continuously in STREAM mode), and every write is recorded for order
// assertions.
type fakeBus struct {
	mu          sync.Mutex
	writes      []wr
	doneOnGo    bool // STATUS_A.DONE asserts while armed
	trigOnGo    bool // STATUS_A.TRIG asserts while armed
	validOnGo   bool // STATUS_A.VALID asserts while armed (AUTO completion)
	fillAdvance bool
	confDone    uint16 // CS3 0x07 value; bit7 = CONF_DONE
	identityOK  bool   // answer the schema identity words
	wave        func(i int) (c1, c2 uint8)

	armed, halted, stream bool
	run                   uint16
	decim                 uint32
	pre, post             uint32
	fill                  uint16
	drainN                int  // words popped since the last HALT / GO
	earlyDrain            bool // a BURST pop before HALT outside STREAM = the CPU-hang trap
	popNoRemain           bool // a pop beyond what BURST_REMAIN reported
	remainLast            int  // words the last BURST_REMAIN read promised
	streamAvail           int  // STREAM: words available (grows per BURST_REMAIN read)
	armCount              int  // GO writes; lets waves shift phase per capture
}

func newFakeBus() *fakeBus {
	return &fakeBus{
		doneOnGo:    true,
		fillAdvance: true,
		confDone:    0x80,
		identityOK:  true,
		// Square wave, period 256: edges everywhere, ptp = 144.
		wave: func(i int) (uint8, uint8) {
			if (i/128)%2 == 0 {
				return 200, 60
			}
			return 56, 190
		},
	}
}

// record is the number of words the fabric finalizes for the programmed
// PRETRIG/POSTTRIG, with the clamp default.v applies (dual mode): pre <=
// PRETRIG_MAX, post <= PRETRIG_MAX - pre, post >= 1. Programming more than
// the fabric holds therefore yields a SHORTER record than asked for — the
// mismatch the engine must never create.
func (f *fakeBus) record() int {
	pre, post := f.pre, f.post
	if pre > iface.PretrigMax {
		pre = iface.PretrigMax
	}
	if room := uint32(iface.PretrigMax) - pre; post > room {
		post = room
	}
	if post == 0 {
		post = 1
	}
	return int(pre + post)
}

func (f *fakeBus) Read(plane uint8, sel uint16) (uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if plane == bus.PlaneCS3 {
		if sel == bus.CS3ConfigPort {
			return f.confDone, nil
		}
		return 0, nil
	}
	switch iface.MaskSel(sel) {
	case iface.SelBuildidLo:
		if f.identityOK {
			return iface.BuildIDLo, nil
		}
	case iface.SelBuildidHi:
		if f.identityOK {
			return iface.BuildIDHi, nil
		}
	case iface.SelVersion:
		if f.identityOK {
			return iface.VersionMagic, nil
		}
	case iface.SelFabricId:
		if f.identityOK {
			return iface.FabricID, nil
		}
	case iface.SelStatusA:
		var s uint16
		if f.armed && f.doneOnGo {
			s |= iface.StatusADoneMask
		}
		if f.armed && f.trigOnGo {
			s |= iface.StatusATrigMask
		}
		if f.armed && f.validOnGo {
			s |= iface.StatusAValidMask
		}
		if f.armed && !f.halted {
			s |= iface.StatusAArmedMask
		}
		return s, nil
	case iface.SelFill:
		if f.armed && !f.halted && f.fillAdvance {
			if f.fill > 0xffff-64 {
				f.fill = 0xffff
			} else {
				f.fill += 64
			}
		}
		return f.fill, nil
	case iface.SelTrigposHi:
		return 0x2790, nil
	case iface.SelTrigposLo:
		return 0x8000, nil
	case iface.SelBurstRemain:
		if f.stream && f.armed {
			f.streamAvail += 64
			if f.streamAvail > 0x7fff {
				f.streamAvail = 0x7fff
			}
			f.remainLast = f.streamAvail
			return iface.BurstRemainReadyMask | uint16(f.streamAvail), nil
		}
		if !f.halted {
			f.remainLast = 0
			return 0, nil
		}
		rem := f.record() - f.drainN
		if rem < 0 {
			rem = 0
		}
		f.remainLast = rem
		return iface.BurstRemainReadyMask | uint16(rem), nil
	case iface.SelBurst, iface.SelBurstAlias:
		return f.pop(), nil
	}
	return 0, nil
}

// pop is one BURST read (caller holds f.mu).
func (f *fakeBus) pop() uint16 {
	if !f.halted && !f.stream {
		f.earlyDrain = true
	}
	if f.remainLast <= 0 {
		f.popNoRemain = true
	} else {
		f.remainLast--
	}
	if f.stream && f.streamAvail > 0 {
		f.streamAvail--
	}
	c1, c2 := f.wave(f.drainN)
	f.drainN++
	return uint16(c1)<<8 | uint16(c2)
}

func (f *fakeBus) Write(plane uint8, sel, val uint16) error {
	if !bus.Writable(plane, sel) {
		return fmt.Errorf("fake: write to non-writable cs%d %#04x", plane, sel)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, wr{plane, sel, val})
	if plane != bus.PlaneCS1 {
		return nil
	}
	switch iface.MaskSel(sel) {
	case iface.SelOpcode:
		switch val {
		case iface.OpGo:
			if f.run&iface.RunRunMask != 0 {
				f.armed, f.halted, f.fill, f.drainN = true, false, 0, 0
				f.armCount++
			}
		case iface.OpHalt:
			f.halted, f.drainN = true, 0
		case iface.OpReset:
			f.armed, f.halted, f.drainN, f.streamAvail = false, false, 0, 0
		}
	case iface.SelRun:
		f.run = val
		f.stream = val&iface.RunStreamMask != 0
	case iface.SelDecimLo:
		f.decim = f.decim&0xffff0000 | uint32(val)
	case iface.SelDecimHi:
		f.decim = f.decim&0xffff | uint32(val)<<16
	case iface.SelPretrigLo:
		f.pre = f.pre&0xffff0000 | uint32(val)
	case iface.SelPretrigHi:
		f.pre = f.pre&0xffff | uint32(val)<<16
	case iface.SelPosttrigLo:
		f.post = f.post&0xffff0000 | uint32(val)
	case iface.SelPosttrigHi:
		f.post = f.post&0xffff | uint32(val)<<16
	}
	return nil
}

func (f *fakeBus) RawWrite(sel, val uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, wr{bus.PlaneCS1, sel, val})
	return nil
}

func (f *fakeBus) BurstInto(c1, c2 []uint8, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < n; i++ {
		w := f.pop()
		c1[i] = uint8(w >> 8)
		c2[i] = uint8(w)
	}
}

func (f *fakeBus) PopWords(sel uint16, dst []uint16, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < n; i++ {
		if m := iface.MaskSel(sel); m == iface.SelBurst || m == iface.SelBurstAlias {
			dst[i] = f.pop()
		} else {
			dst[i] = 0
		}
	}
}

func (f *fakeBus) FastDrain() bool { return true }

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

// bringUpWords is the register program bringUp writes for a band at record
// depth cols: the sequence the test suite pins (workplan §2 order).
func bringUpWords(b Band, cols int, run, acq, lvl uint16) []wr {
	pre, post := uint32(cols/2), uint32(cols-cols/2)
	d := b.Decim()
	return []wr{
		{1, iface.SelOpcode, iface.OpReset},
		{1, iface.SelRun, run},
		{1, iface.SelDecimLo, uint16(d)}, {1, iface.SelDecimHi, uint16(d >> 16)},
		{1, iface.SelPretrigLo, uint16(pre)}, {1, iface.SelPretrigHi, uint16(pre >> 16)},
		{1, iface.SelPosttrigLo, uint16(post)}, {1, iface.SelPosttrigHi, uint16(post >> 16)},
		{1, iface.SelAcqCtrl, acq},
		{1, iface.SelTrigLevel, lvl},
		// schema v3: ADC source, full drain window
		{1, iface.SelIlCtrl, 0}, {1, iface.SelDrainStart, 0}, {1, iface.SelDrainLen, 0},
	}
}

const (
	runAuto    = iface.RunRunMask     // MODE=auto, RUN
	runNorm    = iface.RunRunMask | 1 // MODE=norm, RUN
	acqDefault = iface.AcqCtrlEncEnMask | uint16(encRate)<<iface.AcqCtrlEncRateShift |
		uint16(trigHyst)<<iface.AcqCtrlTrigHystShift | iface.AcqCtrlPairEnMask // CH1, rising
)

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
	// Workplan §2 order: RESET, RUN, DECIM, PRETRIG, POSTTRIG, then the trigger
	// words. 500 µs/div: nominal 800 ns → DECIM 160 at the 5 ns base tick;
	// record = decimDrain split half/half; trigger level unset → mid-scale.
	b, _ := PlanTdiv(500e-6)
	if b.Decim() != 160 {
		t.Fatalf("Decim(500µs) = %d, want 160", b.Decim())
	}
	wantWrites(t, fb.snapWrites(), bringUpWords(b, decimDrain, runAuto, acqDefault, trigLevelUnset))
	if fb.pre != decimDrain/2 || fb.post != decimDrain/2 || fb.decim != 160 {
		t.Fatalf("fabric program: pre=%d post=%d decim=%d", fb.pre, fb.post, fb.decim)
	}
	// NORM + CH2 falling: the mode and trigger bits follow.
	e.SetNorm(true)
	e.SetTrigSource(1)
	e.SetTrigSlope(false)
	fb.clearWrites()
	e.bringUp()
	wantWrites(t, fb.snapWrites(), bringUpWords(b, decimDrain, runNorm,
		acqDefault|iface.AcqCtrlTrigSrcMask|iface.AcqCtrlTrigSlopeMask, trigLevelUnset))
	// Native-fast rows program the full record and DECIM=1 (5 ns delivered).
	if _, ok := e.SetTdiv(1e-9); !ok {
		t.Fatal("SetTdiv(1ns)")
	}
	e.mu.Lock()
	e.band, e.pendSet = e.pendBand, false
	e.mu.Unlock()
	fb.clearWrites()
	e.bringUp()
	if fb.decim != 1 || int(fb.pre+fb.post) != maxRecordCols {
		t.Fatalf("native-fast program: decim=%d record=%d", fb.decim, fb.pre+fb.post)
	}
	if got := e.band.CaptureIntervalNs(); got != baseTickNs {
		t.Fatalf("1 ns/div delivers %g ns/sample, want the %g ns base tick", got, baseTickNs)
	}
}

func TestArmSequence(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	// A never-programmed engine programs the record first (the depth it will
	// drain), then GO; a programmed one arms with the single GO strobe.
	e.armEngine()
	b, _ := PlanTdiv(500e-6)
	wantWrites(t, fb.snapWrites(), append(bringUpWords(b, decimDrain, runAuto, acqDefault, trigLevelUnset),
		wr{1, iface.SelOpcode, iface.OpGo}))
	fb.clearWrites()
	e.armEngine()
	wantWrites(t, fb.snapWrites(), []wr{{1, iface.SelOpcode, iface.OpGo}})
	// SINGLE deepens the drain to the full record: the next arm re-programs.
	e.SetSingle()
	fb.clearWrites()
	e.armEngine()
	got := fb.snapWrites()
	if n := len(bringUpWords(b, decimDrain, runAuto, acqDefault, trigLevelUnset)) + 1; len(got) != n || fb.pre+fb.post != maxRecordCols || got[n-1].val != iface.OpGo {
		t.Fatalf("SINGLE re-program: %d writes, record=%d", len(got), fb.pre+fb.post)
	}
	// The vendor word 0x21 is undecoded in schema v3 (RawWrite only): the
	// fake, like the fabric, arms only on OP_GO.
	arms := fb.armCount
	if err := fb.RawWrite(0x21, 0x00c3); err != nil || fb.armCount != arms {
		t.Fatalf("vendor 0x21=0xC3 must not arm (err=%v arms %d→%d)", err, arms, fb.armCount)
	}
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
		return f != nil && f.TdivS == 1e-6 && f.Valid == maxRecordCols
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
		if w.plane == 1 && w.sel == iface.SelOpcode && w.val == iface.OpGo {
			t.Fatalf("GO written while stopped: %#v", w)
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
	e.bringUp()

	// The default image carries no key-matrix block: ReadMatrix reports not-ok
	// without touching the bus, so the panel keeps its poll fallback.
	fb.clearWrites()
	if _, ok := e.ReadMatrix(); ok {
		t.Fatal("ReadMatrix must report not-ok on the default image")
	}
	if n := len(fb.snapWrites()); n != 0 {
		t.Fatalf("ReadMatrix touched the bus (%d writes)", n)
	}
	e.SetLEDs(0x2030)
	e.serviceCommands()
	// LED strobe (MAX V CS3): 0x0b=0, 0x0a=hi, 0x09=lo, 0x0b=1 — one indivisible burst.
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
