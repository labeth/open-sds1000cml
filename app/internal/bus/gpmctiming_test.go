// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/iface"
)

// TRLC-LINKS: REQ-SDS-131
func TestCS1TimingFieldCodec(t *testing.T) {
	f := FactoryCS1Timing.Fields()
	// fpga-specs 10 §4.3: CONFIG2 0x00141400, CONFIG4 0x10041004, CONFIG5 0x010d141f, CONFIG6 0x060005c1.
	want := TimingFields{RdCycle: 31, RdAccess: 13, OEOn: 4, OEOff: 16, CSOn: 0, CSRdOff: 20, Gap: 5, SameCSEn: true,
		WrCycle: 20, WrAccess: 6, WEOn: 4, WEOff: 16, CSWrOff: 20}
	if f != want {
		t.Fatalf("factory fields %+v, want %+v", f, want)
	}
	n := FactoryCS1Timing.WithRdAccess(9).WithRdCycle(12).WithOEOff(11).WithCSRdOff(12).WithGap(4)
	if n.RdAccess() != 9 || n.RdCycle() != 12 || n.OEOff() != 11 || n.CSRdOff() != 12 || n.Gap() != 4 || !n.SameCSEn() {
		t.Fatalf("setters: %s", n)
	}
	if n.WrCycle() != 20 || n.WEOff() != 16 || n.CSWrOff() != 20 || n.Config1 != FactoryCS1Timing.Config1 {
		t.Fatalf("setters disturbed the write side / CONFIG1: %+v", n)
	}
	if n.Config5 != 0x0109140c {
		t.Fatalf("CONFIG5 = %#08x, want 0x0109140c", n.Config5)
	}
	if err := n.Validate(); err != nil {
		t.Fatalf("valid timing refused: %v", err)
	}
	if FactoryCS1Timing.forCycle(15).OEOff() != 15 || FactoryCS1Timing.forCycle(15).CSRdOff() != 15 ||
		FactoryCS1Timing.forCycle(25).OEOff() != 16 || FactoryCS1Timing.forCycle(25).CSRdOff() != 20 {
		t.Fatalf("forCycle: %s / %s", FactoryCS1Timing.forCycle(15), FactoryCS1Timing.forCycle(25))
	}
	if FactoryCS1Timing.ReadTicks() != 36 {
		t.Fatalf("ReadTicks = %d, want 36 (31 + gap 5 = the 360 ns/word of the R0 report at 100 MHz)", FactoryCS1Timing.ReadTicks())
	}
}

