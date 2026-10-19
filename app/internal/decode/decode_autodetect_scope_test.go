// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"testing"
)

// scopeCapture is a record the scope's BUS search read from a bench source
// of a protocol beyond the first ten (testdata/scope_*.json.gz: the decimated
// record at the timebase it looks at, or the full-rate one) and the protocol
// and line the detection must name there.
type scopeCapture struct {
	file  string
	proto string
	chA   int
}

// scopeCaptures are the records of the bench buses AUTO in BUS named wrong
// or not at all before: read by the converters through 10x probes (slow
// edges, the decimation filter's ringing, a sparse line's levels), unlike the
// generated captures of protoDetectCases.
var scopeCaptures = []scopeCapture{
	{"scope_lin_search_5ms.json.gz", "lin", 0},
	{"scope_modbus_search_5ms.json.gz", "modbus", 0},
	{"scope_j1850_samples_500us.json.gz", "j1850", 0},
	{"scope_usbpd_raw_200us.json.gz", "usbpd", 0},
	{"scope_onewire_samples_500us.json.gz", "onewire", 0},
	{"scope_onewire_uartlike_500us.json.gz", "onewire", 0},
	{"scope_dht_search_5ms.json.gz", "dht", 0},
	{"scope_cec_search_20ms.json.gz", "cec", 0},
	{"scope_irnec_search_20ms.json.gz", "irnec", 0},
	{"scope_wiegand_search_20ms.json.gz", "wiegand", 0},
	{"scope_wiegand_samples_5ms.json.gz", "wiegand", 0},
	{"scope_ps2_samples_500us.json.gz", "ps2", 0},
	{"scope_ps2_oneframe_500us.json.gz", "ps2", 0},
	{"scope_swd_raw_50us.json.gz", "swd", 0},
	{"scope_swd_samples_500us.json.gz", "swd", 0},
	// a 10 MHz SPI read (clock C2): its data line, taken for MDC, clocked MDIO frames out of the clock
	{"scope_spi10_raw_50us.json.gz", "spi", 1},
	// an Oregon sensor's bursts, and RFID readers' commands on the field's envelope
	{"scope_ook_search_20ms.json.gz", "ook", 0},
	{"scope_em4305_search_5ms.json.gz", "em4305", 0},
	{"scope_t55xx_search_5ms.json.gz", "t55xx", 0},
	// records cut a frame at either end: a PDI instruction's first
	// characters, a DSI frame's last bits
	{"scope_pdi_raw_500us.json.gz", "pdi", 0},
	{"scope_dsi_search_20ms.json.gz", "dsi", 0},
	{"scope_cjtag_samples_200us.json.gz", "cjtag", 0},
	{"scope_caliper_search_5ms.json.gz", "caliper", 0},
	// a SWIM transaction from its address on (the window opens inside it)
	{"scope_swim_raw_50us.json.gz", "swim", 0},
}

// TestScopeRFIDStandsDecimated: a reader's commands found on the envelope
// in a search's decimated record span the samples a checked find needs
// there (six, panel decimatedStands): their shortest feature is a gap of
// field clocks, not the field's cycle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestScopeRFIDStandsDecimated(t *testing.T) {
	for _, f := range []string{"scope_em4305_search_5ms.json.gz", "scope_t55xx_search_5ms.json.gz"} {
		c1, c2, sampleS := loadGzJSON(t, "testdata/"+f)
		r, st := AutodetectFast(c1, c2, sampleS, "hex")
		r.SPB *= st / sampleS // in the record's samples, as the panel's Detect gives it
		if fs := FeatureSamples(r); fs < 6 {
			t.Errorf("%s: %s feature %.1f samples", f, r.Proto, fs)
		}
	}
}

// TestAutodetectNamesScopeCaptures: each record the scope read of a bench
// bus is named as that bus, on its line, with a frame whose checks pass (the
// detection the search runs, AutodetectFast).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectNamesScopeCaptures(t *testing.T) {
	for _, sc := range scopeCaptures {
		c1, c2, sampleS := loadGzJSON(t, "testdata/"+sc.file)
		r, _ := AutodetectFast(c1, c2, sampleS, "hex")
		if !r.OK || r.Proto != sc.proto || r.Roles.ChA != sc.chA {
			t.Errorf("%s: named %q on C%d (%s) %.60s, want %q on C%d", sc.file, r.Proto, r.Roles.ChA+1, r.Src, r.Text, sc.proto, sc.chA+1)
			continue
		}
		t.Logf("%-38s %-8s %.60s", sc.file, r.Proto, r.Text)
	}
}

