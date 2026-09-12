// GPMC CS1 read timing (AM335x TRM 7.1.5 GPMC_CONFIG2..6; fpga-specs 10 §4.3,
// 12 §5.5) and the timing sweep of rung R1 (06-TIERS §0, §6).
//
// The bootloader leaves CS1 at RDCYCLETIME 31 / RDACCESSTIME 13 (the "factory"
// timing, ~360 ns per word at the shipped cycle gap). The fabric's read
// through-path budget is 30 ns (default.sdc: RDACCESSTIME >= OEONTIME + 3
// ticks at 100 MHz), so the controller can go markedly faster — but only the
// ramp harness can prove it: a too-early latch is invisible on flat data. The
// sweep here lowers RDACCESSTIME, then RDCYCLETIME (with OEOFFTIME and
// CSRDOFFTIME tracking the cycle end), optionally CYCLE2CYCLEDELAY, draining
// a frozen TSRC ramp record at every setting in alternating blocks against the
// starting timing, and records the floor (last clean value) of every knob.
// The chosen timing is one tick above the floor on every knob. Nothing here
// is persisted or kept applied: the sweep is a step machine (Sweeper.Step),
// every step is one bounded Engine.Exec that restores the timing it started
// from before it returns, so the health token keeps beating and no step can
// strand the controller at a candidate; persistence and the boot-time gate
// are in gpmcpersist.go.
//
// The write-side fields, CONFIG1 (WAIT monitoring, device size) and CONFIG7
// (the CS1 base address) are never touched. Only CS1 is ever written — never
// NAND CS0 and never the shared prefetch engine (fpga-specs 10 §2.4).
package bus

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"open-sds/app/internal/iface"
)

const (
	gpmcConfig1 = 0x90 // GPMC_CONFIG1_1 (read for the report only)
	gpmcConfig2 = 0x94 // CSONTIME[3:0] CSEXTRADELAY[7] CSRDOFFTIME[12:8] CSWROFFTIME[20:16]
	gpmcConfig3 = 0x98 // ADV timings (read for the report only)
	gpmcConfig4 = 0x9C // OEONTIME[3:0] OEEXTRADELAY[7] OEOFFTIME[12:8] WEONTIME[19:16] WEEXTRADELAY[23] WEOFFTIME[28:24]
	gpmcConfig5 = 0xA0 // RDCYCLETIME[4:0] WRCYCLETIME[12:8] RDACCESSTIME[20:16] PAGEBURSTACCESSTIME[27:24]
	// gpmcConfig6 (0xA4): BUSTURNAROUND[3:0] CYCLE2CYCLEDIFFCSEN[6] CYCLE2CYCLESAMECSEN[7]
	// CYCLE2CYCLEDELAY[11:8] WRDATAONADMUXBUS[19:16] WRACCESSTIME[28:24]; gpmcConfig7 (0xA8): CSVALID[6].

	// fabricReadTicks is the fabric's read through-path budget in GPMC FCLK
	// ticks (default.sdc: 30 ns at the assumed 100 MHz FCLK): RDACCESSTIME
	// must sit at least this many ticks after OEONTIME.
	fabricReadTicks = 3
)

// CS1Timing is the CS1 timing configuration: the four words the sweep may
// change (CONFIG2/4/5/6) plus CONFIG1/3/7 as read, for the report.
type CS1Timing struct {
	Config1 uint32 `json:"config1"`
	Config2 uint32 `json:"config2"`
	Config3 uint32 `json:"config3"`
	Config4 uint32 `json:"config4"`
	Config5 uint32 `json:"config5"`
	Config6 uint32 `json:"config6"`
	Config7 uint32 `json:"config7"`
}

// FactoryCS1Timing is the bootloader's CS1 timing on this unit (fpga-specs
// 10 §4.3 table) with the shipped cycle gap 5 (EnableEDMA). Used only as the
// fallback when the boot-time read is unavailable in a report.
var FactoryCS1Timing = CS1Timing{Config1: 0x00001001, Config2: 0x00141400, Config3: 0x00020201,
	Config4: 0x10041004, Config5: 0x010d141f, Config6: 0x060005c1, Config7: 0x00000F41}

func bits(w uint32, shift, width uint) uint32 { return (w >> shift) & (1<<width - 1) }
func setBits(w *uint32, shift, width uint, v uint32) {
	m := uint32(1<<width-1) << shift
	*w = (*w &^ m) | (v << shift & m)
}

