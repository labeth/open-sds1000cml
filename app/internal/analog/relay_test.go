package analog

import (
	"errors"
	"testing"
	"time"
)

// gainOps counts the gain transfers in a fake transport's op log.
func countOps(tr *fakeTr, kind string) int {
	n := 0
	for _, o := range tr.ops {
		if o.kind == kind {
			n++
		}
	}
	return n
}

// lastGain returns the most recent gain op.
func lastGain(tr *fakeTr) (op, bool) {
	for i := len(tr.ops) - 1; i >= 0; i-- {
		if tr.ops[i].kind == "gain" {
			return tr.ops[i], true
		}
	}
	return op{}, false
}

// TestCapturedVendorAlphabet reproduces, from the named controls alone, every
// distinct relay word the reglog corpus actually captured from the vendor
// (fpga-specs/takeover/13-analog-frontend.md §2.3 — eight words, listed there
// with the SCPI command that provoked each). This is the encoding's ground
// truth: if a bit position is wrong, one of these rows stops matching.
func TestCapturedVendorAlphabet(t *testing.T) {
	const (
		att  = BootDetent // 1 V/div, attenuated tier (bit2 set)
		sens = 5          // 100 mV/div, sensitive tier (bit2 clear)
	)
	cases := []struct {
		name  string
		setup func(fe *FrontEnd) error
		want  uint32
	}{
		{"2d ad 70 — both DC/attenuated, BWL off, trig DC (boot, C1:CPL D1M)",
			func(fe *FrontEnd) error { return fe.SetVdiv(0, att) }, 0x70ad2d},
		{"29 a9 70 — both sensitive (fine detents)",
			func(fe *FrontEnd) error {
				if err := fe.SetVdiv(0, sens); err != nil {
					return err
				}
				return fe.SetVdiv(1, sens)
			}, 0x70a929},
		{"29 ad 70 — CH1 sensitive, CH2 attenuated (mixed tiers)",
			func(fe *FrontEnd) error { return fe.SetVdiv(0, sens) }, 0x70ad29},
		{"25 ad 70 — CH1 AC (C1:CPL A1M)",
			func(fe *FrontEnd) error { return fe.SetCouplingHW(0, CplAC) }, 0x70ad25},
		{"27 ad 70 — CH1 GND (C1:CPL GND)",
			func(fe *FrontEnd) error { return fe.SetCouplingHW(0, CplGND) }, 0x70ad27},
		{"2d ad 50 — trigger AC (C1:TRCP AC)",
			func(fe *FrontEnd) error { return fe.SetTrigCoupling(TrigCplAC) }, 0x50ad2d},
		{"2d ad f0 — trigger HFREJ (C1:TRCP HFREJ)",
			func(fe *FrontEnd) error { return fe.SetTrigCoupling(TrigCplHFREJ) }, 0xf0ad2d},
		{"2d ad 40 — trigger LFREJ (C1:TRCP LFREJ)",
			func(fe *FrontEnd) error { return fe.SetTrigCoupling(TrigCplLFREJ) }, 0x40ad2d},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := &fakeTr{}
			fe := newFE(tr)
			if err := c.setup(fe); err != nil {
				t.Fatal(err)
			}
			got, ok := lastRelay(tr)
			if !ok {
				t.Fatal("no relay word emitted")
			}
			if got != c.want {
				t.Fatalf("relay word = %#06x, want %#06x", got, c.want)
			}
		})
	}
}