// The 104 MHz SPI flash's decimated record at 200us/div (0.26 us samples, a
// burst of bytes every 7 us) holds one START, address and bytes among 200
// bare START STOP pairs - data edges while the averaged clock rests high: no
// I2C bus, where AUTO in BUS named one now and then.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectNoI2COnADecimatedFlash(t *testing.T) {
	c1, c2, sampleS := loadGzJSON(t, "testdata/scope_flash104_samples_200us.json.gz")
	if r, _ := AutodetectFast(c1, c2, sampleS, "hex"); r.Proto == "i2c" {
		t.Errorf("named %q (%s) %.60s", r.Proto, r.Src, r.Text)
	}
}

// A 1-Wire bus at the full rate beside a quiet probe whose converter spikes
// a sample now and then: the quiet line stays quiet (quietFlat) however the
// record is cut and strided, and no SPI bus is read off the pair (the scope
// named one at 200us/div, its "data" the picked spikes).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectQuietLineBesideOneWire(t *testing.T) {
	w := newTimeline(500e6)
	w.idle(100e-6)
	for k := 0; k < 3; k++ {
		owReset(w, false, true)
		owByte(w, 0xcc, false)
		owByte(w, 0x44, false)
		w.idle(600e-6)
	}
	c1 := w.codes()
	c2 := make([]uint8, len(c1))
	for i := range c2 {
		c2[i] = 103 + uint8(i%3)
		switch i % 7919 {
		case 0:
			c2[i] = 41
		case 3001:
			c2[i] = 166
		}
	}
	if q := quietFlat(c2); q[0] != q[len(q)/2] || q[7919] != q[0] {
		t.Fatal("a quiet line with spikes kept its spikes")
	}
	if r, _ := AutodetectFast(c1, c2, 2e-9, "hex"); r.Proto != "onewire" {
		t.Errorf("named %q (%s) %.40s, want the 1-Wire bus", r.Proto, r.Src, r.Text)
	}
}

// A line resting high and pulsed low seldom - a Wiegand line, 0.3 % of its
// record low, those samples spread over the codes of the pulses' levels and
// edges - finds both rails (rails takes the tails by their sum), as a line
// pulsed often does.
// TRLC-LINKS: REQ-SDS-018
func TestRailsOfASparselyPulsedLine(t *testing.T) {
	codes := make([]uint8, 10000)
	for i := range codes {
		codes[i] = 200
	}
	for p := 0; p < 6; p++ { // six pulses, five samples each over five codes
		for k := 0; k < 5; k++ {
			codes[1000+1500*p+k] = uint8(48 + k)
		}
	}
	lo, hi, why := rails(codes)
	if why != "" || lo > 60 || hi < 190 {
		t.Fatalf("rails %.0f..%.0f (%q), want about 50..200", lo, hi, why)
	}
	if s := sliceChannel(codes, 0, false); !s.ok || len(s.edges) != 12 {
		t.Fatalf("sliced ok=%v with %d edges, want the six pulses' 12", s.ok, len(s.edges))
	}
}

// TestScopePartialFramesNotStolen: records that hold a bus's bits but none
// of its frames whole are named as no other bus: a WS281x frame's middle at
// 10us/div (its two pulse widths are no PWM output) and a cJTAG escape cut
// from its scan (its TMSC edges while TCKC is high are no I2C STARTs).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestScopePartialFramesNotStolen(t *testing.T) {
	for _, c := range []struct{ file, not string }{
		{"scope_ws281x_samples_10us.json.gz", "pwm"},
		{"scope_cjtag_raw_10us_escape.json.gz", "i2c"},
	} {
		c1, c2, sampleS := loadGzJSON(t, "testdata/"+c.file)
		r, _ := AutodetectFast(c1, c2, sampleS, "hex")
		if r.OK && r.Proto == c.not {
			t.Errorf("%s: named %s %.60s", c.file, r.Proto, r.Text)
		}
	}
}