// Read-side fields (getters).
func (t CS1Timing) CSOn() uint32       { return bits(t.Config2, 0, 4) }
func (t CS1Timing) CSRdOff() uint32    { return bits(t.Config2, 8, 5) }
func (t CS1Timing) CSWrOff() uint32    { return bits(t.Config2, 16, 5) }
func (t CS1Timing) OEOn() uint32       { return bits(t.Config4, 0, 4) }
func (t CS1Timing) OEOff() uint32      { return bits(t.Config4, 8, 5) }
func (t CS1Timing) WEOn() uint32       { return bits(t.Config4, 16, 4) }
func (t CS1Timing) WEOff() uint32      { return bits(t.Config4, 24, 5) }
func (t CS1Timing) RdCycle() uint32    { return bits(t.Config5, 0, 5) }
func (t CS1Timing) WrCycle() uint32    { return bits(t.Config5, 8, 5) }
func (t CS1Timing) RdAccess() uint32   { return bits(t.Config5, 16, 5) }
func (t CS1Timing) Gap() uint32        { return bits(t.Config6, 8, 4) } // CYCLE2CYCLEDELAY
func (t CS1Timing) SameCSEn() bool     { return bits(t.Config6, 7, 1) == 1 }
func (t CS1Timing) WrAccess() uint32   { return bits(t.Config6, 24, 5) }
func (t CS1Timing) Turnaround() uint32 { return bits(t.Config6, 0, 4) }

// Setters return a copy with one field replaced (values masked to the field).
func (t CS1Timing) WithRdAccess(v uint32) CS1Timing { setBits(&t.Config5, 16, 5, v); return t }
func (t CS1Timing) WithRdCycle(v uint32) CS1Timing  { setBits(&t.Config5, 0, 5, v); return t }
func (t CS1Timing) WithOEOn(v uint32) CS1Timing     { setBits(&t.Config4, 0, 4, v); return t }
func (t CS1Timing) WithOEOff(v uint32) CS1Timing    { setBits(&t.Config4, 8, 5, v); return t }
func (t CS1Timing) WithCSRdOff(v uint32) CS1Timing  { setBits(&t.Config2, 8, 5, v); return t }
func (t CS1Timing) WithGap(v uint32) CS1Timing {
	setBits(&t.Config6, 8, 4, v)
	setBits(&t.Config6, 7, 1, 1) // the same-CS gap is what makes the pop port see a fresh nOE
	return t
}

// TimingFields is the decoded read-side view (JSON / logs).
type TimingFields struct {
	RdCycle  uint32 `json:"rd_cycle"`
	RdAccess uint32 `json:"rd_access"`
	OEOn     uint32 `json:"oe_on"`
	OEOff    uint32 `json:"oe_off"`
	CSOn     uint32 `json:"cs_on"`
	CSRdOff  uint32 `json:"cs_rd_off"`
	Gap      uint32 `json:"gap"`
	SameCSEn bool   `json:"same_cs_en"`
	WrCycle  uint32 `json:"wr_cycle"`
	WrAccess uint32 `json:"wr_access"`
	WEOn     uint32 `json:"we_on"`
	WEOff    uint32 `json:"we_off"`
	CSWrOff  uint32 `json:"cs_wr_off"`
}

// Fields decodes the timing.
func (t CS1Timing) Fields() TimingFields {
	return TimingFields{RdCycle: t.RdCycle(), RdAccess: t.RdAccess(), OEOn: t.OEOn(), OEOff: t.OEOff(),
		CSOn: t.CSOn(), CSRdOff: t.CSRdOff(), Gap: t.Gap(), SameCSEn: t.SameCSEn(),
		WrCycle: t.WrCycle(), WrAccess: t.WrAccess(), WEOn: t.WEOn(), WEOff: t.WEOff(), CSWrOff: t.CSWrOff()}
}

// String is the compact read-side summary used in logs.
func (t CS1Timing) String() string {
	return fmt.Sprintf("rdcycle=%d rdaccess=%d oe=%d..%d cs=%d..%d gap=%d", t.RdCycle(), t.RdAccess(),
		t.OEOn(), t.OEOff(), t.CSOn(), t.CSRdOff(), t.Gap())
}

// ReadTicks is the nominal FCLK ticks per read cycle including the gap (the
// quantity the ns/word measurement scales with).
func (t CS1Timing) ReadTicks() uint32 { return t.RdCycle() + t.Gap() }