// TRLC-LINKS: REQ-SDS-131
func TestCS1TimingValidate(t *testing.T) {
	bad := []CS1Timing{
		FactoryCS1Timing.WithRdAccess(31),                                                    // access >= cycle
		FactoryCS1Timing.WithRdAccess(6),                                                     // below OEON + 3
		FactoryCS1Timing.WithRdAccess(16),                                                    // not latched before OEOFF
		FactoryCS1Timing.WithRdCycle(15),                                                     // OEOFF beyond the cycle
		FactoryCS1Timing.WithRdCycle(18),                                                     // CSRDOFF beyond the cycle
		FactoryCS1Timing.WithCSRdOff(15),                                                     // nCS releases before nOE
		FactoryCS1Timing.WithOEOn(3).WithRdAccess(6),                                         // fine for the fabric... no: CSON 0 <= 3 ok, access 6 = 3+3 ok, but access must be >= OEON+3 = 6 → valid; keep as a positive case below
		{Config2: 0x00141400, Config4: 0x10041004, Config5: 0x010d141f, Config6: 0x06000541}, // gap enable off
		FactoryCS1Timing.WithGap(0),
	}
	for i, tm := range bad {
		if i == 6 {
			if err := tm.Validate(); err != nil {
				t.Errorf("case %d (%s) should be valid: %v", i, tm, err)
			}
			continue
		}
		if err := tm.Validate(); err == nil {
			t.Errorf("case %d (%s) accepted", i, tm)
		}
	}
	if err := FactoryCS1Timing.Validate(); err != nil {
		t.Fatalf("factory timing refused: %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-131
func TestApplyTimingSequence(t *testing.T) {
	g := &fakeGPMC{regs: map[uint32]uint32{gpmcConfig1: 0x00001001, gpmcConfig2: 0x00141400, gpmcConfig3: 0x00020201,
		gpmcConfig4: 0x10041004, gpmcConfig5: 0x010d141f, gpmcConfig6: 0x060005c1, gpmcConfig7: 0x00000F41}}
	got := readTiming(g)
	if got != FactoryCS1Timing {
		t.Fatalf("readTiming %+v", got)
	}
	n := FactoryCS1Timing.WithRdAccess(9).forCycle(14)
	if err := applyTiming(g, n, true); err != nil {
		t.Fatal(err)
	}
	if g.regs[gpmcConfig5] != n.Config5 || g.regs[gpmcConfig4] != n.Config4 || g.regs[gpmcConfig2] != n.Config2 {
		t.Fatalf("timing not written: %+v", readTiming(g))
	}
	if g.regs[gpmcConfig7] != 0x00000F41 || g.regs[gpmcConfig1] != 0x00001001 {
		t.Fatalf("CONFIG1/7 disturbed: %+v", readTiming(g))
	}
	want := []string{"csvalid-off", "cfg6", "csvalid-on"}
	if len(g.events) != 3 || g.events[0] != want[0] || g.events[1] != want[1] || g.events[2] != want[2] {
		t.Fatalf("events %v, want %v (CSVALID quiesced around the retiming)", g.events, want)
	}
	if err := applyTiming(g, FactoryCS1Timing.WithRdAccess(30), true); err == nil {
		t.Fatal("invalid timing written")
	}
	if err := applyTiming(g, FactoryCS1Timing.WithRdAccess(30), false); err != nil {
		t.Fatalf("restore path must not validate: %v", err)
	}
}

// fakePort is a timing port with floors: below them the fabric's pops
// corrupt. It also drives the fake clock: each drained word costs
// (RDCYCLETIME + gap) ticks at 100 MHz, so the FCLK estimate can be checked.
// TRLC-LINKS: REQ-SDS-131
type fakePort struct {
	fab         *fakeFab
	cur         CS1Timing
	minAccess   uint32
	minCycle    uint32
	minGap      uint32
	applies     int
	validations int
}

// TRLC-LINKS: REQ-SDS-131
func (p *fakePort) Read() (CS1Timing, error) { return p.cur, nil }
// TRLC-LINKS: REQ-SDS-131
func (p *fakePort) set(t CS1Timing) {
	p.cur = t
	p.applies++
	p.fab.corrupt = t.RdAccess() < p.minAccess || t.RdCycle() < p.minCycle || t.Gap() < p.minGap
}
// TRLC-LINKS: REQ-SDS-131
func (p *fakePort) Apply(t CS1Timing) error {
	p.validations++
	if err := t.Validate(); err != nil {
		return err
	}
	p.set(t)
	return nil
}
// TRLC-LINKS: REQ-SDS-131
func (p *fakePort) Restore(t CS1Timing) error { p.set(t); return nil }

// tickClock advances 10 ns per GPMC tick per popped word: the fake fabric's
// pop counter drives it.
// TRLC-LINKS: REQ-SDS-131
type tickClock struct {
	fab  *fakeFab
	port *fakePort
	t    time.Time
	last uint16
}

// TRLC-LINKS: REQ-SDS-131
func (c *tickClock) now() time.Time {
	c.fab.mu.Lock()
	pops := c.fab.pops
	c.fab.mu.Unlock()
	d := int(uint16(pops - c.last))
	c.last = pops
	c.t = c.t.Add(time.Duration(d) * time.Duration(c.port.cur.ReadTicks()) * 10 * time.Nanosecond)
	return c.t
}

// TRLC-LINKS: REQ-SDS-131
func TestSweepFindsFloorsAndRestores(t *testing.T) {
	fab := newFakeFab()
	port := &fakePort{fab: fab, cur: FactoryCS1Timing, minAccess: 9, minCycle: 12, minGap: 4}
	clk := &tickClock{fab: fab, port: port, t: time.Unix(0, 0)}
	sw := &Sweeper{Port: port, Sleep: noSleep, now: clk.now, Logf: t.Logf,
		o: SweepOptions{Words: 500, Drains: 2, Blocks: 3, VerifyWords: 5000, SweepGap: true}}
	res, err := sw.Run(fab)
	if err != nil {
		t.Fatalf("sweep: %v (%s)", err, res.Err)
	}
	if !res.OK || !res.Restored || port.cur != FactoryCS1Timing {
		t.Fatalf("ok=%v restored=%v cur=%s", res.OK, res.Restored, port.cur)
	}
	if res.FloorAccess != 9 || res.FloorCycle != 12 || res.FloorGap != 4 {
		t.Fatalf("floors access=%d cycle=%d gap=%d, want 9/12/4", res.FloorAccess, res.FloorCycle, res.FloorGap)
	}
	c := res.Chosen
	if c.RdAccess != 10 || c.RdCycle != 13 || c.Gap != 5 || c.OEOff != 13 || c.CSRdOff != 13 {
		t.Fatalf("chosen %+v, want one tick above every floor with OEOFF/CSRDOFF at the cycle end", c)
	}
	// The failing setting is the last of each phase; every earlier one passed.
	for _, ph := range [][]SettingResult{res.Access, res.Cycle, res.GapPhase} {
		for i, s := range ph {
			if s.Pass != (i < len(ph)-1) {
				t.Fatalf("phase entry %d/%d pass=%v: %+v", i, len(ph), s.Pass, s)
			}
			if s.Drains != 6 || s.Words != 3000 || !s.BaselineOK || len(s.PerBlock) != 3 {
				t.Fatalf("block accounting: %+v", s)
			}
		}
	}
	if last := res.Access[len(res.Access)-1]; last.FirstBad == nil || last.Breaks == 0 {
		t.Fatalf("the failing access setting carries no evidence: %+v", last)
	}
	// Verify covered >= VerifyWords at the chosen timing (3 blocks × 4 drains × 500 = 6000).
	if res.Verify.Words < 5000 || !res.Verify.Pass {
		t.Fatalf("verify: %+v", res.Verify)
	}
	// Rates: 36 ticks × 10 ns at the factory timing, 18 at the chosen one; FCLK from the slope.
	if math.Abs(res.Access[0].Baseline-360) > 1e-6 || math.Abs(res.Verify.NsPerWord-180) > 1e-6 {
		t.Fatalf("ns/word baseline=%.1f verify=%.1f, want 360 / 180", res.Access[0].Baseline, res.Verify.NsPerWord)
	}
	if math.Abs(res.FclkMHz-100) > 0.01 {
		t.Fatalf("FCLK estimate %.3f MHz, want 100", res.FclkMHz)
	}
	if res.Verify.WordsPerS < 5.5e6 || res.Verify.WordsPerS > 5.6e6 {
		t.Fatalf("words/s %.0f", res.Verify.WordsPerS)
	}
}

// TRLC-LINKS: REQ-SDS-131
func TestSweepNothingGained(t *testing.T) {
	// Every candidate fails: the chosen timing is the start timing, still restored.
	fab := newFakeFab()
	port := &fakePort{fab: fab, cur: FactoryCS1Timing, minAccess: 13, minCycle: 31, minGap: 5}
	sw := &Sweeper{Port: port, Sleep: noSleep, now: (&tickClock{fab: fab, port: port}).now,
		o: SweepOptions{Words: 300, Drains: 1, VerifyWords: 300}}
	res, err := sw.Run(fab)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChosenRaw != FactoryCS1Timing || !res.OK || len(res.Access) != 1 || len(res.Cycle) != 1 || len(res.GapPhase) != 0 {
		t.Fatalf("chosen %s ok=%v access=%d cycle=%d gap=%d", res.ChosenRaw, res.OK, len(res.Access), len(res.Cycle), len(res.GapPhase))
	}
	if res.FclkMHz != 0 {
		t.Fatalf("FCLK must not be estimated from < 3 clean points: %.1f", res.FclkMHz)
	}
}

// TRLC-LINKS: REQ-SDS-131
func TestSweepAbortsOnBrokenBaseline(t *testing.T) {
	fab := newFakeFab()
	port := &fakePort{fab: fab, cur: FactoryCS1Timing, minAccess: 14} // the baseline itself is below the floor
	sw := &Sweeper{Port: port, Sleep: noSleep, now: (&tickClock{fab: fab, port: port}).now,
		o: SweepOptions{Words: 300, Drains: 1}}
	res, err := sw.Run(fab)
	if err == nil || res.Restored != true || res.OK || port.cur != FactoryCS1Timing {
		t.Fatalf("broken baseline: err=%v restored=%v ok=%v cur=%s", err, res.Restored, res.OK, port.cur)
	}
}

// slowFab is the fake fabric on the ioctl drain (FastDrain false): the step
// budget then holds fewer drains per step.
// TRLC-LINKS: REQ-SDS-131
type slowFab struct{ *fakeFab }

// TRLC-LINKS: REQ-SDS-131
func (slowFab) FastDrain() bool { return false }

// TestSweepStepsRestoreEveryStep is the health-contract property of the step
// machine: every Step is bounded (at most DrainsStep scored drains, sized by
// the drain mode), the start timing is back in the controller when it
// returns, and the step timeout scales with the drain mode.
// TRLC-LINKS: REQ-SDS-131
func TestSweepStepsRestoreEveryStep(t *testing.T) {
	fab := newFakeFab()
	port := &fakePort{fab: fab, cur: FactoryCS1Timing, minAccess: 11, minCycle: 29}
	clk := &tickClock{fab: fab, port: port, t: time.Unix(0, 0)}
	o := SweepOptions{Words: iface.PretrigMax, Drains: 7, Blocks: 3, VerifyWords: 100000}
	sw := &Sweeper{Port: port, Sleep: noSleep, now: clk.now, o: o}
	if got := sw.StepTimeout(); got < 2*time.Second || got > 6*time.Second {
		t.Fatalf("ioctl step timeout %v (unknown mode assumes the slow drain)", got)
	}
	var steps, applies int
	lastApplies := 0
	for {
		done, err := sw.Step(slowFab{fab})
		steps++
		if port.cur != FactoryCS1Timing {
			t.Fatalf("step %d left the controller at %s", steps, port.cur)
		}
		if a := port.applies - lastApplies; a > 2 {
			t.Fatalf("step %d wrote the timing %d times (want apply + restore at most)", steps, a)
		}
		lastApplies = port.applies
		applies = port.applies
		if err != nil {
			t.Fatalf("step %d: %v", steps, err)
		}
		if done {
			break
		}
	}
	res := sw.Result()
	if !res.OK || !res.Restored || res.FastDrain {
		t.Fatalf("ok=%v restored=%v fast=%v", res.OK, res.Restored, res.FastDrain)
	}
	// 20478 words × 2.5 µs = 51 ms per drain: 4 drains fit the 250 ms budget,
	// so a 7-drain half takes two steps; the step timeout follows.
	if res.DrainsStep != 4 {
		t.Fatalf("drains per step %d, want 4 on the ioctl drain", res.DrainsStep)
	}
	if got := sw.StepTimeout(); got < 2*time.Second || got > 6*time.Second {
		t.Fatalf("ioctl step timeout %v", got)
	}
	// Three settings per phase (12, 11 clean, 10 failing; 30, 29, 28) × 3
	// blocks × 2 halves × 2 steps, plus the verify setting (100000 / (3 ×
	// 20478) → 2 drains per half: 1 step each) = 6 × 12 + 6 = 78 steps and one
	// closing step that only finishes; every candidate half wrote apply +
	// restore, and the finish restored once more.
	if steps != 79 || res.Steps != 78 || applies != 2*(6*3*2+3)+1 {
		t.Fatalf("steps=%d/%d applies=%d", steps, res.Steps, applies)
	}
	for _, s := range append(res.Access, res.Cycle...) {
		if s.Drains != 21 || len(s.PerBlock) != 3 {
			t.Fatalf("setting accounting across steps: %+v", s)
		}
	}
	if res.Chosen.RdAccess != 12 || res.Chosen.RdCycle != 30 {
		t.Fatalf("chosen %+v", res.Chosen)
	}
	// On the EDMA drain (0.4 µs/word) the same 250 ms budget holds 30 drains
	// per step (a whole 7-drain half in one step); the step timeout stays ~2 s
	// because it is derived from the same budget.
	fast := &Sweeper{Port: port, Sleep: noSleep, now: clk.now, o: o}
	if _, err := fast.Step(fab); err != nil {
		t.Fatal(err)
	}
	if fast.Result().DrainsStep != 30 || fast.StepTimeout() < 1500*time.Millisecond || fast.StepTimeout() > 3*time.Second {
		t.Fatalf("EDMA: drains/step %d timeout %v vs ioctl %v", fast.Result().DrainsStep, fast.StepTimeout(), sw.StepTimeout())
	}
	if port.cur != FactoryCS1Timing || fast.Phase() != "access" || fast.Setting() == "" {
		t.Fatalf("after one EDMA step: cur=%s phase=%s setting=%q", port.cur, fast.Phase(), fast.Setting())
	}
}
