// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/labeth/open-dso-core/instrument"
)

// Controls is the engine's settings as the controllers drive them (the UI
// model, SCPI, the web and the settings file): each setter writes the
// instrument model at once and is applied on the owner goroutine at its
// next commit point, in the order the controllers gave them
// (ADR-HAL-INSTRUMENT-STATE). Snapshot and SerialConfig answer with the
// settings staged and not yet applied, as the engine will apply them, so a
// controller reads back what it set. Run control (RUN, STOP, SINGLE, AUTO
// and NORM) and FORCE take effect at once, as the engine's own methods do:
// they write the model's run control themselves and a STOP must not wait for
// a holdoff or a capture. Everything else is the engine's.
type Controls struct{ *Engine }

// Controls is the engine's controls over its instrument model.
// TRLC-LINKS: REQ-SDS-008
func (e *Engine) Controls() Controls { return Controls{e} }

// staging is the controllers' settings waiting for the owner: what to apply
// and how the snapshot shows it until then.
type staging struct {
	mu      sync.Mutex
	ops     []stagedOp
	pending atomic.Int32 // len(ops), read without the lock at every commit point
}

// stagedOp is one staged setting: apply runs on the owner, show patches a
// snapshot taken before it ran.
type stagedOp struct {
	apply func()
	show  func(*Stats)
	// serial is the match a SetSerialParams staged (SerialConfig answers it).
	serial *SerialParams
}

// stage queues op for the owner.
// TRLC-LINKS: REQ-SDS-008
func (e *Engine) stage(op stagedOp) {
	e.staged.mu.Lock()
	e.staged.ops = append(e.staged.ops, op)
	e.staged.pending.Store(int32(len(e.staged.ops))) //nolint:gosec // G115: a handful of settings between two commit points
	e.staged.mu.Unlock()
}

// applyStaged applies the staged settings in order (OWNER). The lock is
// held across them, so a snapshot sees each either staged or applied.
// TRLC-LINKS: REQ-SDS-008
func (e *Engine) applyStaged() {
	if e.staged.pending.Load() == 0 {
		return
	}
	e.staged.mu.Lock()
	defer e.staged.mu.Unlock()
	for _, op := range e.staged.ops {
		op.apply()
	}
	clear(e.staged.ops)
	e.staged.ops = e.staged.ops[:0]
	e.staged.pending.Store(0)
}

// Snapshot is the engine's snapshot with the staged settings shown.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-009
func (c Controls) Snapshot() Stats {
	e := c.Engine
	e.staged.mu.Lock()
	defer e.staged.mu.Unlock()
	st := e.Snapshot()
	for _, op := range e.staged.ops {
		if op.show != nil {
			op.show(&st)
		}
	}
	return st
}

// SerialConfig is the serial trigger's match, the staged one first.
// TRLC-LINKS: REQ-SDS-013
func (c Controls) SerialConfig() SerialParams {
	e := c.Engine
	e.staged.mu.Lock()
	for _, op := range slices.Backward(e.staged.ops) {
		if op.serial != nil {
			p := *op.serial
			e.staged.mu.Unlock()
			p.Bytes = append([]int(nil), p.Bytes...)
			p.Pattern = append([]PatternElem(nil), p.Pattern...)
			return p
		}
	}
	e.staged.mu.Unlock()
	return e.SerialConfig()
}

// SetTdiv stages the ladder's timebase; false off the ladder.
// TRLC-LINKS: REQ-SDS-010
func (c Controls) SetTdiv(tdivS float64) (instrument.Timebase, bool) {
	b, ok := PlanTdiv(tdivS)
	if !ok {
		return instrument.Timebase{}, false
	}
	e := c.Engine
	e.inst().SetTdiv(b.TdivS)
	e.stage(stagedOp{
		apply: func() { e.SetTdiv(b.TdivS) },
		show: func(st *Stats) {
			st.TdivSetS = b.TdivS
			st.ViewPending = e.sram != nil && !st.Running && e.viewHeld.Load()
		},
	})
	return b.Timebase(), true
}

