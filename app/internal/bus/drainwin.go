// Windowed drains, REWIND re-drains and the drain monitors of schema v3
// (06-TIERS §1.6 / §2: DRAIN_START, DRAIN_LEN, OPCODE REWIND, DRAIN_STAT,
// POP_MON) plus the TSRC test-source capture that rungs R1/R2b/R3 and the
// boot-time timing check drain.
//
// Everything here is written against the Bus interface, not *Dev, so the
// engine's and the diagnostic block's fakes drive the same code; on hardware
// the pops go through PopWords (EDMA when enabled). Every function must be
// called on the bus-owner goroutine (Engine.Exec is the door).
//
// Contract of the fabric side (schema Desc, the reference):
//   - DRAIN_START.IDX / DRAIN_LEN.LEN are latched at DONE and at OP_REWIND;
//     LEN 0 means "to the record end".
//   - OP_REWIND puts the drain pointer back to DRAIN_START with DRAIN_LEN
//     re-latched and clears DRAIN_STAT.POPS; the record is untouched.
//   - BURST_REMAIN.REMAIN counts the words left in the window; a pop past it
//     returns the last word and counts POP_MON.UNDERRUN (sticky since GO).
//   - DRAIN_STAT.POPS counts BURST/BURST_ALIAS pops since GO / REWIND (wraps).
package bus

import (
	"fmt"
	"time"

	"open-sds/app/internal/iface"
)

// DrainStat is one reading of DRAIN_STAT + POP_MON.
type DrainStat struct {
	Pops     uint16 `json:"pops"`      // DRAIN_STAT.POPS (since GO / REWIND, wraps)
	MinGap   uint8  `json:"min_gap"`   // POP_MON.MIN_GAP: shortest nOE-rise-to-nOE-rise gap, clk cycles (saturating)
	Underrun uint8  `json:"underrun"`  // POP_MON.UNDERRUN: pops on an empty window since GO (sticky, saturating)
	Remain   uint16 `json:"remain"`    // BURST_REMAIN word as read
	Ready    bool   `json:"ready"`     // BURST_REMAIN.READY
	Avail    int    `json:"available"` // BURST_REMAIN.REMAIN
}

// ReadDrainStat reads DRAIN_STAT, POP_MON and BURST_REMAIN.
func ReadDrainStat(b Bus) (DrainStat, error) {
	var s DrainStat
	st, err := b.Read(PlaneCS1, iface.SelDrainStat)
	if err != nil {
		return s, err
	}
	pm, err := b.Read(PlaneCS1, iface.SelPopMon)
	if err != nil {
		return s, err
	}
	rem, err := b.Read(PlaneCS1, iface.SelBurstRemain)
	if err != nil {
		return s, err
	}
	s.Pops = st & iface.DrainStatPopsMask
	s.MinGap = uint8((pm & iface.PopMonMinGapMask) >> iface.PopMonMinGapShift)
	s.Underrun = uint8((pm & iface.PopMonUnderrunMask) >> iface.PopMonUnderrunShift)
	s.Remain = rem
	s.Ready = rem&iface.BurstRemainReadyMask != 0
	s.Avail = int(rem & iface.BurstRemainRemainMask)
	return s, nil
}

// Window is a drain window over the logical record: Len 0 = to the end.
type Window struct {
	Start uint16 `json:"start"`
	Len   uint16 `json:"len"`
}

// Full is the whole record.
var Full = Window{}

// SetWindow writes DRAIN_START / DRAIN_LEN. The fabric latches them at DONE
// (the next finalized record) and at REWIND (the frozen one).
func SetWindow(b Bus, w Window) error {
	if w.Start > iface.PretrigMax {
		return fmt.Errorf("bus: drain window start %d beyond the record (%d)", w.Start, iface.PretrigMax)
	}
	if err := b.Write(PlaneCS1, iface.SelDrainStart, w.Start&iface.DrainStartIdxMask); err != nil {
		return err
	}
	return b.Write(PlaneCS1, iface.SelDrainLen, w.Len)
}

