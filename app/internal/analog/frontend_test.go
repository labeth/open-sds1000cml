// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG
package analog

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/cal"
)

// TRLC-LINKS: REQ-SDS-015
type op struct {
	kind string // "relay" | "gain"
	word uint32
	ch2  uint8
	ch1  uint8
}

// TRLC-LINKS: REQ-SDS-015, REQ-SDS-095, REQ-SDS-096
type fakeTr struct {
	ops    []op
	sleeps []time.Duration
}

// TRLC-LINKS: REQ-SDS-015
func (f *fakeTr) WriteRelay(word uint32) error {
	f.ops = append(f.ops, op{kind: "relay", word: word})
	return nil
}

// TRLC-LINKS: REQ-SDS-015
func (f *fakeTr) WriteGain(ch2, ch1 uint8) error {
	f.ops = append(f.ops, op{kind: "gain", ch2: ch2, ch1: ch1})
	return nil
}

// TRLC-LINKS: REQ-SDS-015
func newFE(tr *fakeTr) *FrontEnd {
	return New(tr, func(d time.Duration) { tr.sleeps = append(tr.sleeps, d) }, nil)
}

// TRLC-LINKS: REQ-SDS-015
func TestSeedDoesNotEmit(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	idx, emitted := fe.Snapshot()
	if idx != [2]int{BootDetent, BootDetent} || emitted {
		t.Fatalf("seed: idx=%v emitted=%v", idx, emitted)
	}
	if len(tr.ops) != 0 {
		t.Fatalf("seed emitted %d SPI ops, want none (inherited range kept)", len(tr.ops))
	}
}

// TRLC-LINKS: REQ-SDS-015
func TestSetVdivSequence(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	// C1 to 100 mV (idx 5, sensitive, gain 12); C2 stays at boot 1 V (att, 57).
	if err := fe.SetVdiv(0, 5); err != nil {
		t.Fatal(err)
	}
	if len(tr.ops) != 2 || tr.ops[0].kind != "relay" || tr.ops[1].kind != "gain" {
		t.Fatalf("ops = %+v, want relay then gain", tr.ops)
	}
	// CH1 sensitive: 0x20|0x01|0x08 = 0x29; CH2 attenuated: 0x2d|0x80 = 0xad;
	// byte2 = 0x70 (DC nibble, source C1). Full word 0x70ad29.
	if got := tr.ops[0].word; got != 0x70ad29 {
		t.Fatalf("relay word = %#06x, want 0x70ad29", got)
	}
	// Relay settle between relay and gain.
	if len(tr.sleeps) != 1 || tr.sleeps[0] != 400*time.Microsecond {
		t.Fatalf("settle = %v, want one 400µs sleep", tr.sleeps)
	}
	// Gain: CH2 first (boot 57), then CH1 (100 mV = 12).
	if tr.ops[1].ch2 != 57 || tr.ops[1].ch1 != 12 {
		t.Fatalf("gain = ch2:%d ch1:%d, want 57/12", tr.ops[1].ch2, tr.ops[1].ch1)
	}
	if _, emitted := fe.Snapshot(); !emitted {
		t.Fatal("emitted flag not set")
	}
}

// TRLC-LINKS: REQ-SDS-015
func TestRelayWordReference(t *testing.T) {
	// Spec 06 §4.2 reference: DC / BWL-off / both attenuated → 0x70ad2d.
	tr := &fakeTr{}
	fe := newFE(tr)
	if err := fe.SetVdiv(0, BootDetent); err != nil { // both at boot detent
		t.Fatal(err)
	}
	if got := tr.ops[0].word; got != 0x70ad2d {
		t.Fatalf("boot relay word = %#06x, want 0x70ad2d", got)
	}
}

// TRLC-LINKS: REQ-SDS-015
func lastRelay(tr *fakeTr) (uint32, bool) {
	for i := len(tr.ops) - 1; i >= 0; i-- {
		if tr.ops[i].kind == "relay" {
			return tr.ops[i].word, true
		}
	}
	return 0, false
}

