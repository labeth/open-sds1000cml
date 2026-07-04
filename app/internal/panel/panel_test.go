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
}

func (f *fakeEng) ReadMatrix() ([5]uint16, bool) { return f.matrix, true }
func (f *fakeEng) SetLEDs(w uint16)              { f.leds = append(f.leds, w) }
func (f *fakeEng) Snapshot() engine.Stats        { return f.stats }
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
func (f *fakeEng) SetNorm(on bool)    { f.calls = append(f.calls, call{"norm", b2i(on), 0}) }
func (f *fakeEng) SetRunning(on bool) { f.calls = append(f.calls, call{"run", b2i(on), 0}) }
func (f *fakeEng) SetSingle()               { f.calls = append(f.calls, call{"single", 0, 0}) }
func (f *fakeEng) SetTrigSlope(r bool)      { f.calls = append(f.calls, call{"slope", b2i(r), 0}) }
func (f *fakeEng) SetTrigSource(ch int)     { f.calls = append(f.calls, call{"src", ch, 0}) }
func (f *fakeEng) SetTrigType(t int)        { f.calls = append(f.calls, call{"ttype", t, 0}) }
func (f *fakeEng) SetAcqMode(m int)         { f.calls = append(f.calls, call{"acq", m, 0}) }
func (f *fakeEng) SetAvgCount(n int)        { f.calls = append(f.calls, call{"avg", n, 0}) }
func (f *fakeEng) SetEresLen(l int)         { f.calls = append(f.calls, call{"eres", l, 0}) }
func (f *fakeEng) SetETS(on bool)           { f.calls = append(f.calls, call{"ets", b2i(on), 0}) }
func (f *fakeEng) SetTrigPosFrac(fr float64) { f.calls = append(f.calls, call{"trigpos", int(fr * 100), 0}) }
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
	f.calls = append(f.calls, call{"offset", ch, int(volts * 262)})
	f.offReqV[ch] = volts
	return uint16(10223 - int(volts*262))
}
func (f *fakeFE) OffsetReqV(ch int) float64 { return f.offReqV[ch] }
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

func TestButtonEdges(t *testing.T) {
	c, eng, _ := newC(t)

	// RUN/STOP is sel 0x65 (idx 1) bit 2: press = 1→0 edge.
	m := idle()
	m[1] &^= 1 << 2
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0] != (call{"run", 0, 0}) {
		t.Fatalf("run/stop press: %v", eng.calls)
	}
	// Held (still low): no repeat.
	c.decode(m, true)
	if len(eng.calls) != 1 {
		t.Fatalf("held button repeated: %v", eng.calls)
	}
	// Release then press again: toggles back to running.
	c.decode(idle(), true)
	c.decode(m, true)
	if eng.calls[len(eng.calls)-1] != (call{"run", 1, 0}) {
		t.Fatalf("second press: %v", eng.calls)
	}
}

func TestSingleAndAuto(t *testing.T) {
	c, eng, _ := newC(t)
	m := idle()
	m[1] &^= 1 << 10 // SINGLE = 0x65 bit 10
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0] != (call{"single", 0, 0}) {
		t.Fatalf("single: %v", eng.calls)
	}
	// SINGLE LED set.
	if last := eng.leds[len(eng.leds)-1]; last&ledSingle == 0 {
		t.Fatalf("single LED not set: %#04x", last)
	}
	c.decode(idle(), true)
	m = idle()
	m[3] &^= 1 << 10 // AUTO = 0x67 bit 10
	c.decode(m, true)
	if eng.calls[len(eng.calls)-2] != (call{"norm", 0, 0}) {
		t.Fatalf("auto: %v", eng.calls)
	}
}