// TestBWLBitSenseAndPosition pins bit0 and its INVERTED sense: the engaged
// 20 MHz limit CLEARS the bit (fpga-specs/40-… §6.5; spec 06 §3). The reference
// CH1 bytes are DC+BWL-off 0x2d and DC+BWL-on 0x2c.
func TestBWLBitSenseAndPosition(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	if fe.BWL(0) || fe.BWL(1) {
		t.Fatal("bandwidth limit engaged at seed; the shipped default is OFF")
	}
	if err := fe.SetBWL(0, true); err != nil { // CH1 limit engaged
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70ad2c {
		t.Fatalf("CH1 BWL on = %#06x, want 0x70ad2c (bit0 cleared on CH1 only)", w)
	}
	if err := fe.SetBWL(1, true); err != nil { // both engaged
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70ac2c {
		t.Fatalf("both BWL on = %#06x, want 0x70ac2c", w)
	}
	if !fe.BWL(0) || !fe.BWL(1) {
		t.Fatal("BWL state not recorded")
	}
	// Reversible: back to the reference word, bit for bit.
	if err := fe.SetBWL(0, false); err != nil {
		t.Fatal(err)
	}
	if err := fe.SetBWL(1, false); err != nil {
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70ad2d {
		t.Fatalf("BWL restored = %#06x, want 0x70ad2d", w)
	}
	if err := fe.SetBWL(2, true); err == nil {
		t.Fatal("bad channel accepted")
	}
}

// TestCouplingHWBits pins bit1/bit3 on BOTH channel bytes. CH2's byte carries
// the same coupling bits plus the bit7 channel-address bit.
func TestCouplingHWBits(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	if fe.CouplingHW(0) != CplDC || fe.CouplingHW(1) != CplDC {
		t.Fatal("hw coupling did not seed to DC")
	}
	// CH2 AC: byte1 = 0xa5 (bit7 address + bit5 enable + bit2 attenuated).
	if err := fe.SetCouplingHW(1, CplAC); err != nil {
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70a52d {
		t.Fatalf("CH2 AC = %#06x, want 0x70a52d", w)
	}
	// CH2 GND: byte1 = 0xa7.
	if err := fe.SetCouplingHW(1, CplGND); err != nil {
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70a72d {
		t.Fatalf("CH2 GND = %#06x, want 0x70a72d", w)
	}
	// GND is bit1 WITH bit3 CLEAR — the read-modify-write artefact that made the
	// GND relay look unpopulated left bit3 set (fpga-specs/40-… §8.2).
	if b := uint8((0x70a72d >> 8) & 0xff); b&0x08 != 0 {
		t.Fatalf("CH2 GND byte %#02x still has the DC bit set", b)
	}
	if err := fe.SetCouplingHW(1, CplDC); err != nil {
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70ad2d {
		t.Fatalf("CH2 restored = %#06x, want 0x70ad2d", w)
	}
	if err := fe.SetCouplingHW(0, 9); err == nil {
		t.Fatal("bad hw coupling mode accepted")
	}
	if err := fe.SetCouplingHW(7, CplAC); err == nil {
		t.Fatal("bad hw coupling channel accepted")
	}
}

// TestHWCouplingIndependentOfSoftwareCoupling keeps the two controls separate:
// the software transform never moves a relay bit, and the relay never moves the
// display transform. TestCouplingIsSoftwareOnly covers the first direction on
// the shipped path; this covers the second.
func TestHWCouplingIndependentOfSoftwareCoupling(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	if err := fe.SetCouplingHW(0, CplGND); err != nil {
		t.Fatal(err)
	}
	if fe.Coupling(0) != CplDC {
		t.Fatalf("hw coupling changed the software transform to %d", fe.Coupling(0))
	}
	if err := fe.SetCoupling(0, CplAC); err != nil {
		t.Fatal(err)
	}
	if fe.CouplingHW(0) != CplGND {
		t.Fatalf("software coupling changed the relay state to %d", fe.CouplingHW(0))
	}
	if w, _ := lastRelay(tr); w != 0x70ad27 {
		t.Fatalf("relay word after a software-coupling change = %#06x, want the unchanged 0x70ad27", w)
	}
}

// TestTrigCouplingNibbleTable pins each mode to its captured nibble and keeps
// the trigger-source bits [3:2] where relayWord leaves them (0 = C1) — that
// field is HW-refuted as a source selector and has no named setter on purpose.
func TestTrigCouplingNibbleTable(t *testing.T) {
	want := map[int]uint8{TrigCplDC: 0x70, TrigCplAC: 0x50, TrigCplHFREJ: 0xf0, TrigCplLFREJ: 0x40}
	for mode, b2 := range want {
		tr := &fakeTr{}
		fe := newFE(tr)
		if err := fe.SetTrigCoupling(mode); err != nil {
			t.Fatal(err)
		}
		w, _ := lastRelay(tr)
		got := uint8(w >> 16)
		if got != b2 {
			t.Fatalf("trig coupling %d → byte2 %#02x, want %#02x", mode, got, b2)
		}
		if fe.TrigCoupling() != mode {
			t.Fatalf("trig coupling state = %d, want %d", fe.TrigCoupling(), mode)
		}
		if got&0x0c != 0 {
			t.Fatalf("byte2 %#02x moved the trigger-source bits [3:2]", got)
		}
	}
	if err := (&FrontEnd{}).SetTrigCoupling(-1); err == nil {
		t.Fatal("negative trigger coupling accepted")
	}
	if err := (&FrontEnd{}).SetTrigCoupling(4); err == nil {
		t.Fatal("out-of-range trigger coupling accepted")
	}
}

// TestNamedControlsKeepAbsoluteWordDiscipline: every named actuator emits ONE
// full relay word, waits the 400 µs settle, then re-emits BOTH gain bytes from
// the seeded shadows — including the channel it did not touch. That is the
// invariant that stops a relay change collapsing the other channel's gain.
func TestNamedControlsKeepAbsoluteWordDiscipline(t *testing.T) {
	acts := map[string]func(fe *FrontEnd) error{
		"SetBWL":          func(fe *FrontEnd) error { return fe.SetBWL(0, true) },
		"SetCouplingHW":   func(fe *FrontEnd) error { return fe.SetCouplingHW(0, CplAC) },
		"SetTrigCoupling": func(fe *FrontEnd) error { return fe.SetTrigCoupling(TrigCplLFREJ) },
	}
	for name, act := range acts {
		t.Run(name, func(t *testing.T) {
			tr := &fakeTr{}
			fe := newFE(tr)
			// Put the two channels on DIFFERENT detents first, so a gain re-emit
			// that dropped a channel would be visible.
			if err := fe.SetVdiv(0, 5); err != nil { // CH1 100 mV → gain 12
				t.Fatal(err)
			}
			tr.ops, tr.sleeps = nil, nil
			if err := act(fe); err != nil {
				t.Fatal(err)
			}
			if len(tr.ops) != 2 || tr.ops[0].kind != "relay" || tr.ops[1].kind != "gain" {
				t.Fatalf("ops = %+v, want exactly one relay then one gain flush", tr.ops)
			}
			if len(tr.sleeps) != 1 || tr.sleeps[0] != 400*time.Microsecond {
				t.Fatalf("settle = %v, want one 400µs sleep between relay and gain", tr.sleeps)
			}
			// CH2 stayed at the boot detent (57); CH1 at 100 mV (12). Both bytes
			// go out, CH2 first, from the shadows — never from a readback.
			if g := tr.ops[1]; g.ch2 != 57 || g.ch1 != 12 {
				t.Fatalf("gain flush = ch2:%d ch1:%d, want 57/12 (both seeded bytes)", g.ch2, g.ch1)
			}
		})
	}
}

// TestShippedVdivPathByteIdentical is AF-0.4's regression predicate: with every
// new control at its default, the V/div path emits exactly what it emitted
// before these controls existed.
func TestShippedVdivPathByteIdentical(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	for _, idx := range []int{BootDetent, 5, 0, 11} {
		if err := fe.SetVdiv(0, idx); err != nil {
			t.Fatal(err)
		}
	}
	// Composed purely from the detent ladder: CH1 10 V/div (attenuated) 0x2d,
	// CH2 boot 1 V/div 0xad, byte2 DC 0x70.
	if w, _ := lastRelay(tr); w != 0x70ad2d {
		t.Fatalf("V/div path word = %#06x, want the historical 0x70ad2d", w)
	}
	if n := countOps(tr, "relay"); n != 4 {
		t.Fatalf("%d relay emits for 4 V/div changes, want 4", n)
	}
	if countOps(tr, "gain") != 4 {
		t.Fatal("a V/div change did not re-emit the gain bytes")
	}
}

func TestRawHatchesRefusedUnlessArmed(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	if fe.RawDebug() {
		t.Fatalf("raw hatches armed by default (env %s leaked into the test?)", RawDebugEnv)
	}
	if err := fe.SetRelayRaw(0x70ad2d); !errors.Is(err, ErrRawDisabled) {
		t.Fatalf("SetRelayRaw disarmed = %v, want ErrRawDisabled", err)
	}
	if err := fe.SetGainRaw(1, 2); !errors.Is(err, ErrRawDisabled) {
		t.Fatalf("SetGainRaw disarmed = %v, want ErrRawDisabled", err)
	}
	if len(tr.ops) != 0 {
		t.Fatalf("a refused raw write still emitted %d SPI ops", len(tr.ops))
	}
	if _, emitted := fe.Snapshot(); emitted {
		t.Fatal("a refused raw write set the emitted flag")
	}
}

func TestRawDebugArmedFromEnv(t *testing.T) {
	t.Setenv(RawDebugEnv, "1")
	if fe := newFE(&fakeTr{}); !fe.RawDebug() {
		t.Fatalf("%s=1 did not arm the raw hatches", RawDebugEnv)
	}
	t.Setenv(RawDebugEnv, "0")
	if fe := newFE(&fakeTr{}); fe.RawDebug() {
		t.Fatalf("%s=0 armed the raw hatches", RawDebugEnv)
	}
}

// TestSetRelayRawUsesTheSameDiscipline: a raw word rides the identical emit
// path — one absolute word, the settle, then BOTH gain bytes from the seeded
// shadows. AF-0.4's hazard is precisely that a raw emit must not skip them.
func TestSetRelayRawUsesTheSameDiscipline(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	fe.SetRawDebug(true)
	if err := fe.SetVdiv(0, 5); err != nil { // CH1 100 mV (gain 12), CH2 boot (57)
		t.Fatal(err)
	}
	tr.ops, tr.sleeps = nil, nil

	// AF-2.5's walk: channel-byte bit4 and bit6, and byte-2 bits [1:0] — the four
	// bits no captured vendor word ever sets and no named control can express.
	const probe = 0x73ed69 // CH1 0x69 (bit6), CH2 0xed (bit6), byte2 0x73 (bits[1:0])
	if err := fe.SetRelayRaw(probe); err != nil {
		t.Fatal(err)
	}
	if len(tr.ops) != 2 || tr.ops[0].kind != "relay" || tr.ops[1].kind != "gain" {
		t.Fatalf("ops = %+v, want relay then gain", tr.ops)
	}
	if tr.ops[0].word != probe {
		t.Fatalf("raw word = %#06x, want %#06x verbatim", tr.ops[0].word, probe)
	}
	if len(tr.sleeps) != 1 || tr.sleeps[0] != 400*time.Microsecond {
		t.Fatalf("settle = %v, want one 400µs sleep", tr.sleeps)
	}
	if g, _ := lastGain(tr); g.ch2 != 57 || g.ch1 != 12 {
		t.Fatalf("raw emit gain flush = ch2:%d ch1:%d, want the seeded 57/12", g.ch2, g.ch1)
	}
	// The shadows are untouched, so a named control restores the composed word.
	if err := fe.SetVdiv(0, 5); err != nil {
		t.Fatal(err)
	}
	if w, _ := lastRelay(tr); w != 0x70ad29 {
		t.Fatalf("word after restore = %#06x, want the composed 0x70ad29", w)
	}
}

func TestCheckRelayRawGuardsAddressingOnly(t *testing.T) {
	bad := map[string]uint32{
		"wider than 24 bits": 0x0170ad2d,
		"CH1 bit5 clear":     0x70ad0d,
		"CH2 bit5 clear":     0x708d2d,
		"CH1 carries bit7":   0x70adad,
		"CH2 missing bit7":   0x702d2d,
	}
	for name, w := range bad {
		if err := CheckRelayRaw(w); err == nil {
			t.Fatalf("%s (%#08x) accepted", name, w)
		}
	}
	good := map[string]uint32{
		"the reference word":      0x70ad2d,
		"BWL engaged both":        0x70ac2c,
		"CH1 GND":                 0x70ad27,
		"unassigned bits 4/6 set": 0x73fd7d,
		"trigger nibble LFREJ":    0x40ad2d,
	}
	for name, w := range good {
		if err := CheckRelayRaw(w); err != nil {
			t.Fatalf("%s (%#06x) rejected: %v", name, w, err)
		}
	}
	// The guard must run BEFORE anything reaches the wire.
	tr := &fakeTr{}
	fe := newFE(tr)
	fe.SetRawDebug(true)
	if err := fe.SetRelayRaw(0x702d2d); err == nil {
		t.Fatal("SetRelayRaw accepted a word that collapses CH2 onto CH1's latch")
	}
	if len(tr.ops) != 0 {
		t.Fatalf("a rejected raw word still emitted %d SPI ops", len(tr.ops))
	}
}

// TestSetGainRawEmitsBothBytesCH2First: two 1-byte transfers, CH2 first, no
// relay traffic and no settle.
func TestSetGainRawEmitsBothBytesCH2First(t *testing.T) {
	tr := &fakeTr{}
	fe := newFE(tr)
	fe.SetRawDebug(true)
	if err := fe.SetGainRaw(0xe6, 0xe5); err != nil {
		t.Fatal(err)
	}
	if len(tr.ops) != 1 || tr.ops[0].kind != "gain" {
		t.Fatalf("ops = %+v, want exactly one gain flush and no relay write", tr.ops)
	}
	if tr.ops[0].ch2 != 0xe6 || tr.ops[0].ch1 != 0xe5 {
		t.Fatalf("gain = ch2:%#02x ch1:%#02x, want e6/e5", tr.ops[0].ch2, tr.ops[0].ch1)
	}
	if len(tr.sleeps) != 0 {
		t.Fatalf("gain-only emit slept %v; the settle belongs to relay changes", tr.sleeps)
	}
	if _, emitted := fe.Snapshot(); !emitted {
		t.Fatal("a raw gain emit left the emitted flag clear")
	}
	// The shadows are untouched: the next V/div restores the ladder codes.
	if err := fe.SetVdiv(0, 5); err != nil {
		t.Fatal(err)
	}
	if g, _ := lastGain(tr); g.ch2 != 57 || g.ch1 != 12 {
		t.Fatalf("gain after restore = ch2:%d ch1:%d, want the ladder 57/12", g.ch2, g.ch1)
	}
}

// TestChannelByteBitMap is the direct unit check on the encoder, one bit at a
// time, against fpga-specs/40-level-dac-and-analog-control.md §6.5.
func TestChannelByteBitMap(t *testing.T) {
	const (
		att  = BootDetent // attenuated tier → bit2 set
		sens = 5          // sensitive tier → bit2 clear
	)
	cases := []struct {
		name string
		idx  int
		ch2  bool
		bwl  bool
		cpl  int
		want uint8
	}{
		{"CH1 DC attenuated BWL-off", att, false, false, CplDC, 0x2d},
		{"CH1 DC sensitive BWL-off", sens, false, false, CplDC, 0x29},
		{"CH1 DC attenuated BWL-on", att, false, true, CplDC, 0x2c},
		{"CH1 AC attenuated", att, false, false, CplAC, 0x25},
		{"CH1 GND attenuated", att, false, false, CplGND, 0x27},
		{"CH2 DC attenuated", att, true, false, CplDC, 0xad},
		{"CH2 DC sensitive", sens, true, false, CplDC, 0xa9},
		{"CH2 AC attenuated BWL-on", att, true, true, CplAC, 0xa4},
	}
	for _, c := range cases {
		if got := channelByte(c.idx, c.ch2, c.bwl, c.cpl); got != c.want {
			t.Errorf("%s: channelByte = %#02x, want %#02x", c.name, got, c.want)
		}
	}
	// Bits 4 and 6 are never set by the encoder — they are unassigned in the bit
	// map and appear in no captured vendor word.
	for _, cpl := range []int{CplDC, CplAC, CplGND} {
		for _, bwl := range []bool{false, true} {
			for _, ch2 := range []bool{false, true} {
				for idx := range Detents {
					if b := channelByte(idx, ch2, bwl, cpl); b&0x50 != 0 {
						t.Fatalf("channelByte(%d,%v,%v,%d) = %#02x sets an unassigned bit", idx, ch2, bwl, cpl, b)
					}
					if b := channelByte(idx, ch2, bwl, cpl); b&0x20 == 0 {
						t.Fatalf("channelByte(%d,%v,%v,%d) = %#02x cleared the constant enable bit", idx, ch2, bwl, cpl, b)
					}
				}
			}
		}
	}
}
