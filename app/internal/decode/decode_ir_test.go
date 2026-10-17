// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"reflect"
	"testing"
)

// irLines are the line forms each IR test runs on: a receiver's
// active-low output, an emitter's active-high drive, and that drive
// modulated at 38 kHz.
var irLines = []struct {
	name       string
	activeHigh bool
	carrierHz  float64
}{{"receiver", false, 0}, {"emitter", true, 0}, {"carrier", true, 38e3}}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeIRNECRoundTrip(t *testing.T) {
	for _, lf := range irLines {
		sr := 1e6
		w := newTimeline(sr)
		l := irLine{w, lf.activeHigh, lf.carrierHz}
		l.space(20e-3)
		necFrame(l, 0x04, -1, 0x08)
		necRepeat(l)
		necFrame(l, 0x34, 0x12, 0x5A) // extended address
		r := DecodeIRNEC(w.codes(), 1/sr, IRCfg{})
		if !r.OK || r.Text != "LDR A04 FB C08 F7 REP LDR A34 12 C5A A5" || r.Inverted != lf.activeHigh {
			t.Fatalf("%s: ok=%v inverted=%v %q (%s)", lf.name, r.OK, r.Inverted, r.Text, r.Error)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeIRSIRCRoundTrip(t *testing.T) {
	for _, lf := range irLines {
		sr := 1e6
		w := newTimeline(sr)
		l := irLine{w, lf.activeHigh, lf.carrierHz}
		l.space(20e-3)
		sircFrame(l, 12, 0x15, 0x01, 0)
		sircFrame(l, 15, 0x33, 0xA4, 0)
		sircFrame(l, 20, 0x7F, 0x1A, 0xC3)
		r := DecodeIRSIRC(w.codes(), 1/sr, IRCfg{})
		if !r.OK || r.Text != "SIRC C15 A01 SIRC C33 AA4 SIRC C7F A1A EC3" {
			t.Fatalf("%s: ok=%v %q (%s)", lf.name, r.OK, r.Text, r.Error)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeIRRC5RoundTrip(t *testing.T) {
	for _, lf := range irLines {
		sr := 1e6
		w := newTimeline(sr)
		l := irLine{w, lf.activeHigh, lf.carrierHz}
		l.space(20e-3)
		rc5Frame(l, 0, 0x00, 0x0C)
		rc5Frame(l, 1, 0x1F, 0x3F)
		rc5Frame(l, 0, 0x05, 0x50) // RC-5X command
		r := DecodeIRRC5(w.codes(), 1/sr, IRCfg{})
		if !r.OK || !reflect.DeepEqual(r.Bytes, []int{0, 0x0C, 0x1F, 0x3F, 0x05, 0x50}) || r.Text != "RC5 T0 A00 C0C RC5 T1 A1F C3F RC5 T0 A05 C50" {
			t.Fatalf("%s: ok=%v %q %X (%s)", lf.name, r.OK, r.Text, r.Bytes, r.Error)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeIRRC6RoundTrip(t *testing.T) {
	for _, lf := range irLines {
		sr := 1e6
		w := newTimeline(sr)
		l := irLine{w, lf.activeHigh, lf.carrierHz}
		l.space(20e-3)
		rc6Frame(l, 0, 0, []int{0x00, 0x0C})
		rc6Frame(l, 0, 1, []int{0xFF, 0x01})
		rc6Frame(l, 6, 1, []int{0x80, 0x0F, 0x04, 0x31})
		r := DecodeIRRC6(w.codes(), 1/sr, IRCfg{})
		if !r.OK || r.Text != "RC6 M0 T0 A00 C0C RC6 M0 T1 AFF C01 RC6 M6 T1 80 0F 04 31" {
			t.Fatalf("%s: ok=%v %q (%s)", lf.name, r.OK, r.Text, r.Error)
		}
	}
}