func TestKnobPriorityOneRowPerEvent(t *testing.T) {
	c, eng, fe := newC(t)
	// Two knobs "moving" at once: HORIZ POSITION (pri 1, claim-ignore) must
	// win over TIME/DIV (pri 3) — nothing dispatched to tdiv.
	m := idle()
	m[3] &^= 1 << 14 // horizpos CW
	m[2] &^= 1 << 14 // tdiv CW
	m[4] = 1
	c.decode(m, true)
	if len(eng.calls) != 0 || len(fe.calls) != 0 {
		t.Fatalf("priority walk dispatched more than the first knob: %v %v", eng.calls, fe.calls)
	}
}

func TestTdivKnob(t *testing.T) {
	c, eng, _ := newC(t)
	// TIME/DIV CW (bit14 low): +1 detent (500µs → 1ms), stepped (0x69 ignored).
	m := idle()
	m[2] &^= 1 << 14
	m[4] = 37 // magnitude must be ignored on stepped knobs
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0].what != "tdiv" || eng.calls[0].a != int(1e-3*1e9) {
		t.Fatalf("tdiv CW: %v", eng.calls)
	}
	// Sustained rotation: bit stays low next event (no phase change) —
	// must still step (resting-bit decode, not edge decode).
	c.decode(idle(), true)
	c.decode(m, true)
	if len(eng.calls) != 2 {
		t.Fatalf("sustained rotation missed: %v", eng.calls)
	}
}

func TestVdivKnob(t *testing.T) {
	c, _, fe := newC(t)
	// CH1 V/DIV CCW (0x65 bit15 low): 1V (idx 8) → 500mV (idx 7).
	m := idle()
	m[1] &^= 1 << 15
	m[4] = 1
	c.decode(m, true)
	if len(fe.calls) != 1 || fe.calls[0] != (call{"vdiv", 0, 7}) {
		t.Fatalf("ch1 vdiv CCW: %v", fe.calls)
	}
}

func TestTrigLevelSign(t *testing.T) {
	c, eng, _ := newC(t)
	// TRIG LEVEL CW must LOWER the code: 31434 − 1·40·1 = 31394.
	m := idle()
	m[0] &^= 1 << 14
	m[4] = 1
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0] != (call{"triglevel", 31394, 0}) {
		t.Fatalf("trig level CW: %v", eng.calls)
	}
}

func TestPositionKnobAccel(t *testing.T) {
	c, _, fe := newC(t)
	// CH1 POSITION (continuous) with raw 0x69 = 25 → 100 steps. Each step is
	// 20 codes = 20/262 V; CW (+1) → +100·20/262 V, routed through fe.
	m := idle()
	m[0] &^= 1 << 6
	m[4] = 25
	c.decode(m, true)
	want := 100 * 20.0 / 262.0
	if len(fe.calls) != 1 || fe.calls[0].what != "offset" || fe.offReqV[0] < want-1e-9 || fe.offReqV[0] > want+1e-9 {
		t.Fatalf("ch1 pos accel: fe=%+v want volts %v", fe.calls, want)
	}
}

func TestKnobResyncFromEngine(t *testing.T) {
	c, eng, fe := newC(t)
	// Web/SCPI moved trigger level to 30000 and V/div to idx 5 behind the
	// panel's back; the next knob step must start from THAT, not a stale
	// shadow.
	eng.stats.TrigCode = 30000
	fe.idx = [2]int{5, 5}
	m := idle()
	m[0] &^= 1 << 14 // TRIG LEVEL CW: 30000 − 40 = 29960
	m[4] = 1
	c.decode(m, true)
	if eng.calls[len(eng.calls)-1] != (call{"triglevel", 29960, 0}) {
		t.Fatalf("resync trig: %v", eng.calls)
	}
	// V/div CW from the resynced idx 5 → 6, not from the stale boot idx 8.
	m = idle()
	m[1] &^= 1 << 14
	m[4] = 1
	c.decode(m, true)
	if fe.calls[len(fe.calls)-1] != (call{"vdiv", 0, 6}) {
		t.Fatalf("resync vdiv: %v", fe.calls)
	}
}

