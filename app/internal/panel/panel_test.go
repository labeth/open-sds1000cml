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
func (f *fakeEng) SetMemDepth(n int) int    { f.calls = append(f.calls, call{"memdepth", n, 0}); return n }
func (f *fakeEng) SetPulseParams(l, mn, mx float64, c int) {
	f.calls = append(f.calls, call{"pulse", c, 0})
}
func (f *fakeEng) SetSlopeParams(lo, hi, mn, mx float64, c int) {
	f.calls = append(f.calls, call{"slope", c, 0})
}
func (f *fakeEng) SetVideoParams(std, line int, neg bool) {
	f.calls = append(f.calls, call{"video", std, line})
}
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
func (f *fakeFE) OffsetReqV(ch int) float64          { return f.offReqV[ch] }
func (f *fakeFE) OffsetVolts(ch int, code uint16) float64 { return (10223 - float64(code)) / 262 }
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
	// Two knobs "moving" at once: HORIZ POSITION (pri 1) must win over TIME/DIV
	// (pri 3) — exactly one knob is serviced per event, so trigpos is dispatched
	// and tdiv is NOT.
	m := idle()
	m[3] &^= 1 << 14 // horizpos CW
	m[2] &^= 1 << 14 // tdiv CW
	m[4] = 1
	c.decode(m, true)
	if len(eng.calls) != 1 || eng.calls[0].what != "trigpos" {
		t.Fatalf("expected exactly one trigpos dispatch (horizpos wins priority), got %v", eng.calls)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("no front-end call expected, got %v", fe.calls)
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

func TestDecodeMenu(t *testing.T) {
	c, _, _ := newC(t)
	c.menuButton(btnMenuOnOff) // MAIN menu
	if v := c.MenuView(); v.Title != "MENU" {
		t.Fatalf("MENU not open: %q", v.Title)
	}
	c.menuButton(btnF5) // slot 4 -> Decode
	if v := c.MenuView(); v.Title != "DECODE" {
		t.Fatalf("DECODE page not open: %q", v.Title)
	}
	// Auto is FIRST after Off (most used) — one F1 press reaches it, and it shows
	// only Proto + the format slot.
	c.menuButton(btnF1)
	if v := c.MenuView(); v.DecProto != 1 || v.Items[0].Value != "Auto" || v.Items[1].Label != "Show" {
		t.Fatalf("Auto not first/labelled: proto=%d items=%+v", v.DecProto, v.Items)
	}
	// Auto fills slots 0..1, so F3 (slot 2) is inert.
	sel := c.MenuView().Sel
	c.menuButton(btnF3)
	if got := c.MenuView().Sel; got != sel {
		t.Fatalf("F3 moved highlight onto an empty Auto slot: %d -> %d", sel, got)
	}
	// Auto -> UART.
	c.menuButton(btnF1)
	if v := c.MenuView(); v.DecProto != 2 || v.Items[1].Label != "Baud" || v.Items[2].Label != "Source" {
		t.Fatalf("UART not selected/labelled: proto=%d items=%+v", v.DecProto, v.Items)
	}
	// UART -> I2C. Clock and data must never share a channel (slot 1=SCL, 2=SDA).
	c.menuButton(btnF1)
	v := c.MenuView()
	if v.DecProto != 3 || v.Items[1].Label != "SCL" || v.Items[2].Label != "SDA" {
		t.Fatalf("I2C not selected/labelled: proto=%d items=%+v", v.DecProto, v.Items)
	}
	if v.DecChA == v.DecChB {
		t.Fatalf("SCL/SDA share a channel at start: A=%d B=%d", v.DecChA, v.DecChB)
	}
	c.menuButton(btnF2) // toggle SCL
	if v := c.MenuView(); v.DecChA == v.DecChB {
		t.Fatalf("SCL/SDA share a channel after SCL toggle: A=%d B=%d", v.DecChA, v.DecChB)
	}
	c.menuButton(btnF3) // toggle SDA
	if v := c.MenuView(); v.DecChA == v.DecChB {
		t.Fatalf("SCL/SDA share a channel after SDA toggle: A=%d B=%d", v.DecChA, v.DecChB)
	}
	// I2C carries a "Show" (byte format) slot at slot 3, cycling Hex/ASCII/Both.
	if v := c.MenuView(); v.Items[3].Label != "Show" || v.Items[3].Value != "Hex" {
		t.Fatalf("I2C Show slot missing/wrong: %+v", v.Items[3])
	}
	c.menuButton(btnF4) // slot 3 = Show -> ASCII
	if v := c.MenuView(); v.DecFormat != 1 || v.Items[3].Value != "ASCII" {
		t.Fatalf("Show did not cycle to ASCII: fmt=%d items=%+v", v.DecFormat, v.Items[3])
	}
	// I2C fills slots 0..3, so F5 (slot 4) is inert.
	sel = c.MenuView().Sel
	c.menuButton(btnF5)
	if got := c.MenuView().Sel; got != sel {
		t.Fatalf("F5 moved the highlight onto an empty I2C slot: %d -> %d", sel, got)
	}
	// I2C -> SPI: real 4th slot (Mode) plus Show on slot 4.
	c.menuButton(btnF1)
	if v := c.MenuView(); v.DecProto != 4 || v.Items[3].Label != "Mode" || v.Items[4].Label != "Show" {
		t.Fatalf("SPI slots wrong: proto=%d items=%+v", v.DecProto, v.Items)
	}
	c.menuButton(btnF5) // slot 4 = Show live in SPI
	if got := c.MenuView().Sel; got != 4 {
		t.Fatalf("F5 in SPI did not reach the Show slot: Sel=%d", got)
	}
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

func TestKnobPushTrigger(t *testing.T) {
	c, eng, _ := newC(t)
	last := func() call { return eng.calls[len(eng.calls)-1] }
	// Push CH1 V/DIV (0x65:9 → m[1] bit 9) → trigger source C1.
	m := idle()
	m[1] &^= 1 << 9
	c.decode(m, true)
	if got := last(); got != (call{"src", 0, 0}) {
		t.Fatalf("CH1 V/DIV push → trig source C1: got %v", got)
	}
	c.decode(idle(), true) // release
	// Push CH2 V/DIV (0x66:1 → m[2] bit 1) → trigger source C2.
	m = idle()
	m[2] &^= 1 << 1
	c.decode(m, true)
	if got := last(); got != (call{"src", 1, 0}) {
		t.Fatalf("CH2 V/DIV push → trig source C2: got %v", got)
	}
	c.decode(idle(), true)
	// Push TRIG LEVEL (0x64:9 → m[0] bit 9) → flip slope (default rising=false → true).
	m = idle()
	m[0] &^= 1 << 9
	c.decode(m, true)
	if got := last(); got != (call{"slope", 1, 0}) {
		t.Fatalf("TRIG LEVEL push → flip slope to rising: got %v", got)
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

// TestSuperresUX drives the device super-res state machine: UTILITY arms/cancels
// (like SINGLE), the SUPER-RES page maps to the softkeys, and ADJUST-push toggles
// the review view. Constant-Seq frame source → the stacker seeds once and idles
// on dedup, so the test sees only the synchronous transitions (the stacking
// numerics are covered by the golden-vector parity test).
func TestSuperresUX(t *testing.T) {
	c, eng, _ := newC(t)
	page := func() int { c.mu.Lock(); defer c.mu.Unlock(); return c.menuPage }

	n := 256
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(40 + (i*7)%160) // varied — not flat, not railed
	}
	fr := &engine.Frame{C1: sig, C2: sig, Valid: n, EdgeX: 32, SampleS: 1e-9, Seq: 0}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })

	// UTILITY arms super-res.
	c.button(btnUtility)
	if !c.SuperresView().Active {
		t.Fatal("UTILITY did not arm super-res")
	}
	if page() != pgSuperres {
		t.Fatalf("arming did not open the SUPER-RES page: page=%d", page())
	}
	if got := eng.calls[len(eng.calls)-1]; got != (call{"run", 1, 0}) {
		t.Fatalf("arm did not resume RUN: %v", got)
	}
	if w := eng.leds[len(eng.leds)-1]; w&ledUtility == 0 {
		t.Errorf("UTILITY lamp not lit while active: %#x", w)
	}

	// Default menu: Channel C1 / Grid x32 / Stop on bits / Target +4.0b / Reset.
	want := []MenuItem{{"Channel", "C1"}, {"Grid", "x32"}, {"Stop on", "bits"}, {"Target", "+4.0b"}, {"Reset", ""}}
	v := c.MenuView()
	if v.Title != "SUPER-RES" || len(v.Items) != 5 {
		t.Fatalf("SUPER-RES menu wrong: title=%q items=%d", v.Title, len(v.Items))
	}
	for i, w := range want {
		if v.Items[i] != w {
			t.Errorf("slot %d = %+v, want %+v", i, v.Items[i], w)
		}
	}

	// F3 (slot 2) cycles the stop mode bits→stacks and reseeds a sensible target.
	c.menuButton(btnF3)
	if v = c.MenuView(); v.Items[2].Value != "stacks" || v.Items[3].Value != "500" {
		t.Errorf("stop-mode cycle: %+v / %+v", v.Items[2], v.Items[3])
	}

	// F1 (slot 0) toggles the aligned channel C1→C2 (rebuilds the stack).
	c.menuButton(btnF1)
	if v = c.MenuView(); v.Items[0].Value != "C2" {
		t.Errorf("channel toggle: %+v", v.Items[0])
	}

	// ADJUST/intensity push cycles focus: watch → gate-start → gate-end → review → watch.
	if f := c.SuperresView().Focus; f != 0 {
		t.Errorf("armed focus = %d, want 0 (watch)", f)
	}
	for i, want := range []int{1, 2, 3, 0} {
		c.button(btnAdjustPsh)
		if f := c.SuperresView().Focus; f != want {
			t.Errorf("ADJUST-push #%d → focus %d, want %d", i+1, f, want)
		}
	}

	// Manual gate: focus the start edge, nudge with ADJUST → the gate moves and the
	// stack re-seeds on the new (manual) gate.
	c.mu.Lock()
	c.srFocus = 1 // gate-start
	c.mu.Unlock()
	lo0 := c.SuperresView().GateLo
	if !c.srGateAdjust(3) {
		t.Error("srGateAdjust not consumed while a gate edge is focused")
	}
	if lo := c.SuperresView().GateLo; lo <= lo0 {
		t.Errorf("gate start did not move right: %d → %d", lo0, lo)
	}

	// UTILITY again cancels: mode off, page closed, lamp dark.
	c.button(btnUtility)
	if c.SuperresView().Active {
		t.Fatal("second UTILITY did not cancel super-res")
	}
	if page() != pgNone {
		t.Errorf("cancel did not close the menu: page=%d", page())
	}
	if w := eng.leds[len(eng.leds)-1]; w&ledUtility != 0 {
		t.Errorf("UTILITY lamp still lit after cancel: %#x", w)
	}
}