// Rewind issues OPCODE REWIND: the drain pointer returns to DRAIN_START with
// DRAIN_LEN re-latched, DRAIN_STAT.POPS restarts at 0, the record is untouched.
func Rewind(b Bus) error { return b.Write(PlaneCS1, iface.SelOpcode, iface.OpRewind) }

// DrainResult is one windowed drain with its monitors.
type DrainResult struct {
	Window  Window        `json:"window"`
	N       int           `json:"n"`       // words popped
	Before  DrainStat     `json:"before"`  // after REWIND, before the pops
	After   DrainStat     `json:"after"`   // after the pops
	Elapsed time.Duration `json:"elapsed"` // the pops only (CLOCK_MONOTONIC)
}

// NsPerWord is the drain rate.
func (r DrainResult) NsPerWord() float64 {
	if r.N == 0 {
		return 0
	}
	return float64(r.Elapsed.Nanoseconds()) / float64(r.N)
}

// DrainWindow re-drains a window of the frozen record: DRAIN_START/LEN,
// REWIND, then pops min(len(dst), BURST_REMAIN.REMAIN) words into dst. The
// monitors are read on both sides of the pops so the caller can run
// CheckExact. Returns an error when no record is ready.
func DrainWindow(b Bus, w Window, dst []uint16) (DrainResult, error) {
	return drainWindow(b, w, dst, time.Now)
}

func drainWindow(b Bus, w Window, dst []uint16, now func() time.Time) (DrainResult, error) {
	r := DrainResult{Window: w}
	if err := SetWindow(b, w); err != nil {
		return r, err
	}
	if err := Rewind(b); err != nil {
		return r, err
	}
	var err error
	if r.Before, err = ReadDrainStat(b); err != nil {
		return r, err
	}
	if !r.Before.Ready {
		return r, fmt.Errorf("bus: no record ready for the drain window (BURST_REMAIN %#04x)", r.Before.Remain)
	}
	n := r.Before.Avail
	if n > len(dst) {
		n = len(dst)
	}
	t0 := now()
	b.PopWords(iface.SelBurst, dst[:n], n)
	r.Elapsed = now().Sub(t0)
	r.N = n
	if r.After, err = ReadDrainStat(b); err != nil {
		return r, err
	}
	return r, nil
}

// DrainWindowInto is DrainWindow split into channels (hi byte = CH1, lo = CH2)
// through a caller-supplied word scratch (len >= len(c1)).
func DrainWindowInto(b Bus, w Window, c1, c2 []uint8, scratch []uint16) (DrainResult, error) {
	n := len(c1)
	if len(c2) < n {
		n = len(c2)
	}
	if len(scratch) < n {
		n = len(scratch)
	}
	r, err := DrainWindow(b, w, scratch[:n])
	for i := 0; i < r.N; i++ {
		c1[i], c2[i] = iface.Split(scratch[i])
	}
	return r, err
}

// Exactness is the pointer-delta byte-exactness verdict of one drain (rung R1
// (c): words/s, ramp breaks, DRAIN_STAT delta, POP_MON.UNDERRUN).
type Exactness struct {
	Words     int    `json:"words"`
	PopsDelta int    `json:"pops_delta"` // DRAIN_STAT.POPS after - before (mod 2^16)
	PopsOK    bool   `json:"pops_ok"`    // PopsDelta == Words mod 2^16
	Underruns int    `json:"underruns"`  // new POP_MON.UNDERRUN counts during the drain
	MinGap    uint8  `json:"min_gap"`    // POP_MON.MIN_GAP after the drain
	Tsrc      uint16 `json:"tsrc"`       // the pattern checked (0 = none)
	K0        uint32 `json:"k0"`         // recovered pattern index of the first word
	Breaks    int    `json:"breaks"`     // words off the pattern
	FirstBad  int    `json:"first_bad"`  // index of the first break, -1 when none
	Mismatch  int    `json:"mismatch"`   // words differing from the reference slice (when given)
	FirstDiff int    `json:"first_diff"` // index of the first mismatch, -1 when none
	OK        bool   `json:"ok"`         // everything above clean
	Err       string `json:"err,omitempty"`
}