// TRLC-LINKS: REQ-SDS-096
func TestCouplingIsSoftwareOnly(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	// Coupling never touches the relay (software-only on this clone). It emits
	// nothing on its own...
	for _, m := range []int{CplGND, CplAC, CplDC} {
		if err := fe.SetCoupling(0, m); err != nil {
			t.Fatal(err)
		}
	}
	if len(tr.ops) != 0 {
		t.Fatalf("coupling emitted %d SPI ops, want 0 (software-only)", len(tr.ops))
	}
	if err := fe.SetCoupling(0, 9); err == nil {
		t.Fatal("bad coupling mode accepted")
	}
	// ...and even after a V/div emit with GND selected, the relay word stays DC
	// (byte0 0x2d), never the unsafe/ineffective GND (bit1) path.
	if err := fe.SetCoupling(0, CplGND); err != nil {
		t.Fatal(err)
	}
	if err := fe.SetVdiv(0, BootDetent); err != nil {
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70ad2d {
		t.Fatalf("relay word with GND selected = %#06x, want DC 0x70ad2d", w)
	}
	if fe.Coupling(0) != CplGND {
		t.Fatal("coupling mode not recorded")
	}
}

// TRLC-LINKS: REQ-SDS-096
func TestCoupleDisplay(t *testing.T) {
	sig := []uint8{100, 200, 100, 200} // mean 150
	// DC passes through unchanged (same backing array).
	if got := CoupleDisplay(sig, CplDC); &got[0] != &sig[0] {
		t.Fatal("DC coupling copied/altered the signal")
	}
	// AC removes DC: mean → 128, so 100→78, 200→178 (shift −22).
	ac := CoupleDisplay(sig, CplAC)
	if ac[0] != 78 || ac[1] != 178 {
		t.Fatalf("AC = %v, want [78 178 ...]", ac[:2])
	}
	// GND is a flat mid-scale ground trace.
	for _, v := range CoupleDisplay(sig, CplGND) {
		if v != 128 {
			t.Fatalf("GND produced %d, want flat 128", v)
		}
	}
}

// TRLC-LINKS: REQ-SDS-096
func TestRemoveDC(t *testing.T) {
	sig := make([]uint8, 100)
	for i := range sig {
		sig[i] = 200 // flat DC level well above centre
	}
	out := RemoveDC(sig)
	if out[50] != 128 {
		t.Fatalf("flat 200 recentred to %d, want 128", out[50])
	}
	if sig[0] != 200 {
		t.Fatal("RemoveDC mutated its input")
	}
	// Clamps at the rails rather than wrapping.
	for _, v := range RemoveDC([]uint8{0, 255}) {
		if v > 255 {
			t.Fatal("RemoveDC produced an out-of-range code")
		}
	}
}

// TRLC-LINKS: REQ-SDS-015
func TestPlanVdiv(t *testing.T) {
	if idx, ok := PlanVdiv(0.002); !ok || idx != 0 {
		t.Fatalf("PlanVdiv(2mV) = %d,%v", idx, ok)
	}
	if idx, ok := PlanVdiv(10.0); !ok || idx != 11 {
		t.Fatalf("PlanVdiv(10V) = %d,%v", idx, ok)
	}
	if _, ok := PlanVdiv(0.003); ok {
		t.Fatal("PlanVdiv(3mV) accepted")
	}
}

// TRLC-LINKS: REQ-SDS-015
func TestZoomAndAnalogVdiv(t *testing.T) {
	// 2 mV and 5 mV run the 10 mV analog range with display zoom.
	if AnalogVdiv(0) != 0.010 || Detents[0].Zoom != 5 {
		t.Fatalf("2mV: analog=%v zoom=%d", AnalogVdiv(0), Detents[0].Zoom)
	}
	if AnalogVdiv(1) != 0.010 || Detents[1].Zoom != 2 {
		t.Fatalf("5mV: analog=%v zoom=%d", AnalogVdiv(1), Detents[1].Zoom)
	}
	if AnalogVdiv(8) != 1.0 {
		t.Fatalf("1V: analog=%v", AnalogVdiv(8))
	}
}

// TRLC-LINKS: REQ-SDS-015, REQ-SDS-095
func TestCalibratedGainAndZero(t *testing.T) {
	tab := cal.Defaults()
	tab.Source = "file" // pretend a real file loaded
	tab.Rec[0][5].GainDAC = 42
	tab.Rec[1][8].GainDAC = 99
	tab.Rec[0][5].Zero = 10440
	tr := &fakeTr{}
	fe := New(tr, func(d time.Duration) { tr.sleeps = append(tr.sleeps, d) }, tab)

	// C1 → 100 mV (vd 5): gain bytes come from the cal table (CH2 stays at
	// its boot detent's table code).
	if err := fe.SetVdiv(0, 5); err != nil {
		t.Fatal(err)
	}
	g := tr.ops[1]
	if g.ch1 != 42 || g.ch2 != 99 {
		t.Fatalf("cal gains = ch1:%d ch2:%d, want 42/99", g.ch1, g.ch2)
	}
	// Offset zero follows the CURRENT detent's cal record.
	if got := fe.OffsetCode(0, 0); got != 10440 {
		t.Fatalf("calibrated zero = %d, want 10440", got)
	}
	// vd 5 (100 mV) is a sensitive ×1 tier: slope 50/0.1 = 500 codes/V, ±1.6 V
	// range. A 0.5 V offset (far past the old ~±0.08 V ceiling) round-trips.
	if v := fe.OffsetVolts(0, fe.OffsetCode(0, 0.5)); v < 0.499 || v > 0.501 {
		t.Fatalf("sensitive OffsetVolts round-trip = %v, want 0.5", v)
	}
}

// TestOffsetKPerDivision pins the per-tier slope: one value per tier, not
// per V/div — 4800 codes/V on the sensitive tier (bench 2026-10-05) and 100 on
// the attenuated tier.
// TRLC-LINKS: REQ-SDS-095
func TestOffsetKPerDivision(t *testing.T) {
	tr := &fakeTr{}
	fe := New(tr, func(time.Duration) {}, cal.Defaults())
	for _, c := range []struct {
		idx  int
		want float64
	}{{2, 4800}, {6, 4800}, {7, 100}, {8, 100}} {
		if err := fe.SetVdiv(0, c.idx); err != nil {
			t.Fatal(err)
		}
		if k := fe.OffsetK(0); k != c.want {
			t.Fatalf("detent %d OffsetK = %v, want %v", c.idx, k, c.want)
		}
	}
}

// TestOffsetTierRangeAndClamp checks per-tier authority (spec 06 §5.2.1):
// ±1.6 V on the sensitive ×1 tier, ±40 V on the attenuated ×25 tier.
// TRLC-LINKS: REQ-SDS-095
func TestOffsetTierRangeAndClamp(t *testing.T) {
	tr := &fakeTr{}
	fe := New(tr, func(time.Duration) {}, cal.Defaults()) // zeros 10223
	if err := fe.SetVdiv(0, 6); err != nil {              // 200 mV sensitive
		t.Fatal(err)
	}
	if c := fe.OffsetCode(0, 1.6); c != 10223-7680 { // −4800·1.6: sensitive-tier slope, positive offset lowers the code
		t.Fatalf("200mV +1.6V code = %d, want %d", c, 10223-7680)
	}
	if v := fe.OffsetVolts(0, fe.OffsetCode(0, 1.6)); v < 1.599 || v > 1.601 {
		t.Fatalf("200mV +1.6V readback = %v", v)
	}
	if c := fe.OffsetCode(0, 5.0); c != 10223-7680 { // clamps to ±1.6 V (×1 tier)
		t.Fatalf("200mV +5V clamps to %d, want %d", c, 10223-7680)
	}
	if err := fe.SetVdiv(0, 8); err != nil { // 1 V attenuated
		t.Fatal(err)
	}
	if c := fe.OffsetCode(0, 40); c != 10223-4000 { // 100·40=4000
		t.Fatalf("1V +40V code = %d, want %d", c, 10223-4000)
	}
	if c := fe.OffsetCode(0, 100); c != 10223-4000 { // clamps to ±40 V
		t.Fatalf("1V +100V clamps to %d, want %d", c, 10223-4000)
	}
}

// TRLC-LINKS: REQ-SDS-095
func TestOffsetCodeMapping(t *testing.T) {
	// Table-less fallback: boot detent 1 V/div (idx 8), zero 10223, slope 50.
	if got := OffsetCode(0, 0); got != 10223 {
		t.Fatalf("C1 zero = %d, want 10223 (boot default)", got)
	}
	if got := OffsetCode(1, 0); got != 10223 {
		t.Fatalf("C2 zero = %d, want 10223 (boot default)", got)
	}
	if got := OffsetCode(0, 1.0); got != 10223-100 { // +1 V → 100 codes below zero
		t.Fatalf("C1 +1V = %d, want %d", got, 10223-100)
	}
	if got := OffsetCode(0, 100); got != 10223-4000 { // clamp to ×25 tier (±40 V = 4000 codes)
		t.Fatalf("clamp low = %d, want %d", got, 10223-4000)
	}
	if got := OffsetCode(0, -100); got != 10223+4000 {
		t.Fatalf("clamp high = %d, want %d", got, 10223+4000)
	}
	if v := OffsetVolts(0, OffsetCode(0, 1.5)); v < 1.49 || v > 1.51 {
		t.Fatalf("round-trip 1.5V = %v", v)
	}
}

// TRLC-LINKS: REQ-SDS-095
func TestOffsetRequestDoesNotWindUpBeyondDACRange(t *testing.T) {
	f := New(&fakeTr{}, func(time.Duration) {}, nil)
	for _, sign := range []float64{-1, 1} {
		f.SetOffset(0, sign*1000)
		before := f.OffsetReqV(0)
		vd, _ := f.offsetZeroAndDetent(0)
		if before != sign*offsetTierRangeV(vd) {
			t.Fatalf("request outside range: %g", before)
		}
		old := f.OffsetCode(0, before)
		next := f.SetOffset(0, before-sign*0.2)
		if old == next {
			t.Fatal("reversing at the limit did not move the DAC")
		}
	}
}

// The zero trim moves the 0 V code by slope·trim on each tier, and the
// built-in default parses.
// TRLC-LINKS: REQ-SDS-015
func TestZeroTrimMovesZero(t *testing.T) {
	f := New(nil, func(time.Duration) {}, nil)
	_, z0 := f.offsetZeroAndDetent(0)
	f.SetZeroTrim(&ZeroTrim{VdivV: []float64{1, 0.01}, C1V: []float64{0.1, 0.002}, C2V: []float64{0, 0}})
	if _, z := f.offsetZeroAndDetent(0); z != z0+offsetCodesPerVolt*0.1 {
		t.Fatalf("1 V/div zero %v, want %v", z, z0+10)
	}
	if v := f.OffsetVolts(0, f.OffsetCode(0, 0)); math.Abs(v) > 1e-9 {
		t.Fatalf("0 V round trip %v", v)
	}
	f.mu.Lock()
	f.idx[0] = 2
	f.mu.Unlock()
	want := float64(f.tab.Rec[0][2].Zero) + offsetCodesPerVoltSensitive*0.002
	if _, z := f.offsetZeroAndDetent(0); z != want {
		t.Fatalf("10 mV/div zero %v, want %v", z, want)
	}
	if _, err := DefaultZeroTrim(); err != nil {
		t.Fatal(err)
	}
	if _, err := parseZeroTrim([]byte(`{"vdiv_v":[0.003],"c1_v":[0],"c2_v":[0]}`)); err == nil {
		t.Fatal("non-detent accepted")
	}
}

// The zoomed detents run the 10 mV analog record even when the cal file
// gives them their own gain codes.
// TRLC-LINKS: REQ-SDS-015
func TestZoomedDetentsUseAnalogRecord(t *testing.T) {
	tab := cal.Defaults()
	tab.Source = "file"
	tab.Rec[0][0].GainDAC, tab.Rec[0][1].GainDAC, tab.Rec[0][2].GainDAC = 219, 164, 146
	tab.Rec[0][0].Zero, tab.Rec[0][2].Zero = 10434, 10426
	f := New(nil, func(time.Duration) {}, tab)
	for _, idx := range []int{0, 1, 2} {
		if g := f.gainFor(0, idx); g != 146 {
			t.Fatalf("detent %d gain %d, want 146", idx, g)
		}
	}
	f.mu.Lock()
	f.idx[0] = 0
	f.mu.Unlock()
	if _, z := f.offsetZeroAndDetent(0); z != 10426 {
		t.Fatalf("2 mV zero %v, want the 10 mV record's 10426", z)
	}
}

// A V/div change re-stages the offset DAC on the new detent's zero even when
// no offset was ever set.
// TRLC-LINKS: REQ-SDS-015
func TestSetVdivStagesZeroWithoutOffset(t *testing.T) {
	tab := cal.Defaults()
	tab.Rec[0][2].Zero, tab.Rec[0][8].Zero = 10426, 10445
	fe := New(&fakeTr{}, func(time.Duration) {}, tab)
	var staged []uint16
	fe.OnOffset(func(ch int, code uint16) { staged = append(staged, code) })
	if err := fe.SetVdiv(0, 2); err != nil {
		t.Fatal(err)
	}
	if len(staged) != 1 || staged[0] != 10426 {
		t.Fatalf("staged %v, want [10426]", staged)
	}
}
