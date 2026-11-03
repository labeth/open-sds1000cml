// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRelayWordIsSpecs: spec 06 §3's reference words (DC, 20 MHz limit off,
// both channels attenuated: 0x70ad2d; AC 0x25, GND 0x27, the limit on 0x2c,
// the sensitive range 0x29 on CH1).
// TRLC-LINKS: REQ-SDS-290
func TestRelayWordIsSpecs(t *testing.T) {
	att := fe{det: [2]int{7, 7}}
	if w := att.relayWord(); w != 0x70ad2d {
		t.Fatalf("DC word %06x", w)
	}
	for _, c := range []struct {
		s    fe
		want uint32
	}{
		{fe{det: [2]int{7, 7}, cpl: [2]int{cplAC, cplDC}}, 0x25},
		{fe{det: [2]int{7, 7}, cpl: [2]int{cplGND, cplDC}}, 0x27},
		{fe{det: [2]int{7, 7}, bwl: [2]bool{true, false}}, 0x2c},
		{fe{det: [2]int{4, 7}}, 0x29},
	} {
		if b := c.s.relayWord() & 0xff; b != c.want {
			t.Errorf("%+v: CH1 byte %02x, want %02x", c.s, b, c.want)
		}
	}
	if w := (fe{det: [2]int{7, 7}, trgSrc: 1}).relayWord() >> 16; w != 0x74 {
		t.Errorf("trigger source C2: byte 2 %02x", w)
	}
}

// TestCodeLevelsFindsTheSquare: two clusters are a square with its
// amplitude; one cluster with noise is a level.
// TRLC-LINKS: REQ-SDS-290
func TestCodeLevelsFindsTheSquare(t *testing.T) {
	var sq, flat []int
	for i := range 64 {
		flat = append(flat, 120+i%3)
		if i%2 == 0 {
			sq = append(sq, 80+i%2)
		} else {
			sq = append(sq, 160)
		}
	}
	if l := codeLevels(sq); !l.Square || l.Amp < 79 || l.Amp > 81 {
		t.Fatalf("square: %+v", l)
	}
	if l := codeLevels(flat); l.Square || l.Mean < 120 || l.Mean > 122 {
		t.Fatalf("level: %+v", l)
	}
}

// ladder is a channel's detents as the SDS1102CML+'s front end makes them:
// the square's size follows V/div with the gain DAC, without it does not
// (and the offset's code step 50/(K·V/div), K 4392 per volt sensitive and
// 100 attenuated).
// TRLC-LINKS: REQ-SDS-290
func ladder(gain bool, square float64) FrontEndResult {
	var r FrontEndResult
	for _, d := range detents {
		k := 100.0
		if !d.atten {
			k = 4392
		}
		v := d.vdiv
		if !gain {
			v = 1
		}
		amp := square / d.vdiv / 10
		if !gain {
			amp = square / 2 // the same size at every V/div
		}
		r.Detents = append(r.Detents, DetentResult{
			Vdiv: d.vdiv, Atten: d.atten, Slope: 50 / (k * v),
			Square: Levels{Square: square > 0, Amp: amp, Lo: 128 - amp/2, Hi: 128 + amp/2},
		})
	}
	r.Coupling = map[string]Levels{
		"DC":  {Square: square > 0, Amp: square, Lo: 100, Hi: 100 + square, Mean: 100 + square/2},
		"AC":  {Square: square > 0, Amp: square, Lo: 100 - square/2, Hi: 100 + square/2, Mean: 100},
		"GND": {Square: square > 0, Amp: square, Lo: 100 - square/2, Hi: 100 + square/2, Mean: 100},
	}
	return r
}

// verdictIn is part's verdict in vs.
// TRLC-LINKS: REQ-SDS-290
func verdictIn(t *testing.T, vs []Verdict, part string) Verdict {
	t.Helper()
	for _, v := range vs {
		if v.Part == part {
			return v
		}
	}
	t.Fatalf("no %q in %v", part, vs)
	return Verdict{}
}

