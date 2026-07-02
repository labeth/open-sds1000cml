package analog

import (
	"testing"
	"time"

	"open-sds/app/internal/cal"
)

type op struct {
	kind string // "relay" | "gain"
	word uint32
	ch2  uint8
	ch1  uint8
}

type fakeTr struct {
	ops    []op
	sleeps []time.Duration
}

func (f *fakeTr) WriteRelay(word uint32) error {
	f.ops = append(f.ops, op{kind: "relay", word: word})
	return nil
}

func (f *fakeTr) WriteGain(ch2, ch1 uint8) error {
	f.ops = append(f.ops, op{kind: "gain", ch2: ch2, ch1: ch1})
	return nil
}

func newFE(tr *fakeTr) *FrontEnd {
	return New(tr, func(d time.Duration) { tr.sleeps = append(tr.sleeps, d) }, nil)
}

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
	if v := fe.OffsetVolts(0, 10440-262); v < 0.99 || v > 1.01 {
		t.Fatalf("calibrated OffsetVolts = %v, want 1.0", v)
	}
}

func TestOffsetCodeMapping(t *testing.T) {
	// 0 V → per-channel zero; positive volts → lower code (inverting).
	if got := OffsetCode(0, 0); got != 10223 {
		t.Fatalf("C1 zero = %d, want 10223 (boot default)", got)
	}
	if got := OffsetCode(1, 0); got != 10223 {
		t.Fatalf("C2 zero = %d, want 10223 (boot default)", got)
	}
	if got := OffsetCode(0, 1.0); got != 10223-262 {
		t.Fatalf("C1 +1V = %d, want %d", got, 10223-262)
	}
	// Clamp to the DAC linear region.
	if got := OffsetCode(0, 100); got != OffsetCodeMin {
		t.Fatalf("clamp low = %d", got)
	}
	if got := OffsetCode(0, -100); got != OffsetCodeMax {
		t.Fatalf("clamp high = %d", got)
	}
	// Round-trip.
	if v := OffsetVolts(0, OffsetCode(0, 1.5)); v < 1.49 || v > 1.51 {
		t.Fatalf("round-trip 1.5V = %v", v)
	}
}