// SetTrigPosFrac stages the trigger's place across the screen.
// TRLC-LINKS: REQ-SDS-011
func (c Controls) SetTrigPosFrac(frac float64) {
	if math.IsNaN(frac) {
		frac = .5
	}
	frac = math.Min(1, math.Max(0, frac))
	e := c.Engine
	e.inst().SetTrigPosFrac(frac)
	e.stage(stagedOp{apply: func() { e.SetTrigPosFrac(frac) }, show: func(st *Stats) { st.TrigPosFrac = frac }})
}

// SetTrigSource stages the trigger channel (0 = C1, 1 = C2).
// TRLC-LINKS: REQ-SDS-011
func (c Controls) SetTrigSource(ch int) {
	if ch != 1 {
		ch = 0
	}
	e := c.Engine
	e.inst().SetTrigSource(ch)
	e.stage(stagedOp{apply: func() { e.SetTrigSource(ch) }, show: func(st *Stats) { st.TrigSource = ch }})
}

// SetTrigSlope stages the rising (true) or falling edge.
// TRLC-LINKS: REQ-SDS-011
func (c Controls) SetTrigSlope(rising bool) {
	e := c.Engine
	e.inst().SetTrigSlope(rising)
	e.stage(stagedOp{apply: func() { e.SetTrigSlope(rising) }, show: func(st *Stats) { st.TrigRising = rising }})
}

// SetTrigType stages the trigger type (a TrigType; out of range is edge).
// TRLC-LINKS: REQ-SDS-011
func (c Controls) SetTrigType(t int) {
	if t < int(TrigEdge) || t > int(TrigVideo) {
		t = int(TrigEdge)
	}
	e := c.Engine
	e.inst().SetTrigType(t)
	e.stage(stagedOp{apply: func() { e.SetTrigType(t) }, show: func(st *Stats) {
		st.TrigType = t
		if t == int(TrigEdge) {
			st.TriggerBackend = ""
		}
	}})
}

// SetHoldoff stages the holdoff, clamped to [0, 10] s, and returns it.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011
func (c Controls) SetHoldoff(sec float64) float64 {
	sec = instrument.ClampHoldoff(sec)
	e := c.Engine
	e.inst().SetHoldoff(sec)
	e.stage(stagedOp{apply: func() { e.SetHoldoff(sec) }, show: func(st *Stats) { st.HoldoffS = sec }})
	return sec
}

// SetPulseParams stages the pulse qualifier.
// TRLC-LINKS: REQ-SDS-011
func (c Controls) SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int) {
	e := c.Engine
	e.inst().SetPulseParams(lvlFrac, wMinNs, wMaxNs, cond)
	lvl := instrument.ClampFrac(lvlFrac)
	lo, hi := instrument.OrderPair(wMinNs, wMaxNs)
	e.stage(stagedOp{apply: func() { e.SetPulseParams(lvlFrac, wMinNs, wMaxNs, cond) }, show: func(st *Stats) {
		q := &st.TrigQual
		q.PulseLvl, q.PulseMinNs, q.PulseMaxNs, q.PulseCond = lvl, lo, hi, instrument.ClampCond(cond)
	}})
}

// SetSlopeParams stages the slope qualifier.
// TRLC-LINKS: REQ-SDS-011
func (c Controls) SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int) {
	e := c.Engine
	e.inst().SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs, cond)
	lo, hi := instrument.OrderPair(instrument.ClampFrac(loFrac), instrument.ClampFrac(hiFrac))
	tlo, thi := instrument.OrderPair(tMinNs, tMaxNs)
	e.stage(stagedOp{apply: func() { e.SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs, cond) }, show: func(st *Stats) {
		q := &st.TrigQual
		q.SlopeLo, q.SlopeHi, q.SlopeMinNs, q.SlopeMaxNs, q.SlopeCond = lo, hi, tlo, thi, instrument.ClampCond(cond)
	}})
}

// SetVideoParams stages the video qualifier.
// TRLC-LINKS: REQ-SDS-011
func (c Controls) SetVideoParams(std, line int, neg bool) {
	if std != 1 {
		std = 0
	}
	line = max(line, 0)
	e := c.Engine
	e.inst().SetVideoParams(std, line, neg)
	e.stage(stagedOp{apply: func() { e.SetVideoParams(std, line, neg) }, show: func(st *Stats) {
		st.TrigQual.VideoStd, st.TrigQual.VideoLine, st.TrigQual.VideoNeg = std, line, neg
	}})
}

