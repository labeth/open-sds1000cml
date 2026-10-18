// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-check of the IR multi decoder against sigrok's ir_irmp, the
// IRMP library (oracle_test.go): IRMP runs at 20 kHz, so the records'
// sample rates are its multiples.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func TestOracleIRMP(t *testing.T) {
	needSigrokPD(t, "ir_irmp")
	const sr = 200000
	w := irOracleLine(sr, func(l irLine) {
		gap := func() { l.space(100e-3) }
		irSamsung32(l, 0x0707, 0xFD02)
		gap()
		irNEC16(l, 0x12, 0x34)
		gap()
		irNEC42(l, 0x1ABC, 0x5D)
		gap()
		// (IRMP reads the NEC frames after a JVC repeat as Siemens: they
		// come first)
		irJVC(l, 0x3, 0x5A1, false)
		irJVC(l, 0x3, 0x5A1, true)
		gap()
		irKaseikyo(l, 0x2002, 0x8, 0x0, 0x123, 0x1)
		gap()
		irDenon(l, 0x0A, 0x2C4)
		gap()
		irMatsushita(l, 0x123, 0x456)
		gap()
		irSamsung37(l, 0x0707, 0x5, 0x9A)
		gap()
		irKaseikyo(l, 0x4004, 0x1, 0x2, 0x3FF, 0x0)
		gap()
		irSamsung32(l, 0xE0E0, 0x40BF)
		gap()
	})
	r := DecodeIRMP(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK || r.FrameBits != IRSamsung32 {
		t.Fatal(r.FrameBits, r.Error)
	}
	var repo []string
	name := ""
	for _, sp := range r.Spans {
		switch {
		case sp.Kind == "sof":
			name = sp.Text
			if name == "REP" {
				name = "JVC rep"
			}
		case sp.Kind == "data" && sp.Text[0] == 'A':
			repo = append(repo, fmt.Sprintf("%s %04x", name, sp.Val))
		case sp.Kind == "data" && sp.Text[0] == 'C':
			repo[len(repo)-1] += fmt.Sprintf(" %04x", sp.Val)
		}
	}
	anns := sigrokDecode(t, sr, []string{"IR"}, [][]byte{w.bits}, "ir_irmp:ir=IR", "ir_irmp")
	re := regexp.MustCompile(`Protocol: (\S+) \(\d+\), Address 0x([0-9a-f]+), Command: 0x([0-9a-f]+), Flags: (\S+)`)
	var orc []string
	for _, a := range sortAnns(anns) {
		f := re.FindStringSubmatch(a.Text)
		if f == nil {
			t.Fatalf("annotation %q", a.Text)
		}
		nm := f[1]
		if strings.Contains(f[4], "repeat") {
			nm += " rep"
		}
		orc = append(orc, fmt.Sprintf("%s %s %s", nm, f[2], f[3]))
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("IR multi differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
