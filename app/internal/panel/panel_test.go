// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// TRLC-LINKS: REQ-SDS-135
type call struct {
	what string
	a, b int
}

// fakeEng is a stateful engine fake: every setter records its call, checks
// its argument (an illegal one is kept for takeIllegal) and updates the
// Stats snapshot the panel reads back, as the real engine does. It has the
// serial trigger; plainEng hides it.
// TRLC-LINKS: REQ-SDS-135
type fakeEng struct {
	matrix  [5]uint16
	calls   []call
	leds    []uint16
	stats   engine.Stats
	acqLog  []engine.AcqSample
	serial  engine.SerialParams
	illegal []string
	statMu  sync.Mutex // the Run loop's lamp sync snapshots it concurrently
}

// plainEng is an engine without the optional interfaces (no serial
// trigger, no Bode, no publish clock).
type plainEng struct{ Engine }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) record(what string, a, b int) {
	f.statMu.Lock()
	f.calls = append(f.calls, call{what, a, b})
	f.statMu.Unlock()
}

// TRLC-LINKS: REQ-SDS-136
func (f *fakeEng) bad(format string, a ...any) {
	f.illegal = append(f.illegal, fmt.Sprintf(format, a...))
}

// takeIllegal returns (and clears) the illegal commands sent so far.
// TRLC-LINKS: REQ-SDS-136
func (f *fakeEng) takeIllegal() []string {
	f.statMu.Lock()
	defer f.statMu.Unlock()
	out := f.illegal
	f.illegal = nil
	return out
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) ReadMatrix() ([5]uint16, bool) { return f.matrix, true }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetLEDs(w uint16) {
	f.statMu.Lock()
	f.leds = append(f.leds, w)
	f.statMu.Unlock()
}

// lastLEDs is the last latched lamp word.
func (f *fakeEng) lastLEDs() uint16 {
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if len(f.leds) == 0 {
		return 0
	}
	return f.leds[len(f.leds)-1]
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) Snapshot() engine.Stats {
	f.statMu.Lock()
	defer f.statMu.Unlock()
	return f.stats
}