// SetAcqMode stages the acquisition mode (Acq*; out of range is normal).
// TRLC-LINKS: REQ-SDS-012
func (c Controls) SetAcqMode(m int) {
	if m < AcqNormal || m > AcqPrecision {
		m = AcqNormal
	}
	e := c.Engine
	e.inst().SetAcqMode(m)
	e.stage(stagedOp{apply: func() { e.SetAcqMode(m) }, show: func(st *Stats) { st.AcqMode = m }})
}

// SetAvgCount stages the average's frame count, clamped to [1, 256].
// TRLC-LINKS: REQ-SDS-012
func (c Controls) SetAvgCount(n int) {
	n = instrument.ClampAverages(n)
	e := c.Engine
	e.inst().SetAvgCount(n)
	e.stage(stagedOp{apply: func() { e.SetAvgCount(n) }, show: func(st *Stats) { st.AvgCount = n }})
}

// SetEresLen stages the ERES boxcar length.
// TRLC-LINKS: REQ-SDS-012
func (c Controls) SetEresLen(l int) {
	l = instrument.ClampEresLen(l)
	e := c.Engine
	e.inst().SetEresLen(l)
	e.stage(stagedOp{apply: func() { e.SetEresLen(l) }, show: func(st *Stats) { st.EresLen = l }})
}

// SetETS stages the equivalent-time opt-in; the SRAM fabric has none.
// TRLC-LINKS: REQ-SDS-019
func (c Controls) SetETS(on bool) {
	e := c.Engine
	if e.sram != nil {
		on = false
	}
	e.inst().SetETS(on)
	e.stage(stagedOp{apply: func() { e.SetETS(on) }, show: func(st *Stats) { st.ETS = on }})
}

// SetSerialMode stages arming (SerialTrigger) or disarming the serial
// trigger.
// TRLC-LINKS: REQ-SDS-013
func (c Controls) SetSerialMode(m int) {
	if m != SerialOff && m != SerialTrigger {
		m = SerialOff
	}
	e := c.Engine
	e.inst().SetSerialMode(m)
	e.stage(stagedOp{apply: func() { e.SetSerialMode(m) }, show: func(st *Stats) {
		if m != SerialOff && st.SerialMode == SerialOff {
			st.SerialMatches = 0
		}
		st.SerialMode = m
	}})
}

// SetSerialParams stages the serial trigger's match (its own copy).
// TRLC-LINKS: REQ-SDS-013
func (c Controls) SetSerialParams(p SerialParams) {
	p.Bytes = append([]int(nil), p.Bytes...)
	p.Pattern = append([]PatternElem(nil), p.Pattern...)
	e := c.Engine
	e.inst().SetSerialParams(p)
	e.stage(stagedOp{apply: func() { e.SetSerialParams(p) }, show: func(st *Stats) { st.SerialSet = !p.Empty() }, serial: &p})
}

// SetTrigLevelCode stages comparator DAC code code, clamped to the DAC's
// operating range, and returns it; 0 stages nothing (the boot comparator
// stays).
// TRLC-LINKS: REQ-SDS-098
func (c Controls) SetTrigLevelCode(code uint16) uint16 {
	if code == 0 {
		return 0
	}
	code = min(max(code, TrigCodeMin), TrigCodeMax)
	e := c.Engine
	e.stage(stagedOp{apply: func() { e.SetTrigLevelCode(code) }, show: func(st *Stats) { st.TrigCode = code }})
	return code
}

// SetPrecisionRate stages the precision words' rate, the nearest the
// hardware gives (500 MHz over a power of two, 2^4 to 2^20), and returns
// it.
// TRLC-LINKS: REQ-SDS-036
func (c Controls) SetPrecisionRate(hz float64) float64 {
	e := c.Engine
	actual := precisionRate(hz)
	e.inst().SetPrecisionRate(actual)
	e.stage(stagedOp{apply: func() { e.SetPrecisionRate(hz) }, show: func(st *Stats) { st.PrecisionRateHz = actual }})
	return actual
}
