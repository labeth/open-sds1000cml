// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the infrared remote decoders against sigrok's
// ir_nec, ir_sirc, ir_rc5 and ir_rc6 (oracle_test.go), on a receiver's
// active-low output, the polarity sigrok's decoders default to.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// irOracleLine builds an active-low IR record at sr.
// TRLC-LINKS: REQ-SDS-018
func irOracleLine(sr float64, f func(l irLine)) *timeline {
	w := newTimeline(sr)
	l := irLine{w: w}
	l.space(20e-3)
	f(l)
	return w
}

// TRLC-LINKS: REQ-SDS-018
func TestOracleIRNEC(t *testing.T) {
	const sr = 200000
	w := irOracleLine(sr, func(l irLine) {
		necFrame(l, 0x04, -1, 0x08)
		necRepeat(l)
		necRepeat(l)
		necFrame(l, 0x00, -1, 0x45)
		necFrame(l, 0xA5, -1, 0xFF)
	})
	r := DecodeIRNEC(w.codes(), 1.0/sr, IRCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch {
		case sp.Text == "REP":
			repo = append(repo, "R")
		case sp.Kind == "data":
			repo = append(repo, fmt.Sprintf("%02X", sp.Val))
		}
	}
	pd := "ir_nec:ir=IR"
	var all []ann
	for _, c := range []string{"addr", "addr-inv", "cmd", "cmd-inv", "repeat-code"} {
		all = append(all, sigrokDecode(t, sr, []string{"IR"}, [][]byte{w.bits}, pd, "ir_nec="+c)...)
	}
	re := regexp.MustCompile(`0x([0-9A-F]{2})`)
	var orc []string
	for _, a := range sortAnns(all) {
		if strings.HasPrefix(a.Text, "Repeat") {
			orc = append(orc, "R")
			continue
		}
		orc = append(orc, re.FindStringSubmatch(a.Text)[1])
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("NEC differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestOracleIRSIRC(t *testing.T) {
	needSigrokPD(t, "ir_sirc")
	const sr = 200000
	w := irOracleLine(sr, func(l irLine) {
		sircFrame(l, 12, 0x15, 0x01, 0)
		sircFrame(l, 15, 0x33, 0xA4, 0)
		sircFrame(l, 20, 0x7F, 0x1A, 0xC3)
		sircFrame(l, 12, 0x00, 0x1F, 0)
	})
	r := DecodeIRSIRC(w.codes(), 1.0/sr, IRCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" {
			repo = append(repo, sp.Text[:1]+fmt.Sprintf("%02X", sp.Val))
		}
	}
	pd := "ir_sirc:ir=IR"
	var all []ann
	for _, c := range []string{"command", "address", "extended"} {
		all = append(all, sigrokDecode(t, sr, []string{"IR"}, [][]byte{w.bits}, pd, "ir_sirc="+c)...)
	}
	re := regexp.MustCompile(`^(\w)\w*: 0x([0-9A-F]{2})`)
	var orc []string
	for _, a := range sortAnns(all) {
		m := re.FindStringSubmatch(a.Text)
		if m == nil {
			t.Fatalf("annotation %q", a.Text)
		}
		orc = append(orc, m[1]+m[2])
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("SIRC differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestOracleIRRC5(t *testing.T) {
	const sr = 200000
	for _, ext := range []bool{false, true} {
		w := irOracleLine(sr, func(l irLine) {
			rc5Frame(l, 0, 0x00, 0x0C)
			rc5Frame(l, 1, 0x1F, 0x3F)
			rc5Frame(l, 0, 0x05, 0x21)
			if ext {
				rc5Frame(l, 1, 0x05, 0x50)
			}
		})
		r := DecodeIRRC5(w.codes(), 1.0/sr, IRCfg{})
		if !r.OK {
			t.Fatal(r.Error)
		}
		var repo []string
		for k, sp := range r.Spans {
			if sp.Kind == "data" {
				v := sp.Val
				if !ext && k > 0 && strings.HasPrefix(sp.Text, "C") {
					v &= 0x3f
				}
				repo = append(repo, sp.Text[:1]+strconv.Itoa(v))
			}
		}
		pd := "ir_rc5:ir=IR"
		if ext {
			pd += ":protocol=extended"
		}
		all := append(sigrokDecode(t, sr, []string{"IR"}, [][]byte{w.bits}, pd, "ir_rc5=address"),
			sigrokDecode(t, sr, []string{"IR"}, [][]byte{w.bits}, pd, "ir_rc5=command")...)
		re := regexp.MustCompile(`^(\w)\w*: (\d+)`)
		var orc []string
		for _, a := range sortAnns(all) {
			if strings.HasPrefix(a.Text, "CMD[6]") { // RC-5X's second start bit
				continue
			}
			m := re.FindStringSubmatch(a.Text)
			if m == nil {
				t.Fatalf("annotation %q", a.Text)
			}
			orc = append(orc, m[1]+m[2])
		}
		if fmt.Sprint(repo) != fmt.Sprint(orc) {
			t.Fatalf("RC-5 (extended %v) differs:\n repo   %v\n sigrok %v", ext, repo, orc)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestOracleIRRC6(t *testing.T) {
	const sr = 200000
	w := irOracleLine(sr, func(l irLine) {
		rc6Frame(l, 0, 0, []int{0x00, 0x0C})
		rc6Frame(l, 0, 1, []int{0xFF, 0x01})
		rc6Frame(l, 0, 0, []int{0x5A, 0xC3})
		rc6Frame(l, 6, 1, []int{0x12, 0x34, 0x56})
	})
	r := DecodeIRRC6(w.codes(), 1.0/sr, IRCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	// per frame: the address byte, then the rest as one hex number
	var repo []string
	frame := []int(nil)
	flush := func() {
		if len(frame) > 0 {
			rest := ""
			for _, v := range frame[1:] {
				rest += fmt.Sprintf("%02X", v)
			}
			repo = append(repo, fmt.Sprintf("A%02X", frame[0]), "D"+strings.TrimLeft(rest, "0"))
		}
		frame = nil
	}
	for _, sp := range r.Spans {
		if sp.Kind == "sof" {
			flush()
		}
		if sp.Kind == "data" {
			frame = append(frame, sp.Val)
		}
	}
	flush()
	pd := "ir_rc6:ir=IR"
	all := append(sigrokDecode(t, sr, []string{"IR"}, [][]byte{w.bits}, pd, "ir_rc6=address"),
		sigrokDecode(t, sr, []string{"IR"}, [][]byte{w.bits}, pd, "ir_rc6=command")...)
	var orc []string
	for _, a := range sortAnns(all) {
		f := strings.SplitN(a.Text, ": ", 2)
		orc = append(orc, f[0][:1]+strings.TrimLeft(f[1], "0"))
	}
	for i := range orc {
		if strings.HasPrefix(orc[i], "A") && len(orc[i]) < 3 {
			orc[i] = fmt.Sprintf("A%02s", orc[i][1:])
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("RC-6 differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