// set changes the engine state under its lock (tests).
func (f *fakeEng) set(fn func(st *engine.Stats)) {
	f.statMu.Lock()
	fn(&f.stats)
	f.statMu.Unlock()
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) AcqLog(n int) ([]engine.AcqSample, float64) { return f.acqLog, 0 }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetOffsetDAC(ch int, code uint16) {
	f.record("offset", ch, int(code))
	f.statMu.Lock()
	defer f.statMu.Unlock()
	switch ch {
	case 0:
		f.stats.OffC1 = code
	case 1:
		f.stats.OffC2 = code
	default:
		f.bad("SetOffsetDAC ch=%d", ch)
	}
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetTrigLevelCode(code uint16) uint16 {
	f.record("triglevel", int(code), 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if code < engine.TrigCodeMin || code > engine.TrigCodeMax {
		f.bad("SetTrigLevelCode %d outside [%d,%d]", code, engine.TrigCodeMin, engine.TrigCodeMax)
	}
	f.stats.TrigCode = code
	return code
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetTdiv(t float64) (engine.Band, bool) {
	f.record("tdiv", int(t*1e9), 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	b, ok := engine.PlanTdiv(t)
	if !ok {
		f.bad("SetTdiv %g not a ladder detent", t)
		return engine.Band{}, false
	}
	f.stats.TdivS = b.TdivS
	return b, true
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetNorm(on bool) {
	f.record("norm", b2i(on), 0)
	f.set(func(st *engine.Stats) { st.Norm = on })
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetRunning(on bool) {
	f.set(func(st *engine.Stats) { st.Running, st.Single = on, false })
	f.record("run", b2i(on), 0)
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetRollStream(on bool) {
	f.set(func(st *engine.Stats) { st.RollStream = on })
}

// TRLC-LINKS: REQ-SDS-010
func (f *fakeEng) RollStream() bool { return f.Snapshot().RollStream }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetSingle() {
	f.set(func(st *engine.Stats) { st.Running, st.Single, st.Norm = true, true, true })
	f.record("single", 0, 0)
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetTrigSlope(r bool) {
	f.record("slope", b2i(r), 0)
	f.set(func(st *engine.Stats) { st.TrigRising = r })
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetTrigSource(ch int) {
	f.record("src", ch, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if ch != 0 && ch != 1 {
		f.bad("SetTrigSource ch=%d", ch)
		return
	}
	f.stats.TrigSource = ch
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetTrigType(t int) {
	f.record("ttype", t, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if t < 0 || t > 3 {
		f.bad("SetTrigType %d", t)
		return
	}
	f.stats.TrigType = t
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetAcqMode(m int) {
	f.record("acq", m, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if m < 0 || m > 3 {
		f.bad("SetAcqMode %d", m)
		return
	}
	f.stats.AcqMode = m
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetAvgCount(n int) {
	f.record("avg", n, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if n < 1 || n > 256 {
		f.bad("SetAvgCount %d", n)
		return
	}
	f.stats.AvgCount = n
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetEresLen(l int) {
	f.record("eres", l, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if !inInts(l, 1, 3, 7, 15, 31, 63) {
		f.bad("SetEresLen %d", l)
		return
	}
	f.stats.EresLen = l
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetETS(on bool) {
	f.record("ets", b2i(on), 0)
	f.set(func(st *engine.Stats) { st.ETS = on })
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetMemDepth(n int) int {
	f.record("memdepth", n, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if !inInts(n, 2048, 6144, 14336, 20480) {
		f.bad("SetMemDepth %d", n)
		return f.stats.MemDepth
	}
	f.stats.MemDepth = n
	return n
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetPulseParams(l, mn, mx float64, c int) {
	f.record("pulse", c, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if l < 0 || l > 1 || mn < 0 || mx < 0 || c < 0 || c > 3 {
		f.bad("SetPulseParams lvl=%g w=[%g,%g] cond=%d", l, mn, mx, c)
		return
	}
	q := &f.stats.TrigQual
	q.PulseLvl, q.PulseMinNs, q.PulseMaxNs, q.PulseCond = l, mn, mx, c
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetSlopeParams(lo, hi, mn, mx float64, c int) {
	f.record("slopeq", c, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if lo < 0 || lo > 1 || hi < 0 || hi > 1 || mn < 0 || mx < 0 || c < 0 || c > 3 {
		f.bad("SetSlopeParams lo=%g hi=%g t=[%g,%g] cond=%d", lo, hi, mn, mx, c)
		return
	}
	q := &f.stats.TrigQual
	q.SlopeLo, q.SlopeHi, q.SlopeMinNs, q.SlopeMaxNs, q.SlopeCond = lo, hi, mn, mx, c
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetMask(m *engine.Mask) {
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if m == nil { // clears, as the engine's does
		f.calls = append(f.calls, call{"mask", 0, 0})
		f.stats.MaskSet = false
		return
	}
	if m.WinCols <= 0 || len(m.Lo) < m.WinCols || len(m.Hi) < m.WinCols {
		f.bad("SetMask malformed")
		return
	}
	f.calls = append(f.calls, call{"mask", m.WinCols, 0})
	f.stats.MaskSet = true
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetMaskMode(m int) {
	f.record("maskmode", m, 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if m < 0 || m > 2 {
		f.bad("SetMaskMode %d", m)
		return
	}
	f.stats.MaskMode = m
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) ClearMaskFails() {
	f.record("maskclear", 0, 0)
	f.set(func(st *engine.Stats) { st.MaskPass, st.MaskFail, st.MaskStopped = 0, 0, false })
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetVideoParams(std, line int, neg bool) {
	f.record("video", std, line)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if std != 0 && std != 1 || line < 0 || line > 625 {
		f.bad("SetVideoParams std=%d line=%d", std, line)
		return
	}
	q := &f.stats.TrigQual
	q.VideoStd, q.VideoLine, q.VideoNeg = std, line, neg
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetTrigPosFrac(fr float64) {
	f.record("trigpos", int(fr*100), 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if fr < 0.02-1e-9 || fr > 1+1e-9 {
		f.bad("SetTrigPosFrac %g outside [0.02,1]", fr)
		return
	}
	f.stats.TrigPosFrac = fr
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeEng) SetHoldoff(s float64) float64 {
	f.record("holdoff", int(s*1e6), 0)
	f.statMu.Lock()
	defer f.statMu.Unlock()
	if s < 0 {
		f.bad("SetHoldoff %g < 0", s)
		return f.stats.HoldoffS
	}
	f.stats.HoldoffS = s
	return s
}

// SetSerialMode arms (1) or disarms (0) the serial trigger.
// TRLC-LINKS: REQ-SDS-013
func (f *fakeEng) SetSerialMode(m int) {
	f.record("serialmode", m, 0)
	f.set(func(st *engine.Stats) { st.SerialMode = m })
}

// SetSerialParams sets the serial trigger's bus and match.
// TRLC-LINKS: REQ-SDS-013
func (f *fakeEng) SetSerialParams(p engine.SerialParams) {
	f.statMu.Lock()
	f.serial = p
	f.stats.SerialSet = len(p.Bytes) > 0 || len(p.Pattern) > 0
	f.statMu.Unlock()
}

// SerialConfig is the serial trigger's configuration.
// TRLC-LINKS: REQ-SDS-013
func (f *fakeEng) SerialConfig() engine.SerialParams {
	f.statMu.Lock()
	defer f.statMu.Unlock()
	p := f.serial
	p.Bytes = append([]int(nil), p.Bytes...)
	p.Pattern = append([]engine.PatternElem(nil), p.Pattern...)
	return p
}

// TRLC-LINKS: REQ-SDS-135
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TRLC-LINKS: REQ-SDS-136
func inInts(v int, opts ...int) bool {
	for _, o := range opts {
		if v == o {
			return true
		}
	}
	return false
}

// TRLC-LINKS: REQ-SDS-136
func inIntRange(v, lo, hi int) bool { return v >= lo && v <= hi }

// TRLC-LINKS: REQ-SDS-135
type fakeFE struct {
	calls   []call
	idx     [2]int
	offReqV [2]float64
	cpl     [2]int
	probe   [2]float64
	bwl     [2]bool
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) SetVdiv(ch, idx int) error {
	f.calls = append(f.calls, call{"vdiv", ch, idx})
	f.idx[ch] = idx
	return nil
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) Snapshot() ([2]int, bool) { return f.idx, false }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) SetOffset(ch int, volts float64) uint16 {
	f.calls = append(f.calls, call{"offset", ch, int(volts * 100)})
	f.offReqV[ch] = volts
	return uint16(10223 - int(volts*100))
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) OffsetReqV(ch int) float64 { return f.offReqV[ch] }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) OffsetVolts(ch int, code uint16) float64 { return (10223 - float64(code)) / 100 }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) OffsetK(ch int) float64 { return 100 }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) SetCoupling(ch, mode int) error {
	f.calls = append(f.calls, call{"coupling", ch, mode})
	f.cpl[ch] = mode
	return nil
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) Coupling(ch int) int { return f.cpl[ch] }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) SetProbe(ch int, x float64) {
	f.calls = append(f.calls, call{"probe", ch, int(x)})
	f.probe[ch] = x
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) ProbeFactor(ch int) float64 {
	if f.probe[ch] >= 1 {
		return f.probe[ch]
	}
	return 1
}

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) TrigCode(volts float64, srcCh int) float64 { return 31437 - 911*volts }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) TrigVolts(code uint16, srcCh int) float64 { return (31437 - float64(code)) / 911 }

// TRLC-LINKS: REQ-SDS-135
func (f *fakeFE) SetTrigCalDetent(ch, detent int, c analog.TrigCal) {}

// bwlFE is a front end with the 20 MHz bandwidth limit relays.
type bwlFE struct{ *fakeFE }

// SetBWL sets a channel's bandwidth limit.
func (f bwlFE) SetBWL(ch int, on bool) error { f.bwl[ch&1] = on; return nil }

// BWL reports a channel's bandwidth limit.
func (f bwlFE) BWL(ch int) bool { return f.bwl[ch&1] }

// fakeInv is the display invert (the SCPI INVS shadow).
type fakeInv struct {
	mu  sync.Mutex
	inv [2]bool
}

// Inverted reports both channels' invert.
func (f *fakeInv) Inverted() [2]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inv
}

// SetInverted sets a channel's invert.
func (f *fakeInv) SetInverted(ch int, on bool) {
	f.mu.Lock()
	f.inv[ch&1] = on
	f.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-135
func idle() [5]uint16 {
	return [5]uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF, 0}
}

// newEng is the engine at the state the panel tests start from, with the
// real engine's boot qualifiers (levels set, windows unset).
func newEng() *fakeEng {
	return &fakeEng{matrix: idle(), stats: engine.Stats{
		Running: true, TrigCode: 31434, TdivS: 500e-6, TrigPosFrac: 0.5,
		AvgCount: 16, EresLen: 1, MemDepth: 20480,
		TrigQual: engine.TrigQual{PulseLvl: 0.5, SlopeLo: 0.2, SlopeHi: 0.8, VideoNeg: true},
	}}
}

// TRLC-LINKS: REQ-SDS-135
func newC(t *testing.T) (*Controller, *fakeEng, *fakeFE) {
	t.Helper()
	fe := &fakeFE{idx: [2]int{8, 8}} // boot detent
	eng := newEng()
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true) // seed baseline
	return c, eng, fe
}

// testClock is a manual clock for notices, rail labels and statistics.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (k *testClock) now() time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.t
}

// advance moves the clock on by d.
func (k *testClock) advance(d time.Duration) {
	k.mu.Lock()
	k.t = k.t.Add(d)
	k.mu.Unlock()
}

// withClock gives c a manual clock.
func withClock(c *Controller) *testClock {
	k := &testClock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	c.SetClock(k.now)
	return k
}

// press runs physical buttons by their API names, as the panel does.
func press(t *testing.T, c *Controller, names ...string) {
	t.Helper()
	for _, n := range names {
		code, ok := nameCode(n)
		if !ok {
			t.Fatalf("no button %q", n)
		}
		c.button(code)
	}
}

// turn turns a knob n detents (negative: counter-clockwise), one event each.
func turn(c *Controller, knob string, n int) {
	dir := 1
	if n < 0 {
		dir, n = -1, -n
	}
	for i := 0; i < n; i++ {
		c.resync()
		c.dispatch(knob, dir, 1)
	}
}

// goRow reaches a row with panel inputs only: Menu On/Off opens the
// workspace's sheet, S/DIV turns to the row's tab, Adjust moves to the row.
func goRow(t *testing.T, c *Controller, id string) {
	t.Helper()
	if rowByID[id] == nil {
		t.Fatalf("no row %q", id)
	}
	if !c.PanelStatus().Sheet.Open {
		press(t, c, "menu")
	}
	want := sheetTabs[rowTab[id]].id
	for i := 0; i <= numTabs && c.PanelStatus().Sheet.Tab != want; i++ {
		turn(c, "tdiv", 1)
	}
	if got := c.PanelStatus().Sheet.Tab; got != want {
		t.Fatalf("S/DIV never reached tab %s (at %s)", want, got)
	}
	for i := 0; i <= len(sheetTabs[rowTab[id]].rows); i++ {
		if r := c.PanelStatus().Sheet.Row; r != nil && r.ID == id {
			return
		}
		turn(c, "adjust", 1)
	}
	t.Fatalf("Adjust never reached row %s", id)
}

// pickKey presses the F-key whose label is label.
func pickKey(t *testing.T, c *Controller, label string) {
	t.Helper()
	for i, l := range keyLabels(c) {
		if l == label {
			c.button(softkeys[i])
			return
		}
	}
	t.Fatalf("no F-key %q in %q", label, keyLabels(c))
}

// rowText is a row's value as written on the sheet.
func rowText(c *Controller, id string) string { return rowByID[id].valueText(c.snap()) }

// keyLabels are the five F-key labels as the status reports them.
func keyLabels(c *Controller) []string {
	st := c.PanelStatus()
	var out []string
	for _, k := range st.FKeys {
		out = append(out, k.Label)
	}
	return out
}

// mustState fails unless the UI is in the given workspace and mode.
func mustState(t *testing.T, c *Controller, ws, mode string) Status {
	t.Helper()
	st := c.PanelStatus()
	if st.Workspace != ws || st.Mode != mode {
		t.Fatalf("state %s/%s, want %s/%s", st.Workspace, st.Mode, ws, mode)
	}
	return st
}

// TRLC-LINKS: REQ-SDS-135
func TestSingleThenRunLamp(t *testing.T) {
	c, eng, _ := newC(t)
	press(t, c, "single")
	if eng.lastLEDs()&ledSingle == 0 {
		t.Fatalf("SINGLE lamp not lit after SINGLE: %#04x", eng.lastLEDs())
	}
	press(t, c, "runstop") // RUN/STOP leaves single mode → lamp must darken
	if eng.lastLEDs()&ledSingle != 0 {
		t.Fatalf("SINGLE lamp still lit after RUN/STOP: %#04x", eng.lastLEDs())
	}
}

// The lamp bits match spec 02 §7.5, each channel lamp follows its trace, the
// MATH and CURSORS lamps follow their functions, and INTENSITY, which no
// firmware has lit, stays dark (spec 08 §8.2: one meaning each).
// TRLC-LINKS: REQ-SDS-135
func TestLEDMap(t *testing.T) {
	if ledCH1 != 0x0010 || ledMath != 0x0020 || ledCH2 != 0x0040 || ledRef != 0x0080 || ledCursors != 0x0002 {
		t.Fatalf("LED bits drifted from spec: CH1=%#x MATH=%#x CH2=%#x REF=%#x CURSORS=%#x (want 0x10/0x20/0x40/0x80/0x02)",
			ledCH1, ledMath, ledCH2, ledRef, ledCursors)
	}
	c, eng, _ := newC(t)
	c.SetTraceOn(1, false)
	if w := eng.lastLEDs(); w&ledCH2 != 0 || w&ledCH1 == 0 {
		t.Errorf("C2 off: lamps %#x", w)
	}
	c.SetTraceOn(1, true)
	if w := eng.lastLEDs(); w&ledCH2 == 0 || w&ledMath != 0 {
		t.Errorf("C2 on (the reported bug: C2 lit the MATH lamp): %#x", w)
	}
	for _, id := range []string{"cmp.math"} {
		if r := c.SheetSet(id, nil, "C1-C2"); !r.OK {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	c.SetCursorsOn(true)
	c.afterInput()
	if w := eng.lastLEDs(); w&(ledMath|ledCursors) != ledMath|ledCursors || w&(ledRef|ledIntens) != 0 {
		t.Errorf("math and cursors on, no reference: lamps %#x", w)
	}
	c.SetTraceOn(0, false)
	if w := eng.lastLEDs(); w&ledCH1 != 0 || w&ledCH2 == 0 {
		t.Errorf("C1 off: lamps %#x", w)
	}
}

// keyNamesOf lists the keys of a map (for messages).
func keyNamesOf[V any](m map[string]V) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ",")
}
