// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"reflect"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestDecodeLINRoundTrip(t *testing.T) {
	for _, baud := range []float64{9600, 19200, 10417} {
		sr := 1e6
		w := newTimeline(sr)
		w.idle(200e-6)
		linFrame(w, baud, 13, 0x3c, []int{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, true)
		linFrame(w, baud, 14, 0x12, []int{0xA5, 0x5A}, false)
		linFrame(w, baud, 13, 0x20, nil, false)
		r := DecodeLIN(w.codes(), 1/sr, LINCfg{})
		if !r.OK {
			t.Fatalf("baud %v: %s", baud, r.Error)
		}
		want := []int{linPIDParity(0x3c), 1, 2, 3, 4, 5, 6, 7, 8, linChecksum(linPIDParity(0x3c), []int{1, 2, 3, 4, 5, 6, 7, 8}, false),
			linPIDParity(0x12), 0xA5, 0x5A, linChecksum(linPIDParity(0x12), []int{0xA5, 0x5A}, true), linPIDParity(0x20)}
		if !reflect.DeepEqual(r.Bytes, want) {
			t.Fatalf("baud %v: bytes %X want %X (%s)", baud, r.Bytes, want, r.Text)
		}
		if strings.Contains(r.Text, "!") || r.Baud != int(baud) {
			t.Fatalf("baud %v: %q at %d", baud, r.Text, r.Baud)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeLINFlagsErrors(t *testing.T) {
	sr := 1e6
	w := newTimeline(sr)
	w.idle(100e-6)
	linFrame(w, 19200, 13, 0x12, []int{0xA5}, false)
	bits := w.bits
	// corrupt the checksum's top data bit: the last character before the idle
	w2 := newTimeline(sr)
	w2.idle(100e-6)
	bit := 1 / 19200.0
	w2.add(0, 13*bit)
	w2.add(1, bit)
	tlUART(w2, 0x55, 19200, "none", 1)
	tlUART(w2, 0x12, 19200, "none", 1) // parity bits missing
	w2.add(1, 2*bit)
	tlUART(w2, 0xA5, 19200, "none", 1)
	tlUART(w2, 0x00, 19200, "none", 1) // a wrong checksum
	w2.idle(30 * bit)
	r := DecodeLIN(bitsToCodes(append(bits, w2.bits...)), 1/sr, LINCfg{})
	if !r.OK || !strings.Contains(r.Text, "!ID12") || !strings.Contains(r.Text, "!CK00") {
		t.Fatalf("got %q ok=%v", r.Text, r.OK)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeDMX512RoundTrip(t *testing.T) {
	sr := 10e6
	w := newTimeline(sr)
	w.idle(50e-6)
	slots := make([]int, 40)
	for i := range slots {
		slots[i] = (i * 37) & 0xff
	}
	dmxPacket(w, 100, 12, 0, slots)
	dmxPacket(w, 176, 8, 0xCC, slots[:5])
	r := DecodeDMX512(w.codes(), 1/sr, DMXCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	want := append(append(append([]int{0}, slots...), 0xCC), slots[:5]...)
	if !reflect.DeepEqual(r.Bytes, want) {
		t.Fatalf("bytes %X want %X", r.Bytes, want)
	}
	if !strings.HasPrefix(r.Text, "BRK MAB SC00 00 25") {
		t.Fatalf("text %q", r.Text)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeMIDIRoundTrip(t *testing.T) {
	sr := 1e6
	w := newTimeline(sr)
	w.idle(100e-6)
	for _, b := range []int{0x90, 0x3C, 0x64, 0x3E, 0x64, 0xF8, 0x80, 0x3C, 0x00, 0xC1, 0x05, 0xF0, 0x41, 0x10, 0xF7} {
		tlUART(w, b, 31250, "none", 1)
	}
	w.idle(1e-3)
	r := DecodeMIDI(w.codes(), 1/sr, MIDICfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	if r.Text != "ON1 3C 64 3E 64 CLK OFF1 3C 00 PC2 05 SYX 41 10 EOX" {
		t.Fatalf("text %q", r.Text)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeModbusRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		parity string
		stops  int
	}{{"even", 1}, {"none", 2}, {"odd", 1}} {
		sr := 2e6
		w := newTimeline(sr)
		w.idle(5e-3)
		modbusFrame(w, 19200, tc.parity, tc.stops, []int{0x01, 0x03, 0x00, 0x6B, 0x00, 0x03})
		modbusFrame(w, 19200, tc.parity, tc.stops, []int{0x01, 0x03, 0x06, 0x02, 0x2B, 0x00, 0x00, 0x00, 0x64})
		modbusFrame(w, 19200, tc.parity, tc.stops, []int{0x11, 0x83, 0x02})
		r := DecodeModbus(w.codes(), 1/sr, ModbusCfg{})
		if !r.OK || r.Parity != tc.parity || strings.Contains(strings.ReplaceAll(r.Text, "F83!", ""), "!") {
			t.Fatalf("%s: ok=%v parity=%s %q (%s)", tc.parity, r.OK, r.Parity, r.Text, r.Error)
		}
		if !strings.HasPrefix(r.Text, "A01 F03 00 6B 00 03 74 17") {
			t.Fatalf("%s: text %q", tc.parity, r.Text)
		}
	}
}