// Validate checks the read-side relations the controller and the fabric need
// (TRM 7.1.3.3 read timing; default.sdc's through-path budget). The write
// side is not checked: the sweep never changes it.
func (t CS1Timing) Validate() error {
	c, a, oeOn, oeOff, csOn, csOff := t.RdCycle(), t.RdAccess(), t.OEOn(), t.OEOff(), t.CSOn(), t.CSRdOff()
	switch {
	case c == 0 || a == 0 || oeOff == 0 || csOff == 0:
		return fmt.Errorf("gpmc timing: zero cycle/access/off time (%s)", t)
	case a >= c:
		return fmt.Errorf("gpmc timing: RDACCESSTIME %d must be below RDCYCLETIME %d", a, c)
	case a < oeOn+fabricReadTicks:
		return fmt.Errorf("gpmc timing: RDACCESSTIME %d below OEONTIME %d + %d (fabric read through-path)", a, oeOn, fabricReadTicks)
	case a >= oeOff:
		return fmt.Errorf("gpmc timing: RDACCESSTIME %d must be latched before OEOFFTIME %d", a, oeOff)
	case oeOff > c:
		return fmt.Errorf("gpmc timing: OEOFFTIME %d beyond RDCYCLETIME %d", oeOff, c)
	case csOff > c:
		return fmt.Errorf("gpmc timing: CSRDOFFTIME %d beyond RDCYCLETIME %d", csOff, c)
	case csOff < oeOff:
		return fmt.Errorf("gpmc timing: CSRDOFFTIME %d before OEOFFTIME %d (nCS must cover nOE)", csOff, oeOff)
	case csOn > oeOn:
		return fmt.Errorf("gpmc timing: CSONTIME %d after OEONTIME %d", csOn, oeOn)
	case !t.SameCSEn() || t.Gap() == 0:
		return fmt.Errorf("gpmc timing: the same-CS cycle gap is off (CONFIG6 %#08x) — the pop port needs it", t.Config6)
	}
	return nil
}

// forCycle derives the candidate for a shorter read cycle: OEOFFTIME and
// CSRDOFFTIME never extend past the cycle end (they keep their factory
// offsets otherwise).
func (t CS1Timing) forCycle(c uint32) CS1Timing {
	n := t.WithRdCycle(c)
	if n.OEOff() > c {
		n = n.WithOEOff(c)
	}
	if n.CSRdOff() > c {
		n = n.WithCSRdOff(c)
	}
	return n
}

// TimingPort reads and writes the CS1 timing: the /dev/mem mapping on the
// device, a fake in tests.
type TimingPort interface {
	Read() (CS1Timing, error)
	// Apply validates (CS1Timing.Validate) and writes CONFIG2/4/5/6.
	Apply(CS1Timing) error
	// Restore writes a timing previously read from the controller without
	// validation (the boot timing may predate the cycle gap).
	Restore(CS1Timing) error
}

// readTiming / applyTiming operate on a mapped register block.
func readTiming(r regs32) CS1Timing {
	return CS1Timing{Config1: r.R(gpmcConfig1), Config2: r.R(gpmcConfig2), Config3: r.R(gpmcConfig3),
		Config4: r.R(gpmcConfig4), Config5: r.R(gpmcConfig5), Config6: r.R(gpmcConfig6), Config7: r.R(gpmcConfig7)}
}

// applyTiming writes CONFIG2/4/5/6 with CSVALID quiesced around the retiming
// and restored exactly (the sequence proven for the cycle gap). CONFIG1/3/7
// are left as they are. Every written word is read back.
func applyTiming(r regs32, t CS1Timing, validate bool) error {
	if validate {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	old7 := r.R(gpmcConfig7)
	r.W(gpmcConfig7, old7&^uint32(c7CSValid))
	_ = r.R(gpmcConfig7)
	writes := []struct {
		off uint32
		v   uint32
	}{{gpmcConfig2, t.Config2}, {gpmcConfig4, t.Config4}, {gpmcConfig5, t.Config5}, {gpmcConfig6, t.Config6}}
	for _, w := range writes {
		r.W(w.off, w.v)
	}
	_ = r.R(gpmcConfig6)
	r.W(gpmcConfig7, old7)
	_ = r.R(gpmcConfig7)
	for _, w := range writes {
		if got := r.R(w.off); got != w.v {
			return fmt.Errorf("gpmc: CONFIG at %#x reads %#08x after write, want %#08x", w.off, got, w.v)
		}
	}
	return nil
}

// MemTimingPort is the /dev/mem mapping of the GPMC block (the way fpgaload
// maps the CS3 port and programCS1CycleGap the gap).
type MemTimingPort struct{ m []byte }

// OpenTimingPort maps the GPMC controller registers. Close when done.
func OpenTimingPort() (*MemTimingPort, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), gpmcBase, gpmcLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("gpmc mmap: %w", err)
	}
	return &MemTimingPort{m: m}, nil
}