// CheckExact scores a drained window: the pop counter must have advanced by
// exactly len(words), no pop may have found the window empty, the words must
// follow the TSRC pattern (tsrc != ADC), and, when ref is given, equal it
// word for word. ref may be nil.
func CheckExact(r DrainResult, words []uint16, tsrc uint16, ref []uint16) Exactness {
	e := Exactness{Words: len(words), Tsrc: tsrc, FirstBad: -1, FirstDiff: -1, MinGap: r.After.MinGap}
	e.PopsDelta = int(uint16(r.After.Pops - r.Before.Pops))
	e.PopsOK = e.PopsDelta == len(words)&0xffff
	e.Underruns = int(r.After.Underrun) - int(r.Before.Underrun)
	if e.Underruns < 0 { // saturated / wrapped counter: count it as a failure
		e.Underruns = 255
	}
	if tsrc != iface.IlCtrlTsrcAdc && len(words) > 0 {
		k0, bad, err := iface.TsrcCheck(tsrc, words)
		if err != nil {
			e.Err = err.Error()
			e.Breaks = len(words)
			e.FirstBad = 0
		} else {
			e.K0, e.Breaks = k0, len(bad)
			if len(bad) > 0 {
				e.FirstBad = bad[0]
			}
		}
	}
	if ref != nil {
		n := len(words)
		if len(ref) != n {
			e.Mismatch = n
			e.FirstDiff = 0
			if e.Err == "" {
				e.Err = fmt.Sprintf("reference has %d words, drained %d", len(ref), n)
			}
		} else {
			for i := 0; i < n; i++ {
				if words[i] != ref[i] {
					if e.FirstDiff < 0 {
						e.FirstDiff = i
					}
					e.Mismatch++
				}
			}
		}
	}
	e.OK = e.PopsOK && e.Underruns == 0 && e.Breaks == 0 && e.Mismatch == 0 && e.Err == ""
	return e
}

// ---- TSRC capture ----

// TsrcOptions programs a frozen test-source record.
type TsrcOptions struct {
	Words   int    `json:"words"`    // record words, default and max iface.PretrigMax
	Tsrc    uint16 `json:"tsrc"`     // IL_CTRL.TSRC (0 = the converters; RampCheck and the sweep use RAMP)
	Chmode  uint16 `json:"chmode"`   // RUN.CHMODE (default dual)
	Decim   uint32 `json:"decim"`    // default 1
	EncRate int    `json:"enc_rate"` // ACQ_CTRL.ENC_RATE (default 3): the writer is clocked by the encode
	Timeout int    `json:"timeout_ms"`
}

func (o *TsrcOptions) defaults() {
	if o.Words <= 0 || o.Words > iface.PretrigMax {
		o.Words = iface.PretrigMax
	}
	if o.Tsrc > iface.IlCtrlTsrcGlitch {
		o.Tsrc = iface.IlCtrlTsrcAdc
	}
	if o.Chmode > iface.RunChmodeCh2 {
		o.Chmode = iface.RunChmodeDual
	}
	if o.Decim == 0 {
		o.Decim = 1
	}
	if o.EncRate < 0 || o.EncRate > 3 {
		o.EncRate = 3
	}
	if o.Timeout <= 0 {
		o.Timeout = 500
	}
}

