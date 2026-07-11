package panel

import (
	"testing"

	"open-sds/app/internal/engine"
)

type call struct {
	what string
	a, b int
}

type fakeEng struct {
	matrix [5]uint16
	calls  []call
	leds   []uint16
	stats  engine.Stats
	acqLog []engine.AcqSample
}

func (f *fakeEng) ReadMatrix() ([5]uint16, bool)              { return f.matrix, true }
func (f *fakeEng) SetLEDs(w uint16)                           { f.leds = append(f.leds, w) }
func (f *fakeEng) Snapshot() engine.Stats                     { return f.stats }
func (f *fakeEng) AcqLog(n int) ([]engine.AcqSample, float64) { return f.acqLog, 0 }
func (f *fakeEng) SetOffsetDAC(ch int, code uint16) {
	f.calls = append(f.calls, call{"offset", ch, int(code)})
}
func (f *fakeEng) SetTrigLevelCode(code uint16) uint16 {
	f.calls = append(f.calls, call{"triglevel", int(code), 0})
	return code
}
func (f *fakeEng) SetTdiv(t float64) (engine.Band, bool) {
	f.calls = append(f.calls, call{"tdiv", int(t * 1e9), 0})
	return engine.Band{}, true
}
func (f *fakeEng) SetNorm(on bool)       { f.calls = append(f.calls, call{"norm", b2i(on), 0}) }
func (f *fakeEng) SetRunning(on bool)    { f.calls = append(f.calls, call{"run", b2i(on), 0}) }
func (f *fakeEng) SetSingle()            { f.calls = append(f.calls, call{"single", 0, 0}) }
func (f *fakeEng) SetTrigSlope(r bool)   { f.calls = append(f.calls, call{"slope", b2i(r), 0}) }
func (f *fakeEng) SetTrigSource(ch int)  { f.calls = append(f.calls, call{"src", ch, 0}) }
func (f *fakeEng) SetTrigType(t int)     { f.calls = append(f.calls, call{"ttype", t, 0}) }
func (f *fakeEng) SetAcqMode(m int)      { f.calls = append(f.calls, call{"acq", m, 0}) }
func (f *fakeEng) SetAvgCount(n int)     { f.calls = append(f.calls, call{"avg", n, 0}) }
func (f *fakeEng) SetEresLen(l int)      { f.calls = append(f.calls, call{"eres", l, 0}) }
func (f *fakeEng) SetETS(on bool)        { f.calls = append(f.calls, call{"ets", b2i(on), 0}) }
func (f *fakeEng) SetMemDepth(n int) int { f.calls = append(f.calls, call{"memdepth", n, 0}); return n }
func (f *fakeEng) SetPulseParams(l, mn, mx float64, c int) {
	f.calls = append(f.calls, call{"pulse", c, 0})
}
func (f *fakeEng) SetSlopeParams(lo, hi, mn, mx float64, c int) {
	f.calls = append(f.calls, call{"slope", c, 0})
}
func (f *fakeEng) SetMask(m *engine.Mask) { f.calls = append(f.calls, call{"mask", m.WinCols, 0}) }
func (f *fakeEng) SetMaskMode(m int)      { f.calls = append(f.calls, call{"maskmode", m, 0}) }
func (f *fakeEng) ClearMaskFails()        { f.calls = append(f.calls, call{"maskclear", 0, 0}) }
func (f *fakeEng) SetVideoParams(std, line int, neg bool) {
	f.calls = append(f.calls, call{"video", std, line})
}
func (f *fakeEng) SetTrigPosFrac(fr float64) {
	f.calls = append(f.calls, call{"trigpos", int(fr * 100), 0})
}
func (f *fakeEng) SetHoldoff(s float64) float64 {
	f.calls = append(f.calls, call{"holdoff", int(s * 1e6), 0})
	return s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type fakeFE struct {
	calls   []call
	idx     [2]int
	offReqV [2]float64
	cpl     [2]int
	probe   [2]float64
}

func (f *fakeFE) SetVdiv(ch, idx int) error {
	f.calls = append(f.calls, call{"vdiv", ch, idx})
	f.idx[ch] = idx
	return nil
}
func (f *fakeFE) Snapshot() ([2]int, bool) { return f.idx, false }
func (f *fakeFE) SetOffset(ch int, volts float64) uint16 {
	f.calls = append(f.calls, call{"offset", ch, int(volts * 100)})
	f.offReqV[ch] = volts
	return uint16(10223 - int(volts*100))
}
func (f *fakeFE) OffsetReqV(ch int) float64               { return f.offReqV[ch] }
func (f *fakeFE) OffsetVolts(ch int, code uint16) float64 { return (10223 - float64(code)) / 100 }
func (f *fakeFE) OffsetK(ch int) float64                  { return 100 }
func (f *fakeFE) SetCoupling(ch, mode int) error {
	f.calls = append(f.calls, call{"coupling", ch, mode})
	f.cpl[ch] = mode
	return nil
}
func (f *fakeFE) Coupling(ch int) int { return f.cpl[ch] }
func (f *fakeFE) SetProbe(ch int, x float64) {
	f.calls = append(f.calls, call{"probe", ch, int(x)})
	f.probe[ch] = x
}
func (f *fakeFE) ProbeFactor(ch int) float64 {
	if f.probe[ch] >= 1 {
		return f.probe[ch]
	}
	return 1
}

func idle() [5]uint16 {
	return [5]uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF, 0}
}