func TestAccelMap(t *testing.T) {
	cases := map[uint16]int{0: 0, 5: 5, 9: 9, 10: 50, 19: 50, 20: 100, 150: 100, 1000: 100}
	for raw, want := range cases {
		if got := accel(raw); got != want {
			t.Errorf("accel(%d) = %d, want %d", raw, got, want)
		}
	}
}

func TestKnobGateOnZeroMagnitude(t *testing.T) {
	c, eng, fe := newC(t)
	// Phase bit low but 0x69 == 0: plain button interrupt, no knob move.
	m := idle()
	m[2] &^= 1 << 14
	m[4] = 0
	c.decode(m, true)
	if len(eng.calls) != 0 || len(fe.calls) != 0 {
		t.Fatalf("knob moved with zero magnitude: %v %v", eng.calls, fe.calls)
	}
}

func TestResyncButtonsOnly(t *testing.T) {
	c, eng, _ := newC(t)
	// Knob phase low + magnitude on a BUTTONS-ONLY decode (40 ms tick):
	// no knob dispatch.
	m := idle()
	m[2] &^= 1 << 14
	m[4] = 3
	c.decode(m, false)
	if len(eng.calls) != 0 {
		t.Fatalf("knob decoded on the re-sync tick: %v", eng.calls)
	}
}

func TestCursorMenu(t *testing.T) {
	c, _, _ := newC(t)
	// HORIZONTAL once → timebase page; twice → cursor page.
	c.menuButton(btnHorizMenu)
	c.menuButton(btnHorizMenu)
	if v := c.MenuView(); v.Title != "CURSOR" {
		t.Fatalf("second HORIZ did not open the cursor page: %q", v.Title)
	}
	// F1 toggles cursors on.
	c.menuButton(btnF1)
	if !c.MenuView().CurOn {
		t.Fatal("F1 did not enable cursors")
	}
	// ADJUST moves the active (A) X-cursor; default 0.35 → +2 steps ≈ 0.37.
	before := c.MenuView().CurX[0]
	c.menuAdjust(+1)
	c.menuAdjust(+1)
	if got := c.MenuView().CurX[0]; got <= before {
		t.Fatalf("ADJUST did not move cursor A: %v → %v", before, got)
	}
	// F3 switches the active cursor to B; ADJUST then moves B, not A.
	aFixed := c.MenuView().CurX[0]
	c.menuButton(btnF3)
	c.menuAdjust(-1)
	v := c.MenuView()
	if v.CurSel != 1 || v.CurX[0] != aFixed {
		t.Fatalf("active cursor switch failed: sel=%d A=%v(want %v)", v.CurSel, v.CurX[0], aFixed)
	}
	// F2 flips to volts cursors.
	c.menuButton(btnF2)
	if c.MenuView().CurType != 1 {
		t.Fatal("F2 did not switch to volts cursors")
	}
}

func TestTriggerHoldoffSoftkey(t *testing.T) {
	c, eng, _ := newC(t)
	c.menuButton(btnTrigMenu) // open TRIGGER page
	if v := c.MenuView(); v.Items[4].Label != "Holdoff" || v.Items[4].Value != "Off" {
		t.Fatalf("holdoff softkey missing/wrong: %+v", v.Items[4])
	}
	// F5 (slot 4) steps the holdoff ladder Off -> 100us.
	c.menuButton(btnF5)
	last := eng.calls[len(eng.calls)-1]
	if last != (call{"holdoff", 100, 0}) { // 100us = 100e-6 * 1e6
		t.Fatalf("F5 did not step holdoff to 100us: %v", last)
	}
	// Reflect it back and confirm the menu formats it.
	eng.stats.HoldoffS = 100e-6
	if v := c.MenuView(); v.Items[4].Value == "Off" {
		t.Fatalf("holdoff value not shown after set: %+v", v.Items[4])
	}
}