// CaptureTsrc programs a one-shot auto capture with the test source
// substituted for the ADC (RESET, IL_CTRL.TSRC, RUN auto, DECIM, PRE/POST,
// ACQ_CTRL encode on, GO), waits for DONE/VALID, HALTs and returns the
// finalized record length. The full window is selected. The caller restores
// the engine's program afterwards (diag.saveRegs, or ResetTsrc for the boot
// path); sleep runs on the owner goroutine.
func CaptureTsrc(b Bus, o TsrcOptions, sleep func(time.Duration)) (recLen int, err error) {
	o.defaults()
	pre, post := uint32(o.Words/2), uint32(o.Words-o.Words/2)
	w := func(sel, v uint16) {
		if err == nil {
			err = b.Write(PlaneCS1, sel, v)
		}
	}
	w(iface.SelOpcode, iface.OpReset)
	w(iface.SelIlCtrl, o.Tsrc<<iface.IlCtrlTsrcShift)
	w(iface.SelRun, iface.RunRunMask|o.Chmode<<iface.RunChmodeShift)
	w(iface.SelDecimLo, uint16(o.Decim))
	w(iface.SelDecimHi, uint16(o.Decim>>16))
	w(iface.SelPretrigLo, uint16(pre))
	w(iface.SelPretrigHi, uint16(pre>>16))
	w(iface.SelPosttrigLo, uint16(post))
	w(iface.SelPosttrigHi, uint16(post>>16))
	w(iface.SelDrainStart, 0)
	w(iface.SelDrainLen, 0)
	w(iface.SelAcqCtrl, iface.AcqCtrlEncEnMask|uint16(o.EncRate)<<iface.AcqCtrlEncRateShift|iface.AcqCtrlPairEnMask)
	w(iface.SelTrigLevel, 128)
	w(iface.SelOpcode, iface.OpGo)
	if err != nil {
		return 0, err
	}
	var st uint16
	for i := 0; i < o.Timeout; i++ {
		st, _ = b.Read(PlaneCS1, iface.SelStatusA)
		if st&(iface.StatusADoneMask|iface.StatusAValidMask) != 0 {
			break
		}
		sleep(time.Millisecond)
	}
	w(iface.SelOpcode, iface.OpHalt)
	if err != nil {
		return 0, err
	}
	rem, err := b.Read(PlaneCS1, iface.SelBurstRemain)
	if err != nil {
		return 0, err
	}
	if rem&iface.BurstRemainReadyMask == 0 {
		return 0, fmt.Errorf("bus: no test-source record ready (STATUS_A %#04x, BURST_REMAIN %#04x)", st, rem)
	}
	return int(rem & iface.BurstRemainRemainMask), nil
}

// ResetTsrc returns the fabric to the reset posture after a test-source run
// outside the engine (boot path): capture idle, ADC source, full window.
func ResetTsrc(b Bus) error {
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	keep(b.Write(PlaneCS1, iface.SelOpcode, iface.OpReset))
	keep(b.Write(PlaneCS1, iface.SelIlCtrl, 0))
	keep(SetWindow(b, Full))
	keep(b.Write(PlaneCS1, iface.SelRun, 0))
	keep(b.Write(PlaneCS1, iface.SelAcqCtrl, 0x7c00)) // reset word: encode off, all pairs enabled
	return first
}

// RampReport is the outcome of RampCheck: one test-source record re-drained
// Passes times through REWIND, every drain checked for exactness against the
// pattern and against the first drain.
type RampReport struct {
	Tsrc      uint16     `json:"tsrc"`
	RecLen    int        `json:"rec_len"`
	Passes    int        `json:"passes"`
	Words     int        `json:"words"` // total words drained
	Bad       int        `json:"bad_drains"`
	Breaks    int        `json:"breaks"`
	PopsBad   int        `json:"pops_mismatch"`
	Underrun  int        `json:"underruns"`
	Mismatch  int        `json:"mismatch"` // words differing from the first drain
	NsPerW    float64    `json:"ns_per_word"`
	MinNsPerW float64    `json:"ns_per_word_min"`
	MaxNsPerW float64    `json:"ns_per_word_max"`
	WordsPerS float64    `json:"words_per_s"`
	First     Exactness  `json:"first"`
	FirstBad  *Exactness `json:"first_bad,omitempty"`
	OK        bool       `json:"ok"`
}