// TestBoardVerdicts: a fitted front end, trigger, SRAM and panel are
// present; a channel without its gain DAC, a GND relay that leaves the
// square, an SRAM that answers nothing and an open key chain are not.
// TRLC-LINKS: REQ-SDS-290, REQ-SDS-291
func TestBoardVerdicts(t *testing.T) {
	good := BoardTests{
		FrontEnd: [2]FrontEndResult{ladder(true, 60), ladder(true, 60)},
		Trigger:  TriggerResult{Comparator: [2]string{"A12", "B11"}},
		Edges:    [2]EdgeResult{{Captures: 80, Rise: [2]float64{0.4, 1.8}}, {Captures: 80, Rise: [2]float64{0.4, 1.8}}},
		SRAM:     SRAMResult{Checked: 524283},
		Panel:    PanelResult{Chain: strings.Repeat("10", 32), IRQ: true, IRQQuiet: true},
	}
	vs := boardVerdicts(good, nil)
	for _, part := range []string{
		"channel 1 gain DAC", "channel 2 range relay", "channel 1 1 kHz input", "channel 1 GND relay bit",
		"channel 2 AC coupling", "channel 1 bandwidth limit", "channel 1 trigger comparator", "channel 2 trigger comparator", "SRAM",
		"front panel key chain", "front panel key interrupt",
	} {
		if v := verdictIn(t, vs, part); v.Verdict != vPresent {
			t.Errorf("%s: %s %s", part, v.Verdict, v.Detail)
		}
	}
	bad := good
	bad.FrontEnd[1] = ladder(false, 60)
	bad.FrontEnd[1].Coupling["AC"] = bad.FrontEnd[1].Coupling["DC"]
	bad.SRAM = SRAMResult{Checked: 524283, PatternError: 524283, BadBits: 0xfff00001}
	bad.Panel = PanelResult{Chain: strings.Repeat("1", 64)}
	vs = boardVerdicts(bad, nil)
	for part, want := range map[string]string{
		"channel 2 gain DAC": vAbsent, "channel 2 AC coupling": vAbsent, "SRAM": vAbsent,
		"front panel key chain": vAbsent, "front panel key interrupt": vAbsent, "channel 1 gain DAC": vPresent,
	} {
		if v := verdictIn(t, vs, part); v.Verdict != want {
			t.Errorf("%s: %s (%s), want %s", part, v.Verdict, v.Detail, want)
		}
	}
	noSignal := good
	noSignal.FrontEnd[0] = ladder(true, 0)
	if v := verdictIn(t, boardVerdicts(noSignal, nil), "channel 1 1 kHz input"); v.Verdict != vAbsent || !strings.Contains(v.Detail, "1 kHz output") {
		t.Errorf("no square: %+v", v)
	}
}

