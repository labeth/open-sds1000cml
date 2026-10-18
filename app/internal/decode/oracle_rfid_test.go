// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the 125 kHz RFID decoders against sigrok's
// em4100, em4305 and t55xx decoders (oracle_test.go). sigrok reads a
// reader's commands off its carrier.

import (
	"fmt"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func TestOracleEM4100(t *testing.T) {
	const sr = 500000
	const hp = 256e-6
	w := newTimeline(sr)
	w.add(1, 3*hp)
	tags := [][5]int{{0x01, 0x23, 0x45, 0x67, 0x89}, {0x0A, 0xFF, 0x00, 0x5C, 0xE1}, {0x7F, 0x80, 0x12, 0x34, 0x56}}
	for i, tg := range tags {
		em4100Frame(w, tg, hp, false)
		em4100Frame(w, tg, hp, i == 1)
	}
	w.add(0, 2*hp)
	r := DecodeEM4100(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo, repoTags []string
	tag, ok := "", true
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "sof":
			tag, ok = "", true
		case "data", "parity-error":
			repo = append(repo, fmt.Sprintf("%X", sp.Val>>4), fmt.Sprintf("%X", sp.Val&15))
			tag += hex2(sp.Val)
			ok = ok && sp.Kind == "data"
			if len(tag) == 10 && ok {
				repoTags = append(repoTags, tag)
			}
		}
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "em4100:data=D", "em4100=fields")
	var orc []string
	for _, a := range sortAnns(anns) {
		if strings.HasPrefix(a.Text, "Version/customer: ") || strings.HasPrefix(a.Text, "Data: ") {
			orc = append(orc, a.Text[strings.Index(a.Text, ": ")+2:])
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("EM4100 rows differ:\n repo   %v\n sigrok %v", repo, orc)
	}
	var orcTags []string
	for _, a := range sortAnns(sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "em4100:data=D", "em4100=tag")) {
		orcTags = append(orcTags, strings.TrimPrefix(a.Text, "Tag: "))
	}
	if fmt.Sprint(repoTags) != fmt.Sprint(orcTags) {
		t.Fatalf("EM4100 tags differ:\n repo   %v\n sigrok %v", repoTags, orcTags)
	}
}

// rfidFieldTexts is a writes decode's units as sigrok's fields read.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func rfidFieldTexts(r Result) []string {
	var out []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" || sp.Kind == "parity-error" {
			out = append(out, strings.TrimPrefix(sp.Text, "!"))
			if sp.Kind == "parity-error" {
				out = append(out, "BAD")
			}
		}
	}
	return out
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func TestOracleEM4305(t *testing.T) {
	const sr = 1000000
	w := newTimeline(sr)
	f := rfidField{w, true}
	f.on(500 * rfidFC)
	em4305Command(f, em4305Bits(1, -1, [4]int{0x12, 0x34, 0x56, 0x78}, false))
	em4305Command(f, em4305Bits(2, 5, [4]int{0xDE, 0xAD, 0xBE, 0xEF}, false))
	em4305Command(f, em4305Bits(2, 9, [4]int{0x00, 0xFF, 0x81, 0x7E}, true))
	r := DecodeEM4305(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	repo := rfidFieldTexts(r)
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "em4305:data=D", "em4305=fields")
	var orc []string
	for _, a := range sortAnns(anns) {
		switch {
		case a.Text == "Login" || a.Text == "Write word":
			orc = append(orc, map[string]string{"Login": "LOGIN", "Write word": "WRITE"}[a.Text])
		case strings.HasPrefix(a.Text, "Addr: "):
			orc = append(orc, "A"+strings.TrimPrefix(a.Text, "Addr: "))
		case strings.HasPrefix(a.Text, "Data: "):
			v := 0
			fmt.Sscanf(strings.TrimPrefix(a.Text, "Data: "), "%X", &v)
			orc = append(orc, hex2(v))
		case a.Text == "Row parity failed":
			orc = append(orc, "BAD")
		}
	}
	// sigrok marks a row's parity after the row's value
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("EM4305 differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func TestOracleT55xx(t *testing.T) {
	const sr = 1000000
	w := newTimeline(sr)
	f := rfidField{w, true}
	f.on(500 * rfidFC)
	t55xxCommand(f, t55xxBits(2, 0x51243648, 0, 0x00148040, 0))
	t55xxCommand(f, t55xxBits(2, -1, 1, 0xDEADBEEF, 5))
	t55xxCommand(f, []int{0, 0})
	r := DecodeT55xx(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK || r.FrameBits != 70 {
		t.Fatal(r.FrameBits, r.Error)
	}
	var repo []string
	word := ""
	for _, sp := range r.Spans {
		if sp.Kind != "data" {
			continue
		}
		switch {
		case strings.HasPrefix(sp.Text, "OP"):
			repo = append(repo, "OP"+sp.Text[2:])
		case strings.HasPrefix(sp.Text, "LK"), strings.HasPrefix(sp.Text, "BL"):
			repo = append(repo, sp.Text)
		default:
			word += strings.TrimPrefix(sp.Text, "P")
			if len(word) == 8 {
				repo = append(repo, strings.TrimLeft(word, "0"))
				word = ""
			}
		}
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "t55xx:data=D", "t55xx=fields")
	var orc []string
	for _, a := range sortAnns(anns) {
		k := strings.Index(a.Text, ": ")
		if k < 0 {
			continue
		}
		key, val := a.Text[:k], a.Text[k+2:]
		switch key {
		case "Opcode":
			orc = append(orc, "OP"+val)
		case "Password", "Data":
			orc = append(orc, strings.TrimLeft(val, "0"))
		case "Lock":
			orc = append(orc, "LK"+val)
		case "Addr":
			orc = append(orc, "BL"+val)
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("T55xx differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