func newC(t *testing.T) (*Controller, *fakeEng, *fakeFE) {
	t.Helper()
	fe := &fakeFE{idx: [2]int{8, 8}} // boot detent
	// The engine's authoritative state (read by resync before each knob step).
	eng := &fakeEng{matrix: idle(), stats: engine.Stats{
		Running: true, TrigCode: 31434, TdivS: 500e-6,
	}}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true) // seed baseline
	return c, eng, fe
}

func TestSingleThenRunLamp(t *testing.T) {
	c, eng, _ := newC(t)
	c.button(btnSingle)
	if last := eng.leds[len(eng.leds)-1]; last&ledSingle == 0 {
		t.Fatalf("SINGLE lamp not lit after SINGLE: %#04x", last)
	}
	c.button(btnRunStop) // RUN/STOP leaves single mode → lamp must darken
	if last := eng.leds[len(eng.leds)-1]; last&ledSingle != 0 {
		t.Fatalf("SINGLE lamp still lit after RUN/STOP: %#04x", last)
	}
}

func TestLEDMap(t *testing.T) {
	// Shadow-word bits must match spec 02 §7.5 (corroborated PCB wiring).
	if ledCH1 != 0x0010 || ledMath != 0x0020 || ledCH2 != 0x0040 {
		t.Fatalf("LED bits drifted from spec: CH1=%#x MATH=%#x CH2=%#x (want 0x10/0x20/0x40)", ledCH1, ledMath, ledCH2)
	}
	c, eng, _ := newC(t)
	last := func() uint16 {
		if len(eng.leds) == 0 {
			t.Fatal("no LED word latched")
		}
		return eng.leds[len(eng.leds)-1]
	}
	// Both channels on by default; math off. Toggle C2 to force a fresh flush.
	c.menuButton(btnCh2) // C2 off
	if w := last(); w&ledCH2 != 0 {
		t.Errorf("CH2 lamp still lit after toggling C2 off: %#x", w)
	}
	c.menuButton(btnCh2) // C2 back on
	w := last()
	if w&ledCH2 == 0 {
		t.Errorf("CH2 lamp (0x40) not lit when C2 on: %#x", w)
	}
	if w&ledMath != 0 { // the reported bug: C2 was lighting the MATH lamp
		t.Errorf("MATH lamp lit while math is off: %#x", w)
	}
	if w&ledCH1 == 0 {
		t.Errorf("CH1 lamp (0x10) not lit when C1 on: %#x", w)
	}
	// A math function lights the MATH lamp (and only then).
	c.menuButton(btnMath)
	if w := last(); w&ledMath == 0 {
		t.Errorf("MATH lamp not lit when math active: %#x", w)
	}
	// Toggling C1 off updates its lamp immediately (direct key must flush LEDs).
	c.menuButton(btnCh1)
	w = last()
	if w&ledCH1 != 0 {
		t.Errorf("CH1 lamp should be dark after toggle: %#x", w)
	}
	if w&ledCH2 == 0 {
		t.Errorf("CH2 lamp should stay lit: %#x", w)
	}
}
