// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of DALI, USB Power Delivery and S/PDIF against
// sigrok's dali, usb_power_delivery and spdif decoders (oracle_test.go).

import (
	"fmt"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestOracleDALI(t *testing.T) {
	const sr = 100000
	w := newTimeline(sr)
	w.idle(5e-3)
	daliFrame(w, 16, 0x0190, 4e-3)
	daliFrame(w, 8, 0x5A, 10e-3)
	daliFrame(w, 16, 0xFF05, 10e-3)
	daliFrame(w, 16, 0xA300, 10e-3)
	daliFrame(w, 8, 0x00, 10e-3)
	r := DecodeDALI(w.codes(), 1.0/sr, DALICfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" {
			repo = append(repo, fmt.Sprintf("%02X", sp.Val))
		}
	}
	anns := sigrokDecode(t, sr, []string{"DALI"}, [][]byte{w.bits}, "dali:dali=DALI", "dali=raw")
	var orc []string
	for _, a := range anns {
		f := strings.Fields(a.Text)
		if strings.HasPrefix(a.Text, "Start") {
			continue
		}
		orc = append(orc, f[len(f)-1])
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("DALI differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestOracleUSBPD(t *testing.T) {
	const sr = 20000000
	w := newTimeline(sr)
	w.add(0, 20e-6)
	pdMessage(w, pdSOP, 0x1161, []uint32{0x0801912C}, false)
	pdMessage(w, pdSOP, 0x0041, nil, false)
	pdMessage(w, pdSOP, 0x2042, []uint32{0x12345678, 0x0A0B0C0D}, false)
	pdMessage(w, pdSOPPrime, 0x1042, []uint32{0x12345678}, true)
	w.add(w.bits[len(w.bits)-1], 2e-3) // sigrok ends a message on a millisecond's idle
	r := DecodeUSBPD(w.codes(), 1.0/sr, USBPDCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	// per message: the header, each data object and the CRC, as sigrok
	// writes them, and the CRC verdict
	var repo []string
	var cur []int
	flush := func() {
		if len(cur) < 6 {
			cur = nil
			return
		}
		repo = append(repo, fmt.Sprintf("H:%04x", cur[0]|cur[1]<<8))
		le := func(i int) uint32 {
			return uint32(cur[i]) | uint32(cur[i+1])<<8 | uint32(cur[i+2])<<16 | uint32(cur[i+3])<<24
		}
		for k := 2; k+4 < len(cur); k += 4 {
			repo = append(repo, fmt.Sprintf("[%d]%08x", (k-2)/4, le(k)))
		}
		repo = append(repo, fmt.Sprintf("CRC:%08x", le(len(cur)-4)))
		cur = nil
	}
	bad := 0
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "sof":
			flush()
		case "data", "frame-error":
			cur = append(cur, sp.Val)
			if sp.Kind == "frame-error" {
				bad++
			}
		}
	}
	flush()
	pd := "usb_power_delivery:cc1=CC"
	var all []ann
	for _, c := range []string{"header", "data", "crc"} {
		all = append(all, sigrokDecode(t, sr, []string{"CC"}, [][]byte{w.bits}, pd, "usb_power_delivery="+c)...)
	}
	var orc []string
	for _, a := range sortAnns(all) {
		orc = append(orc, a.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("USB PD differs:\n repo   %v\n sigrok %v", repo, orc)
	}
	warns := 0
	for _, a := range sigrokDecode(t, sr, []string{"CC"}, [][]byte{w.bits}, pd, "usb_power_delivery=warnings") {
		if strings.HasPrefix(a.Text, "Bad CRC") {
			warns++
		}
	}
	if warns != 1 || bad != 4 {
		t.Fatalf("CRC verdicts: sigrok %d bad messages, repo %d flagged bytes (want 1 and 4)", warns, bad)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestOracleSPDIF(t *testing.T) {
	const sr = 100000000
	w := newTimeline(sr)
	w.add(0, 100e-9)
	var s [][2]int
	for i := 0; i < 40; i++ {
		s = append(s, [2]int{(i * 0x13579) & 0xffffff, (0xF0F0F0 - i*0x1111) & 0xffffff})
	}
	spdifStream(w, 48e3, 180, s)
	r := DecodeSPDIF(w.codes(), 1.0/sr, SPDIFCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "spdif:data=D", "spdif=samples")
	var orc []int
	for _, a := range anns {
		var v int
		if _, err := fmt.Sscanf(a.Text, "Audio 0x%x", &v); err != nil {
			t.Fatalf("annotation %q", a.Text)
		}
		orc = append(orc, v)
	}
	// sigrok reads from the first preamble after its clock recovery
	if len(orc) == 0 || len(orc) > len(r.Bytes) {
		t.Fatalf("sigrok %d samples, repo %d", len(orc), len(r.Bytes))
	}
	tail := r.Bytes[len(r.Bytes)-len(orc):]
	eqBytes(t, "S/PDIF samples", tail, orc)
}