// rampChecker holds a frozen test-source record and re-drains it.
type rampChecker struct {
	b    Bus
	tsrc uint16
	rec  int
	ref  []uint16 // the first drain (reference for byte identity)
	buf  []uint16
	now  func() time.Time
}

func newRampChecker(b Bus, o TsrcOptions, sleep func(time.Duration), now func() time.Time) (*rampChecker, error) {
	o.defaults()
	rec, err := CaptureTsrc(b, o, sleep)
	if err != nil {
		return nil, err
	}
	if rec == 0 {
		return nil, fmt.Errorf("bus: test-source record is empty")
	}
	if now == nil {
		now = time.Now
	}
	c := &rampChecker{b: b, tsrc: o.Tsrc, rec: rec, buf: make([]uint16, rec), now: now}
	r, err := drainWindow(b, Full, c.buf, now)
	if err != nil {
		return nil, err
	}
	if r.N != rec {
		return nil, fmt.Errorf("bus: reference drain got %d of %d words", r.N, rec)
	}
	c.ref = append([]uint16(nil), c.buf[:rec]...)
	return c, nil
}

// drain re-drains the full record and scores it. When the reference itself
// is disputed (ref != nil but the pattern check fails on it) the caller sees
// it in First.
func (c *rampChecker) drain() (DrainResult, Exactness, error) {
	r, err := drainWindow(c.b, Full, c.buf, c.now)
	if err != nil {
		return r, Exactness{}, err
	}
	return r, CheckExact(r, c.buf[:r.N], c.tsrc, c.ref), nil
}

// run drains passes times and folds the verdicts into a report.
func (c *rampChecker) run(passes int) (RampReport, error) {
	rep := RampReport{Tsrc: c.tsrc, RecLen: c.rec, Passes: passes, OK: true, MinNsPerW: -1}
	// The reference drain is scored on the pattern only (it IS the reference).
	rep.First = CheckExact(DrainResult{N: c.rec, Before: DrainStat{}, After: DrainStat{Pops: uint16(c.rec)}}, c.ref, c.tsrc, nil)
	if !rep.First.OK {
		rep.OK = false
		rep.Bad++
		rep.Breaks += rep.First.Breaks
	}
	var sumNs float64
	for p := 0; p < passes; p++ {
		r, e, err := c.drain()
		if err != nil {
			return rep, err
		}
		rep.Words += e.Words
		rep.Breaks += e.Breaks
		rep.Mismatch += e.Mismatch
		rep.Underrun += e.Underruns
		if !e.PopsOK {
			rep.PopsBad++
		}
		if !e.OK {
			rep.Bad++
			rep.OK = false
			if rep.FirstBad == nil {
				bad := e
				rep.FirstBad = &bad
			}
		}
		ns := r.NsPerWord()
		sumNs += ns
		if rep.MinNsPerW < 0 || ns < rep.MinNsPerW {
			rep.MinNsPerW = ns
		}
		if ns > rep.MaxNsPerW {
			rep.MaxNsPerW = ns
		}
	}
	if passes > 0 {
		rep.NsPerW = sumNs / float64(passes)
		if rep.NsPerW > 0 {
			rep.WordsPerS = 1e9 / rep.NsPerW
		}
	}
	if rep.MinNsPerW < 0 {
		rep.MinNsPerW = 0
	}
	return rep, nil
}

// RampCheck captures one test-source record and re-drains it passes times
// (REWIND), scoring every drain: the boot-time gate of a persisted GPMC
// timing and the R1 per-setting block. It leaves the record frozen; the
// caller restores the fabric program.
func RampCheck(b Bus, o TsrcOptions, passes int, sleep func(time.Duration)) (RampReport, error) {
	if passes <= 0 {
		passes = 3
	}
	if o.Tsrc == iface.IlCtrlTsrcAdc {
		o.Tsrc = iface.IlCtrlTsrcRamp
	}
	c, err := newRampChecker(b, o, sleep, time.Now)
	if err != nil {
		return RampReport{}, err
	}
	return c.run(passes)
}
