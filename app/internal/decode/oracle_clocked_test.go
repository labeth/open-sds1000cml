// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of PS/2, SWD, MDIO and Wiegand against sigrok's ps2,
// swd, mdio and wiegand decoders (oracle_test.go).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestOraclePS2(t *testing.T) {
	// sigrok 0.5 reads a frame at the clock fall after its eleventh, later
	// releases at the rise after it: a lone clock pulse with the data line
	// idle between frames serves both, and is no frame here.
	const sr = 1000000
	l := newTwoLine(sr)
	l.add(1, 1, 300e-6)
	vals := []int{0x1C, 0xF0, 0x1C, 0x5A, 0x00, 0xFF, 0x7E}
	for i, v := range vals {
		ps2Frame(l, v, false, i == 4, 80e-6, 200e-6)
		l.add(0, 1, 40e-6)
		l.add(1, 1, 300e-6)
	}
	r := DecodePS2(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "data":
			repo = append(repo, fmt.Sprintf("%02x", sp.Val))
		case "parity-error":
			repo = append(repo, fmt.Sprintf("%02x", sp.Val), "PE")
		}
	}
	pd := "ps2:clk=CLK:data=DATA"
	anns := sortAnns(append(sigrokDecode(t, sr, []string{"CLK", "DATA"}, [][]byte{l.a.bits, l.b.bits}, pd, "ps2=word"),
		sigrokDecode(t, sr, []string{"CLK", "DATA"}, [][]byte{l.a.bits, l.b.bits}, pd, "ps2=parity-err")...))
	var orc []string
	for _, a := range anns {
		if a.Text == "Parity error" {
			orc = append(orc, "PE")
			continue
		}
		orc = append(orc, strings.TrimPrefix(a.Text, "Data: "))
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("PS/2 differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestOracleSWD(t *testing.T) {
	const sr = 10000000
	s := &swdLine{t: newTwoLine(sr), per: 1e-6}
	s.host(0, 0)
	s.lineReset()
	s.transfer(false, true, 0, 1, 0x2BA01477, false, 2)
	s.transfer(false, false, 8, 1, 0x000000F0, false, 2)
	s.transfer(true, false, 0, 1, 0x23000052, false, 2)
	s.transfer(true, true, 0xC, 1, 0xDEADBEEF, false, 2)
	s.transfer(false, true, 0xC, 1, 0x12345678, true, 2)
	s.host(0, 0)
	r := DecodeSWD(s.t.a.codes(), s.t.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		txt := strings.TrimPrefix(sp.Text, "!")
		switch {
		case sp.Kind == "rw":
			repo = append(repo, "LINERESET")
		case txt == "OK" || txt == "WAIT" || txt == "FAULT":
			repo = append(repo, txt)
		case len(txt) == 8:
			repo = append(repo, "0x"+strings.ToLower(txt))
			if sp.Kind != "data" {
				repo = append(repo, "PE")
			}
		}
	}
	pd := "swd:swclk=CLK:swdio=DIO"
	var all []ann
	for _, c := range []string{"reset", "ack", "data", "parity"} {
		all = append(all, sigrokDecode(t, sr, []string{"CLK", "DIO"}, [][]byte{s.t.a.bits, s.t.b.bits}, pd, "swd="+c)...)
	}
	var orc []string
	for _, a := range sortAnns(all) {
		if len(a.Text) == 2 && strings.Trim(a.Text, "01") == "" { // a parity error: computed and read
			orc = append(orc, "PE")
			continue
		}
		orc = append(orc, a.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("SWD differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestOracleMDIO(t *testing.T) {
	const sr = 25000000
	l := newTwoLine(sr)
	l.add(1, 1, 2e-6)
	mdioFrame(l, 400e-9, 32, 1, 2, 0x01, 0x02, 0x796D, 0)
	mdioFrame(l, 400e-9, 32, 1, 1, 0x1F, 0x00, 0x1140, 0)
	mdioFrame(l, 400e-9, 32, 0, 0, 0x02, 0x01, 0x0007, 0)
	mdioFrame(l, 400e-9, 32, 0, 3, 0x02, 0x01, 0xBEEF, 0)
	mdioFrame(l, 400e-9, 40, 1, 2, 0x05, 0x02, 0x1234, 0)
	r := DecodeMDIO(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind != "data" {
			continue
		}
		if strings.Contains(sp.Text, ".") {
			repo = append(repo, fmt.Sprintf("%02d", sp.Val>>5&0x1f), fmt.Sprintf("%02d", sp.Val&0x1f))
		} else {
			repo = append(repo, fmt.Sprintf("%04X", sp.Val))
		}
	}
	anns := sigrokDecode(t, sr, []string{"MDC", "MDIO"}, [][]byte{l.a.bits, l.b.bits}, "mdio:mdc=MDC:mdio=MDIO", "mdio=frame")
	re := regexp.MustCompile(`^(PHYAD|REGAD|PRTAD|DEVAD|DATA): (\w+)$`)
	var orc []string
	for _, a := range anns {
		if m := re.FindStringSubmatch(a.Text); m != nil {
			orc = append(orc, m[2])
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("MDIO differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func TestOracleWiegand(t *testing.T) {
	const sr = 100000
	l := newTwoLine(sr)
	l.add(1, 1, 10e-3)
	frames := []string{wiegandBits(26, 12, 4660), wiegandBits(34, 1000, 65535), wiegandBits(26, 255, 1), "1010110011"}
	for _, f := range frames {
		wiegandFrame(l, f, 2e-3)
	}
	r := DecodeWiegand(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "rw" {
			repo = append(repo, sp.Text)
		}
		if sp.Kind == "data" && (strings.HasPrefix(sp.Text, "F") || strings.HasPrefix(sp.Text, "C")) {
			repo = append(repo, sp.Text)
		}
	}
	anns := sigrokDecode(t, sr, []string{"D0", "D1"}, [][]byte{l.a.bits, l.b.bits}, "wiegand:d0=D0:d1=D1:bitwidth_ms=4", "wiegand=state")
	var orc []string
	for _, a := range anns {
		f := strings.Fields(a.Text)
		if len(f) != 3 || f[1] != "bits" {
			continue
		}
		nb, _ := strconv.Atoi(f[0])
		orc = append(orc, fmt.Sprintf("%db", nb))
		if nb == 26 || nb == 34 {
			fc := 8
			if nb == 34 {
				fc = 16
			}
			v1, _ := strconv.ParseInt(f[2][1:1+fc], 2, 32)
			v2, _ := strconv.ParseInt(f[2][1+fc:1+fc+16], 2, 32)
			orc = append(orc, fmt.Sprintf("F%d", v1), fmt.Sprintf("C%d", v2))
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("Wiegand differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