func (p *MemTimingPort) Read() (CS1Timing, error)  { return readTiming(memRegs{p.m}), nil }
func (p *MemTimingPort) Apply(t CS1Timing) error   { return applyTiming(memRegs{p.m}, t, true) }
func (p *MemTimingPort) Restore(t CS1Timing) error { return applyTiming(memRegs{p.m}, t, false) }
func (p *MemTimingPort) Close()                    { syscall.Munmap(p.m) }

// ---- the sweep ----

// SweepOptions tunes the R1 sweep.
type SweepOptions struct {
	Words       int    `json:"words"`        // record words per drain (default iface.PretrigMax)
	Drains      int    `json:"drains"`       // drains per block and setting (default 7)
	Blocks      int    `json:"blocks"`       // alternating candidate/baseline blocks per setting (default 3, min 3)
	AccessFloor uint32 `json:"access_floor"` // lowest RDACCESSTIME tried (default OEONTIME + 3)
	CycleFloor  uint32 `json:"cycle_floor"`  // lowest RDCYCLETIME tried (default RDACCESSTIME + 1)
	SweepGap    bool   `json:"sweep_gap"`    // also lower CYCLE2CYCLEDELAY (default off: 4 was the first clean gap on the bench)
	GapFloor    uint32 `json:"gap_floor"`    // lowest gap tried (default 2)
	VerifyWords int    `json:"verify_words"` // words drained at the chosen timing in the final check (default 400 000)
	Tsrc        uint16 `json:"tsrc"`         // the pattern (default RAMP)
}

func (o *SweepOptions) defaults(start CS1Timing) {
	if o.Words <= 0 || o.Words > iface.PretrigMax {
		o.Words = iface.PretrigMax
	}
	if o.Drains <= 0 {
		o.Drains = 7
	}
	if o.Blocks < 3 {
		o.Blocks = 3
	}
	if o.AccessFloor == 0 {
		o.AccessFloor = start.OEOn() + fabricReadTicks
	}
	if o.GapFloor == 0 {
		o.GapFloor = 2
	}
	if o.VerifyWords <= 0 {
		o.VerifyWords = 400000
	}
	if o.Tsrc == 0 {
		o.Tsrc = iface.IlCtrlTsrcRamp
	}
}

// SettingResult is one setting's block-alternated score.
type SettingResult struct {
	Timing     TimingFields `json:"timing"`
	Drains     int          `json:"drains"`
	Words      int          `json:"words"`
	Bad        int          `json:"bad_drains"`
	Breaks     int          `json:"breaks"`
	PopsBad    int          `json:"pops_mismatch"`
	Underruns  int          `json:"underruns"`
	Mismatch   int          `json:"mismatch"`
	NsPerWord  float64      `json:"ns_per_word"`
	PerBlock   []float64    `json:"ns_per_word_per_block"`
	Spread     float64      `json:"spread_ns"` // max - min of the per-block ns/word (within-condition spread)
	WordsPerS  float64      `json:"words_per_s"`
	Baseline   float64      `json:"baseline_ns_per_word"` // the interleaved baseline blocks
	BaselineOK bool         `json:"baseline_ok"`
	Pass       bool         `json:"pass"`
	FirstBad   *Exactness   `json:"first_bad,omitempty"`
}

// SweepResult is the R1 report.
type SweepResult struct {
	Options     SweepOptions    `json:"options"`
	Start       TimingFields    `json:"start"` // the timing the sweep began from (restored after every step)
	StartRaw    CS1Timing       `json:"start_raw"`
	FastDrain   bool            `json:"fast_drain"` // EDMA drain in use (the timing matters little on ioctl)
	RecLen      int             `json:"rec_len"`
	Steps       int             `json:"steps"`           // Exec steps taken (each one restores the start timing)
	DrainsStep  int             `json:"drains_per_step"` // drains per step at this drain mode
	Access      []SettingResult `json:"access"`          // phase 1: RDACCESSTIME downwards
	Cycle       []SettingResult `json:"cycle"`           // phase 2: RDCYCLETIME downwards at the chosen access time
	GapPhase    []SettingResult `json:"gap"`             // phase 3 (optional): CYCLE2CYCLEDELAY downwards
	FloorAccess uint32          `json:"floor_access"`    // last clean value (0 = every value failed)
	FloorCycle  uint32          `json:"floor_cycle"`
	FloorGap    uint32          `json:"floor_gap"`
	Chosen      TimingFields    `json:"chosen"` // one tick above every floor
	ChosenRaw   CS1Timing       `json:"chosen_raw"`
	Verify      SettingResult   `json:"verify"`   // the chosen timing over VerifyWords
	FclkMHz     float64         `json:"fclk_mhz"` // from the slope of ns/word vs RDCYCLETIME over the clean cycle points (0 = not estimable)
	Restored    bool            `json:"restored"` // the start timing is back in place
	OK          bool            `json:"ok"`
	Err         string          `json:"err,omitempty"`
}

