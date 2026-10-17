// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"reflect"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestDecodeDALIRoundTrip(t *testing.T) {
	sr := 200e3
	w := newTimeline(sr)
	w.idle(5e-3)
	daliFrame(w, 16, 0x0190, 3e-3)
	daliFrame(w, 8, 0x5A, 10e-3)
	daliFrame(w, 16, 0xFF05, 10e-3)
	daliFrame(w, 24, 0x123456, 10e-3)
	r := DecodeDALI(w.codes(), 1/sr, DALICfg{})
	if !r.OK || r.Text != "START A01 D90 START R5A START AFF D05 START A12 34 56" || r.Inverted {
		t.Fatalf("ok=%v inverted=%v %q", r.OK, r.Inverted, r.Text)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeUSBPDRoundTrip(t *testing.T) {
	sr := 50e6
	w := newTimeline(sr)
	w.add(0, 20e-6)
	// a Source_Capabilities with one fixed 5 V 3 A PDO, a GoodCRC, a bad CRC, a hard reset
	pdMessage(w, pdSOP, 0x1161, []uint32{0x0801912C}, false)
	pdMessage(w, pdSOP, 0x0041, nil, false)
	pdMessage(w, pdSOPPrime, 0x1042, []uint32{0x12345678}, true)
	pdMessage(w, pdHardReset, 0, nil, false)
	r := DecodeUSBPD(w.codes(), 1/sr, USBPDCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	if !strings.HasPrefix(r.Text, "SOP SrcCap 11 2C 91 01 08 ") || !strings.Contains(r.Text, "SOP GoodCRC 00 ") ||
		!strings.Contains(r.Text, "SOP' Request 10 78 56 34 12 !") || !strings.HasSuffix(r.Text, "HRST") {
		t.Fatalf("text %q", r.Text)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestDecodeSPDIFRoundTrip(t *testing.T) {
	for _, fs := range []float64{48e3, 96e3} {
		sr := 500e6
		w := newTimeline(sr)
		w.add(0, 50e-9)
		var s [][2]int
		var want []int
		for i := 0; i < 6; i++ {
			a, b := (i*0x1357)&0xffffff, 0xFFFFF0-i*0x10
			s = append(s, [2]int{a, b})
			want = append(want, a, b)
		}
		spdifStream(w, fs, 190, s)
		r := DecodeSPDIF(w.codes(), 1/sr, SPDIFCfg{})
		// the last subframe's last cell has no edge closing it in the record
		if !r.OK || !reflect.DeepEqual(r.Bytes, want[:len(want)-1]) || !strings.Contains(r.Text, " B 0026AE") {
			t.Fatalf("fs %v: ok=%v %X\n want %X\n %q", fs, r.OK, r.Bytes, want[:len(want)-1], r.Text)
		}
		if r.Baud != int(64*fs) {
			t.Fatalf("fs %v: cell rate %d", fs, r.Baud)
		}
	}
}
