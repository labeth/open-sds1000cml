// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"reflect"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestDecodeOneWireRoundTrip(t *testing.T) {
	sr := 4e6
	w := newTimeline(sr)
	w.idle(100e-6)
	rom := owROM(0x28, 0xFF, 0x4C, 0x60, 0x91, 0x16, 0x05)
	owReset(w, false, true)
	owByte(w, 0x55, false)
	for _, v := range rom {
		owByte(w, v, false)
	}
	owByte(w, 0x44, false)
	w.idle(200e-6)
	owReset(w, false, true)
	owByte(w, 0xF0, false)
	owSearchROM(w, rom)
	w.idle(200e-6)
	owReset(w, false, true)
	owByte(w, 0x3C, false) // overdrive skip
	owReset(w, true, true)
	owByte(w, 0xBE, true)
	owByte(w, 0x50, true)
	w.idle(200e-6)
	owReset(w, false, false)
	w.idle(100e-6)
	r := DecodeOneWire(w.codes(), 1/sr, OneWireCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	want := "RST PRES MATCH 28 FF 4C 60 91 16 05 " + hex2(rom[7]) + " 44 RST PRES SEARCH 28 FF 4C 60 91 16 05 " + hex2(rom[7]) +
		" RST PRES ODSKIP RST PRES BE 50 RST NOPRES"
	if r.Text != want {
		t.Fatalf("text\n %q\nwant\n %q", r.Text, want)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestDecodeWS281xRoundTrip(t *testing.T) {
	for _, rate := range []float64{800e3, 400e3} {
		sr := 50e6
		w := newTimeline(sr)
		w.add(0, 60e-6)
		b := []int{0xFF, 0x00, 0x80, 0x12, 0x34, 0x56}
		wsFrame(w, rate, b)
		wsFrame(w, rate, []int{0x01, 0x02, 0x03})
		r := DecodeWS281x(w.codes(), 1/sr, WS281xCfg{})
		if !r.OK || !reflect.DeepEqual(r.Bytes, append(b, 1, 2, 3)) || r.Baud != int(rate) {
			t.Fatalf("rate %v: ok=%v %X baud %d (%s) %q", rate, r.OK, r.Bytes, r.Baud, r.Error, r.Text)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestDecodeDHTRoundTrip(t *testing.T) {
	sr := 2e6
	w := newTimeline(sr)
	w.idle(1e-3)
	dhtReading(w, 1000, [4]int{0x02, 0x8C, 0x01, 0x5F}, -1)
	w.idle(2e-3)
	dhtReading(w, 18000, [4]int{0x2D, 0x00, 0x17, 0x00}, 0x11)
	r := DecodeDHT(w.codes(), 1/sr, DHTCfg{})
	if !r.OK || r.Text != "START RESP 02 8C 01 5F EE START RESP 2D 00 17 00 !11" {
		t.Fatalf("ok=%v %q", r.OK, r.Text)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestDecodeCECRoundTrip(t *testing.T) {
	sr := 200e3
	w := newTimeline(sr)
	w.idle(5e-3)
	cecMessage(w, []int{0x40, 0x04}, []int{0, 0})                   // image view on, acked
	cecMessage(w, []int{0x4F, 0x82, 0x10, 0x00}, []int{1, 1, 1, 1}) // broadcast active source
	cecMessage(w, []int{0x01}, []int{1})                            // a poll nobody answered
	r := DecodeCEC(w.codes(), 1/sr, CECCfg{})
	want := "START 4>0 A 04 EOM A START 4>F A 82 A 10 A 00 EOM A START 0>1 EOM N"
	if !r.OK || r.Text != want {
		t.Fatalf("ok=%v\n %q\nwant\n %q", r.OK, r.Text, want)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestDecodeJ1850RoundTrip(t *testing.T) {
	sr := 1e6
	w := newTimeline(sr)
	w.add(0, 500e-6)
	j1850Frame(w, []int{0x68, 0x6A, 0xF1, 0x01, 0x00}, false)
	j1850Frame(w, []int{0x48, 0x6B, 0x10, 0x41, 0x00, 0xBE, 0x1F, 0xB8, 0x10}, false)
	j1850Frame(w, []int{0x68, 0x6A, 0xF1}, true)
	r := DecodeJ1850(w.codes(), 1/sr, J1850Cfg{})
	if !r.OK || !strings.HasPrefix(r.Text, "SOF 68 6A F1 01 00 ") || strings.Count(r.Text, "!") != 1 {
		t.Fatalf("ok=%v %q", r.OK, r.Text)
	}
	if got := j1850CRC([]int{0x68, 0x6A, 0xF1, 0x01, 0x00}); got != 0x17 {
		t.Fatalf("CRC of the OBD request 68 6A F1 01 00 is %02X, want 17", got)
	}
}