// Drain cost per word by drain mode, for the step budget and the step
// timeout (measured: EDMA ~0.4 µs/word, ioctl ~2.5 µs/word at the factory
// timing; a faster candidate only shortens them).
const (
	nsPerWordEDMA  = 400
	nsPerWordIoctl = 2500
	// stepDrainBudget bounds the drain time of one step. A step is one
	// Engine.Exec on the bus owner; the health token keys on the owner's
	// beats, and the OTA agent's health timeout is 3 s, so every step must
	// stay well under a second.
	stepDrainBudget = 250 * time.Millisecond
)

// Sweep phases.
const (
	phaseAccess = iota
	phaseCycle
	phaseGap
	phaseVerify
	phaseDone
)

var phaseNames = [...]string{"access", "cycle", "gap", "verify", "done"}

// Sweeper is the R1 sweep as a step machine. Step runs ONE bounded piece of
// the sweep on the bus-owner goroutine (inside one Engine.Exec): it captures
// a fresh test-source record at the start timing, applies the candidate
// (candidate halves only), drains at most DrainsPerStep records, scores them,
// and restores the start timing before returning — whatever happened. The
// state machine (which setting, block, half, drain) lives in the Sweeper and
// is advanced by the caller between steps, so the engine's beats advance and
// the health token stays fresh (05-WORKPLAN §4.3); no step leaves the
// controller at a candidate timing. Run is Step in a loop (tests, bench).
//
// Sleep and now are hooks for tests (default time.Sleep / time.Now).
type Sweeper struct {
	Port  TimingPort
	Sleep func(time.Duration)
	Logf  func(string, ...any)
	now   func() time.Time

	o       SweepOptions
	res     *SweepResult
	start   CS1Timing // the timing the sweep began from: restored after every step
	cur     CS1Timing // the chosen-so-far timing the next phase builds on
	phase   int
	fast    bool
	inited  bool
	perStep int

	// the setting under test
	setting   *SettingResult
	cand      CS1Timing
	blk       int  // block index 0..Blocks-1
	base      bool // false: candidate half of the block, true: baseline half
	drained   int  // drains done in this half
	blkNs     float64
	baseNs    float64
	baseN     int
	drainsPer int // drains per half for this setting (Drains, or the verify count)
	verifying bool
	err       error
}

// NewSweeper prepares a sweep over port. Nothing touches the controller or
// the fabric until the first Step.
func NewSweeper(port TimingPort, o SweepOptions, logf func(string, ...any)) *Sweeper {
	s := &Sweeper{Port: port, o: o, Logf: logf}
	s.init()
	return s
}

func (s *Sweeper) init() {
	if s.Sleep == nil {
		s.Sleep = time.Sleep
	}
	if s.Logf == nil {
		s.Logf = func(string, ...any) {}
	}
	if s.now == nil {
		s.now = time.Now
	}
}

// Result is the report so far (complete once Done). It is the caller's
// responsibility not to read it while a Step is in flight on another
// goroutine.
func (s *Sweeper) Result() *SweepResult { return s.res }

// Done reports whether the sweep has finished (passed, failed or aborted).
func (s *Sweeper) Done() bool { return s.phase == phaseDone }

// Phase names the current phase for progress reports.
func (s *Sweeper) Phase() string { return phaseNames[s.phase] }

// Setting is the candidate under test, "" between settings.
func (s *Sweeper) Setting() string {
	if s.setting == nil {
		return ""
	}
	return s.cand.String()
}

// nsPerWord is the drain cost assumed for the step budget and timeout.
func (s *Sweeper) nsPerWord() time.Duration {
	if s.inited && s.fast {
		return nsPerWordEDMA * time.Nanosecond
	}
	return nsPerWordIoctl * time.Nanosecond // unknown yet: assume the slow mode
}

// drainsPerStep is how many scored drains fit the step budget at this drain
// mode (at least 1, at most one half-block).
func (s *Sweeper) drainsPerStep(words int) int {
	per := time.Duration(words) * s.nsPerWord()
	n := int(stepDrainBudget / per)
	if n < 1 {
		n = 1
	}
	return n
}