// TestARMInventory reads a modelled /sys and /proc, and the verdict names
// what is missing and extra.
// TRLC-LINKS: REQ-SDS-291
func TestARMInventory(t *testing.T) {
	root := t.TempDir()
	put := func(p, v string) {
		f := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("sys/class/net/eth0/address", "x")
	put("sys/class/net/lo/address", "x")
	put("sys/bus/mdio_bus/devices/0:00/phy_id", "0x0007c0f1\n")
	put("sys/bus/usb/devices/1-1/idVendor", "0781")
	put("sys/bus/usb/devices/1-1/idProduct", "5567")
	put("sys/bus/usb/devices/1-1/1-1:1.0/bInterfaceClass", "08")
	put("sys/bus/usb/devices/usb1/idVendor", "1d6b")
	put("sys/bus/usb/devices/usb1/idProduct", "0002")
	put("sys/bus/usb/devices/usb1/product", "MUSB HDRC host driver")
	put("sys/bus/i2c/devices/1-0050/name", "24c256")
	put("proc/mtd", "dev:    size   erasesize  name\nmtd0: 00020000 00020000 \"SPL\"\n")
	put("proc/bus/input/devices", "I: Bus=0019\nN: Name=\"gpio-keys\"\n")
	inv := armInventory(root)
	for _, want := range []string{
		"net eth0", "phy 0:00 0x0007c0f1", "usb 1d6b:0002 MUSB HDRC host driver", "i2c 1-0050 24c256 unbound",
		`mtd mtd0: 00020000 00020000 "SPL"`, `input "gpio-keys"`,
	} {
		if !slices.Contains(inv, want) {
			t.Errorf("no %q in %q", want, inv)
		}
	}
	if slices.ContainsFunc(inv, func(s string) bool { return strings.Contains(s, "0781") || s == "net lo" }) {
		t.Errorf("the stick or lo listed: %q", inv)
	}
	if v := armVerdict(inv, inv); v.Verdict != vPresent {
		t.Errorf("same: %+v", v)
	}
	v := armVerdict(slices.DeleteFunc(slices.Clone(inv), func(s string) bool { return s == "net eth0" }), inv)
	if v.Verdict != vOther || !strings.Contains(v.Detail, "missing: net eth0") {
		t.Errorf("missing eth0: %+v", v)
	}
}

// TestBannerDrawsTheWord: the DONE screen's word is white on its colour.
// TRLC-LINKS: REQ-SDS-292
func TestBannerDrawsTheWord(t *testing.T) {
	b := newBanner(800, 480, doneGreen, "DONE", []string{"switch the scope off"})
	white, green := 0, 0
	for _, p := range b.px {
		switch p {
		case 0xffff:
			white++
		case doneGreen:
			green++
		}
	}
	if white < 4*12*12*10 || green < 800*480/2 || white+green != len(b.px) {
		t.Fatalf("white %d green %d of %d", white, green, len(b.px))
	}
	if _, ok := glyphs5x7['D']; !ok {
		t.Fatal("no D")
	}
}

// TestBoardReferenceParses: the embedded SDS1102CML+ values parse.
// TRLC-LINKS: REQ-SDS-290
func TestBoardReferenceParses(t *testing.T) {
	if r := loadBoardReference(); r.TriggerBall == "" || len(r.ARM) == 0 || r.Chain == "" {
		t.Fatalf("the SDS1102CML+ reference is incomplete: %+v", r)
	}
}

// TestRiseSamples counts the samples within a capture's edge.
// TRLC-LINKS: REQ-SDS-290
func TestRiseSamples(t *testing.T) {
	c := make([]float64, capWords)
	for i := range c {
		switch {
		case i < capTrigger:
			c[i] = 50
		case i < capTrigger+2:
			c[i] = 100 + 40*float64(i-capTrigger)
		default:
			c[i] = 200
		}
	}
	if n := riseSamples(c); n != 2 {
		t.Fatalf("an edge of two samples: %v", n)
	}
	for i := range c {
		c[i] = 128
	}
	if n := riseSamples(c); n != -1 {
		t.Fatalf("no edge: %v", n)
	}
}

// TestBoardTestsOfTheSDS1102CMLPlus: the takeover stage's tests on an
// SDS1102CML+ with the 1 kHz square on C1 (the bench's C2 probe carried
// none) judge every part present, nothing absent but C2's square.
// TRLC-LINKS: REQ-SDS-290, REQ-SDS-291
func TestBoardTestsOfTheSDS1102CMLPlus(t *testing.T) {
	var bt BoardTests
	var inv []string
	for f, v := range map[string]any{"testdata/board-tests-sds1102cml.json": &bt, "testdata/arm-inventory-sds1102cml.json": &inv} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range boardVerdicts(bt, inv) {
		switch {
		case v.Part == "channel 2 1 kHz input", strings.HasSuffix(v.Part, " bandwidth"):
		case strings.HasPrefix(v.Part, "channel 2 ") && v.Verdict == vOther && strings.Contains(v.Detail, "not tested"):
		case strings.HasPrefix(v.Part, "ARM gpio113") || strings.HasPrefix(v.Part, "ARM eHRPWM"):
		case v.Verdict != vPresent:
			t.Errorf("%s: %s %s", v.Part, v.Verdict, v.Detail)
		}
	}
}
