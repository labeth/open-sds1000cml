// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the two-wire buses beyond the first thirty against
// sigrok's sdcard_sd, avr_pdi, cjtag, maple_bus and caliper decoders
// (oracle_test.go).

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestOracleSDCard(t *testing.T) {
	const sr = 4000000
	const per = 4e-6
	l := newTwoLine(sr)
	var bits []int
	add := func(b []int) { bits = append(bits, b...) }
	add(sdIdle(10))
	add(sdToken(true, 0, 0, false))
	add(sdIdle(10))
	add(sdToken(true, 8, 0x1AA, false))
	add(sdIdle(4))
	add(sdToken(false, 8, 0x1AA, false))
	add(sdIdle(10))
	add(sdToken(true, 55, 0, false))
	add(sdIdle(4))
	add(sdToken(false, 55, 0x120, false))
	add(sdIdle(10))
	add(sdToken(true, 41, 0x40300000, false))
	add(sdIdle(4))
	add(sdToken(false, 63, 0xC0FF8000, false))
	add(sdIdle(10))
	add(sdToken(true, 2, 0, false))
	add(sdIdle(4))
	add(sdR2([15]int{0x03, 0x53, 0x44, 0x53, 0x44, 0x31, 0x36, 0x47, 0x80, 0x12, 0x34, 0x56, 0x78, 0x01, 0x23}))
	add(sdIdle(10))
	add(sdToken(true, 17, 0x00001000, true))
	add(sdIdle(10))
	clockBits(l, bits, per)
	r := DecodeSDCard(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if (sp.Kind == "data" || sp.Kind == "frame-error") && (strings.Contains(sp.Text, "CMD") || sp.Text[0] == 'R') {
			repo = append(repo, sp.Text)
		}
	}
	// sigrok reads CMD at the clock's rises: channels CMD, CLK
	chans := [][]byte{l.b.bits, l.a.bits}
	anns := sigrokDecode(t, sr, []string{"CMD", "CLK"}, chans, "sdcard_sd:cmd=CMD:clk=CLK", "sdcard_sd=commands")
	if len(anns) == 0 { // the 0.5 releases' row, their replies "Reply: R1"
		anns = sigrokDecode(t, sr, []string{"CMD", "CLK"}, chans, "sdcard_sd:cmd=CMD:clk=CLK", "sdcard_sd=cmd")
	}
	var orc []string
	for _, a := range sortAnns(anns) {
		switch {
		case strings.HasPrefix(a.Text, "Response: "):
			orc = append(orc, "R"+strings.TrimPrefix(a.Text, "Response: R"))
		case strings.HasPrefix(a.Text, "Reply: "):
			orc = append(orc, strings.Fields(strings.TrimPrefix(a.Text, "Reply: "))[0])
		default:
			orc = append(orc, strings.Fields(a.Text)[0])
		}
	}
	// the broken CRC: ours flags it, sigrok does not check it
	repo[len(repo)-1] = strings.TrimPrefix(repo[len(repo)-1], "!")
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("SD differs:\n repo   %v\n sigrok %v", repo, orc)
	}
	if !strings.HasPrefix(r.Spans[len(r.Spans)-1].Text, "!") {
		t.Fatal("broken CRC not flagged")
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestOraclePDI(t *testing.T) {
	const sr = 2000000
	const per = 4e-6
	l := newTwoLine(sr)
	var bits []int
	idle := func(n int) {
		for i := 0; i < n; i++ {
			bits = append(bits, 1)
		}
	}
	chars := func(v ...int) {
		for _, x := range v {
			bits = append(bits, pdiChar(x, false)...)
		}
	}
	idle(16)
	chars(0xC2, 0x59)                               // STCS ctrl, 0x59
	chars(0x80, 0x07)                               // LDCS status, the device's byte
	chars(0x0C, 0x00, 0x00, 0x00, 0x01)             // LDS a4 m1
	chars(0x4C, 0xCA, 0x01, 0x00, 0x01, 0x55)       // STS a4 i1
	chars(0xA0, 0x03, 0x24, 0x10, 0x20, 0x30, 0x40) // REPEAT 3, LD *(ptr++) m1, four bytes
	chars(0xE0, 1, 2, 3, 4, 5, 6, 7, 8)             // KEY
	bits = append(bits, pdiChar(0x6B, true)...)     // a parity error
	idle(4)
	// (no BREAK: sigrok's break handling compares a bit with "0" and never
	// ends one)
	chars(0x80, 0x02)
	idle(8)
	clockBits(l, bits, per)
	r := DecodePDI(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "data":
			repo = append(repo, fmt.Sprintf("%02x", sp.Val))
		case "rw":
			repo = append(repo, "BRK")
		}
	}
	anns := sigrokDecode(t, sr, []string{"CLK", "DATA"}, [][]byte{l.a.bits, l.b.bits}, "avr_pdi:reset=CLK:data=DATA", "avr_pdi=uart_fields")
	var orc []string
	for _, a := range sortAnns(anns) {
		switch {
		case strings.HasPrefix(a.Text, "Data: "):
			orc = append(orc, strings.TrimPrefix(a.Text, "Data: "))
		case a.Text == "Break condition":
			orc = append(orc, "BRK")
		}
	}
	// sigrok lists the bad character's data too: ours flags it
	var bad []string
	for _, sp := range r.Spans {
		if sp.Kind == "parity-error" {
			bad = append(bad, fmt.Sprintf("%02x", sp.Val))
		}
	}
	var o2 []string
	for _, x := range orc {
		if len(bad) > 0 && x == bad[0] {
			bad = bad[1:]
			continue
		}
		o2 = append(o2, x)
	}
	if fmt.Sprint(repo) != fmt.Sprint(o2) {
		t.Fatalf("PDI differs:\n repo   %v\n sigrok %v", repo, o2)
	}
	cmds := sigrokDecode(t, sr, []string{"CLK", "DATA"}, [][]byte{l.a.bits, l.b.bits}, "avr_pdi:reset=CLK:data=DATA", "avr_pdi=opcode")
	var ops []string
	for _, a := range sortAnns(cmds) {
		ops = append(ops, strings.Fields(strings.TrimPrefix(a.Text, "Insn: "))[0])
	}
	var mine []string
	for _, sp := range r.Spans {
		for _, op := range pdiOps {
			if sp.Text == op {
				mine = append(mine, op)
			}
		}
	}
	if fmt.Sprint(mine) != fmt.Sprint(ops) {
		t.Fatalf("PDI instructions differ:\n repo   %v\n sigrok %v", mine, ops)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestOracleCJTAG(t *testing.T) {
	needSigrokPD(t, "cjtag")
	const sr = 2000000
	c := &cjtagLine{l: newTwoLine(sr), per: 4e-6}
	for i := 0; i < 6; i++ {
		c.rise(0)
	}
	c.escape(6)
	for i, b := range []int{1, 1, 0, 0, 1, 0, 0, 1, 1, 0, 0, 0, 1} {
		_ = i
		c.rise(b)
	}
	c.tck(0, 0, 0, true) // reset -> run-test/idle
	c.scan(true, 4, 0x5, 0x1)
	c.scan(false, 32, 0x12345678, 0x4BA00477)
	c.scan(false, 8, 0xA5, 0x3C)
	for i := 0; i < 6; i++ {
		c.tck(0, 0, 0, false)
	}
	r := DecodeCJTAG(c.l.a.codes(), c.l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" {
			repo = append(repo, sp.Text[:2]+" "+strconv.FormatInt(int64(sp.Val), 16))
		}
	}
	anns := sigrokDecode(t, sr, []string{"TCKC", "TMSC"}, [][]byte{c.l.a.bits, c.l.b.bits}, "cjtag:tckc=TCKC:tmsc=TMSC", "cjtag=bitstring-tdi")
	var orc []string
	for _, a := range sortAnns(anns) {
		// "IR TDI: 0101 (0x5), 4 bits"
		f := strings.Fields(a.Text)
		orc = append(orc, f[0]+" "+strings.TrimSuffix(strings.TrimPrefix(f[3], "(0x"), "),"))
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("cJTAG differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestOracleMaple(t *testing.T) {
	const sr = 20000000
	m := &mapleLine{l: newTwoLine(sr), st: 250e-9}
	m.set(1, 1)
	frames := [][]int{mapleBytes(0x20, 0x00, 0x01, nil, false), mapleBytes(0x00, 0x20, 0x05, []int{0x00, 0x00, 0x00, 0x01, 0x12, 0x34, 0x56, 0x78}, false),
		mapleBytes(0x01, 0x20, 0x09, []int{0xFF, 0x00, 0xA5, 0x5A}, true)}
	for _, f := range frames {
		m.frame(f)
		for i := 0; i < 20; i++ {
			m.set(1, 1)
		}
	}
	r := DecodeMaple(m.l.a.codes(), m.l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "data":
			repo = append(repo, sp.Text[len(sp.Text)-2:])
		case "frame-error":
			repo = append(repo, sp.Text[len(sp.Text)-2:], "KE")
		}
	}
	pd := "maple_bus:sdcka=A:sdckb=B"
	chans := [][]byte{m.l.a.bits, m.l.b.bits}
	all := append(sigrokDecode(t, sr, []string{"A", "B"}, chans, pd, "maple_bus=fields"),
		sigrokDecode(t, sr, []string{"A", "B"}, chans, pd, "maple_bus=checksum-error")...)
	var orc []string
	for _, a := range sortAnns(all) {
		if strings.HasPrefix(a.Text, "Cksum error") {
			orc = append(orc, "KE")
			continue
		}
		f := strings.Fields(a.Text)
		orc = append(orc, f[len(f)-1])
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("Maple differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestOracleCaliper(t *testing.T) {
	needSigrokPD(t, "caliper")
	const sr = 1000000
	l := newTwoLine(sr)
	l.add(0, 0, 20e-3)
	caliperFrame(l, 1234, false, false, 80e-6, 30e-3)
	caliperFrame(l, 1234, true, false, 80e-6, 30e-3)
	caliperFrame(l, 2001, false, true, 80e-6, 30e-3)
	caliperFrame(l, 0, false, false, 80e-6, 30e-3)
	r := DecodeCaliper(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "rw" {
			repo = append(repo, sp.Text)
		}
	}
	anns := sigrokDecode(t, sr, []string{"CLK", "DATA"}, [][]byte{l.a.bits, l.b.bits}, "caliper:clk=CLK:data=DATA", "caliper=measurement")
	var orc []string
	for _, a := range sortAnns(anns) {
		v := strings.TrimRight(a.Text, "minch")
		unit := strings.TrimPrefix(a.Text, v)
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatal(a.Text)
		}
		orc = append(orc, strconv.FormatFloat(f, 'f', -1, 64)+unit)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("caliper differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