// StepTimeout is the Exec timeout for the next step: the drains it will do
// (plus the reference drain and the capture) at the drain mode's cost, with
// a 4× margin and a second for the capture and the register traffic. It
// scales with the drain mode (EDMA vs ioctl) instead of bounding the whole
// sweep.
func (s *Sweeper) StepTimeout() time.Duration {
	words := s.o.Words
	if words <= 0 || words > iface.PretrigMax {
		words = iface.PretrigMax
	}
	n := s.drainsPerStep(words) + 1
	return 4*time.Duration(n)*time.Duration(words)*s.nsPerWord() + time.Second
}

// Step runs one bounded step on the bus-owner goroutine. It returns done
// when the sweep is over; the error (also in Result().Err) ends the sweep.
// The start timing is restored before Step returns, always.
func (s *Sweeper) Step(b Bus) (done bool, err error) {
	s.init()
	if s.phase == phaseDone {
		return true, s.err
	}
	if !s.inited {
		if err := s.begin(b); err != nil {
			return true, s.finish(err)
		}
	}
	if s.setting == nil {
		if !s.nextSetting() {
			return true, s.finish(nil)
		}
	}
	s.res.Steps++
	if err := s.chunk(b); err != nil {
		return true, s.finish(err)
	}
	return false, nil
}

// begin reads the start timing and sizes the steps.
func (s *Sweeper) begin(b Bus) error {
	start, err := s.Port.Read()
	if err != nil {
		return err
	}
	s.o.defaults(start)
	s.start, s.cur, s.fast, s.inited = start, start, b.FastDrain(), true
	s.perStep = s.drainsPerStep(s.o.Words)
	s.res = &SweepResult{Options: s.o, Start: start.Fields(), StartRaw: start, FastDrain: s.fast, DrainsStep: s.perStep,
		FloorAccess: start.RdAccess(), FloorCycle: start.RdCycle(), FloorGap: start.Gap()}
	s.phase = phaseAccess
	s.Logf("gpmc sweep: start %s, %d drains per step (fast drain %v), step timeout %v", start, s.perStep, s.fast, s.StepTimeout())
	return nil
}

// finish closes the sweep: a last restore of the start timing (every step
// restored already; this covers a failed restore), the verdict, the log.
func (s *Sweeper) finish(err error) error {
	s.phase = phaseDone
	s.setting = nil
	if s.res == nil {
		s.res = &SweepResult{Options: s.o}
	}
	if err != nil {
		s.err = err
		s.res.Err = err.Error()
	}
	if rerr := s.Port.Restore(s.start); rerr != nil {
		s.Logf("gpmc sweep: restoring the start timing: %v", rerr)
		s.res.Err = fmt.Sprintf("restore: %v", rerr)
		s.res.Restored = false
		if s.err == nil {
			s.err = rerr
		}
	} else {
		s.res.Restored = true
	}
	if s.err == nil {
		s.res.OK = s.res.Verify.Pass
		s.Logf("gpmc sweep: floor rdaccess=%d rdcycle=%d gap=%d → chosen %s: %s at %.0f ns/word (%.2f Mw/s), FCLK≈%.1f MHz, %d steps",
			s.res.FloorAccess, s.res.FloorCycle, s.res.FloorGap, s.cur, map[bool]string{true: "PASS", false: "FAIL"}[s.res.Verify.Pass],
			s.res.Verify.NsPerWord, s.res.Verify.WordsPerS/1e6, s.res.FclkMHz, s.res.Steps)
	} else {
		s.Logf("gpmc sweep: aborted in phase %s after %d steps: %v", phaseNames[s.phase], s.res.Steps, s.err)
	}
	return s.err
}

// nextSetting picks the next candidate of the current phase (advancing the
// phase when its floor is found), or returns false when the sweep is over.
func (s *Sweeper) nextSetting() bool {
	for s.phase != phaseDone {
		var cand CS1Timing
		var ok bool
		switch s.phase {
		case phaseAccess:
			cand, ok = s.nextAccess()
		case phaseCycle:
			cand, ok = s.nextCycle()
		case phaseGap:
			cand, ok = s.nextGap()
		case phaseVerify:
			cand, ok = s.nextVerify()
		}
		if ok {
			s.setting = &SettingResult{Timing: cand.Fields(), Pass: true, BaselineOK: true}
			s.cand = cand
			s.blk, s.base, s.drained, s.blkNs, s.baseNs, s.baseN = 0, false, 0, 0, 0, 0
			return true
		}
		s.closePhase()
	}
	return false
}

// lastOf is the most recent setting of a phase (nil when none).
func lastOf(ph []SettingResult) *SettingResult {
	if len(ph) == 0 {
		return nil
	}
	return &ph[len(ph)-1]
}

func (s *Sweeper) nextAccess() (CS1Timing, bool) {
	if l := lastOf(s.res.Access); l != nil && !l.Pass {
		return CS1Timing{}, false
	}
	a := s.start.RdAccess() - 1
	if l := lastOf(s.res.Access); l != nil {
		a = l.Timing.RdAccess - 1
	}
	if a < s.o.AccessFloor || a == 0 {
		return CS1Timing{}, false
	}
	cand := s.cur.WithRdAccess(a)
	if err := cand.Validate(); err != nil {
		s.Logf("gpmc sweep: stop at rdaccess=%d: %v", a, err)
		return CS1Timing{}, false
	}
	return cand, true
}

func (s *Sweeper) nextCycle() (CS1Timing, bool) {
	if l := lastOf(s.res.Cycle); l != nil && !l.Pass {
		return CS1Timing{}, false
	}
	floor := s.o.CycleFloor
	if floor == 0 {
		floor = s.cur.RdAccess() + 1
	}
	c := s.start.RdCycle() - 1
	if l := lastOf(s.res.Cycle); l != nil {
		c = l.Timing.RdCycle - 1
	}
	if c < floor || c == 0 {
		return CS1Timing{}, false
	}
	cand := s.cur.forCycle(c)
	if err := cand.Validate(); err != nil {
		s.Logf("gpmc sweep: stop at rdcycle=%d: %v", c, err)
		return CS1Timing{}, false
	}
	return cand, true
}

func (s *Sweeper) nextGap() (CS1Timing, bool) {
	if !s.o.SweepGap {
		return CS1Timing{}, false
	}
	if l := lastOf(s.res.GapPhase); l != nil && !l.Pass {
		return CS1Timing{}, false
	}
	g := s.start.Gap() - 1
	if l := lastOf(s.res.GapPhase); l != nil {
		g = l.Timing.Gap - 1
	}
	if g < s.o.GapFloor || g == 0 {
		return CS1Timing{}, false
	}
	return s.cur.WithGap(g), true
}

func (s *Sweeper) nextVerify() (CS1Timing, bool) {
	if s.verifying {
		return CS1Timing{}, false
	}
	s.verifying = true
	return s.cur, true
}

// closePhase folds a finished phase into the chosen timing and moves on.
func (s *Sweeper) closePhase() {
	switch s.phase {
	case phaseAccess:
		if a := s.res.FloorAccess + 1; a < s.start.RdAccess() {
			s.cur = s.cur.WithRdAccess(a) // one tick above the floor; else nothing gained, keep the start
		}
	case phaseCycle:
		if c := s.res.FloorCycle + 1; c < s.start.RdCycle() {
			s.cur = s.cur.forCycle(c)
		}
	case phaseGap:
		if g := s.res.FloorGap + 1; g < s.start.Gap() {
			s.cur = s.cur.WithGap(g)
		}
		s.res.ChosenRaw, s.res.Chosen = s.cur, s.cur.Fields()
		// FCLK from the clean cycle-phase points: ns/word = k · (RDCYCLETIME + gap) + c.
		s.res.FclkMHz = fclkEstimate(s.res.Cycle, s.start.Gap())
	}
	s.phase++
}

// drainsPerHalf is the drains per half-block of the current setting: the
// option, or for the verify setting enough to cover VerifyWords over the
// candidate halves.
func (s *Sweeper) drainsPerHalf() int {
	if s.phase != phaseVerify {
		return s.o.Drains
	}
	rec := s.res.RecLen
	if rec <= 0 {
		rec = s.o.Words
	}
	n := (s.o.VerifyWords + s.o.Blocks*rec - 1) / (s.o.Blocks * rec)
	if n < 1 {
		n = 1
	}
	return n
}

// chunk is the body of one step: capture + reference drain at the start
// timing, then up to perStep scored drains at the candidate (candidate half)
// or at the start timing (baseline half); the start timing is restored on
// every exit path.
func (s *Sweeper) chunk(b Bus) (err error) {
	c, err := newRampChecker(b, TsrcOptions{Words: s.o.Words, Tsrc: s.o.Tsrc}, s.Sleep, s.now)
	if err != nil {
		return err
	}
	if s.res.RecLen == 0 {
		s.res.RecLen = c.rec
	}
	res := s.setting
	if !s.base {
		if err := s.Port.Apply(s.cand); err != nil {
			return fmt.Errorf("apply candidate %s: %w", s.cand, err)
		}
		defer func() {
			if rerr := s.Port.Restore(s.start); rerr != nil && err == nil {
				err = fmt.Errorf("restore %s after the step: %w", s.start, rerr)
			}
		}()
	}
	per := s.drainsPerHalf()
	n := min(s.perStep, per-s.drained)
	for d := 0; d < n; d++ {
		r, e, err := c.drain()
		if err != nil {
			return err
		}
		if s.base {
			if !e.OK {
				res.BaselineOK = false
			}
			s.baseNs += r.NsPerWord()
			s.baseN++
			continue
		}
		res.Drains++
		res.Words += e.Words
		res.Breaks += e.Breaks
		res.Mismatch += e.Mismatch
		res.Underruns += e.Underruns
		if !e.PopsOK {
			res.PopsBad++
		}
		if !e.OK {
			res.Bad++
			res.Pass = false
			if res.FirstBad == nil {
				bad := e
				res.FirstBad = &bad
			}
		}
		s.blkNs += r.NsPerWord()
	}
	s.drained += n
	if s.drained < per {
		return nil
	}
	// half-block done
	s.drained = 0
	if !s.base {
		res.PerBlock = append(res.PerBlock, s.blkNs/float64(per))
		s.blkNs = 0
		s.base = true
		return nil
	}
	s.base = false
	s.blk++
	if s.blk < s.o.Blocks {
		return nil
	}
	return s.closeSetting()
}

// closeSetting scores the finished setting, records it in its phase and
// updates the floor.
func (s *Sweeper) closeSetting() error {
	res := s.setting
	s.setting = nil
	var sum float64
	minB, maxB := res.PerBlock[0], res.PerBlock[0]
	for _, v := range res.PerBlock {
		sum += v
		minB = min(minB, v)
		maxB = max(maxB, v)
	}
	res.NsPerWord = sum / float64(len(res.PerBlock))
	if res.NsPerWord > 0 {
		res.WordsPerS = 1e9 / res.NsPerWord
	}
	if s.baseN > 0 {
		res.Baseline = s.baseNs / float64(s.baseN)
	}
	res.Spread = maxB - minB
	s.Logf("gpmc sweep: %s → %s ns/word=%.0f (baseline %.0f) breaks=%d pops_bad=%d underruns=%d",
		s.cand, map[bool]string{true: "PASS", false: "FAIL"}[res.Pass], res.NsPerWord, res.Baseline, res.Breaks, res.PopsBad, res.Underruns)
	switch s.phase {
	case phaseAccess:
		s.res.Access = append(s.res.Access, *res)
		if res.Pass {
			s.res.FloorAccess = s.cand.RdAccess()
		}
	case phaseCycle:
		s.res.Cycle = append(s.res.Cycle, *res)
		if res.Pass {
			s.res.FloorCycle = s.cand.RdCycle()
		}
	case phaseGap:
		s.res.GapPhase = append(s.res.GapPhase, *res)
		if res.Pass {
			s.res.FloorGap = s.cand.Gap()
		}
	case phaseVerify:
		s.res.Verify = *res
	}
	if !res.BaselineOK {
		return fmt.Errorf("baseline timing %s no longer drains exactly — harness fault, sweep aborted", s.start)
	}
	return nil
}

// Run performs the whole sweep as consecutive steps on the calling
// goroutine (tests and bench tools; the app steps through Engine.Exec). The
// fabric program is left in the test-source state for the caller to restore
// (diag.saveRegs); the timing is back at the start one.
func (s *Sweeper) Run(b Bus) (*SweepResult, error) {
	for {
		done, err := s.Step(b)
		if done {
			return s.res, err
		}
	}
}

// fclkEstimate fits ns/word against the read ticks (RDCYCLETIME + gap) over
// the passing cycle points by least squares; the slope is the FCLK period.
func fclkEstimate(pts []SettingResult, gap uint32) float64 {
	var n, sx, sy, sxx, sxy float64
	for _, p := range pts {
		if !p.Pass || p.NsPerWord <= 0 {
			continue
		}
		x := float64(p.Timing.RdCycle + gap)
		n++
		sx += x
		sy += p.NsPerWord
		sxx += x * x
		sxy += x * p.NsPerWord
	}
	if n < 3 {
		return 0
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	slope := (n*sxy - sx*sy) / den // ns per tick
	if slope <= 0 {
		return 0
	}
	return 1000 / slope
}
