// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the single-wire buses beyond the first thirty
// against sigrok's swim, sdq, pjdl with pjon, dsi and qi decoders
// (oracle_test.go).

import (
	"fmt"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func TestOracleSWIM(t *testing.T) {
	const sr = 20000000
	const T = 22 / 4e6
	w := newTimeline(sr)
	w.add(1, 20e-6)
	swimSync(w, T)
	gap := 3 * T
	// read on the fly: 2 bytes at 0x00 0x50 0x0A from the device
	swimFrame(w, 0, 3, 1, T, 1, false, gap)
	swimFrame(w, 0, 8, 2, T, 1, false, gap)
	swimFrame(w, 0, 8, 0x00, T, 1, false, gap)
	swimFrame(w, 0, 8, 0x50, T, 1, false, gap)
	swimFrame(w, 0, 8, 0x0A, T, 1, false, gap)
	swimFrame(w, 1, 8, 0x5A, T, 1, false, gap)
	swimFrame(w, 1, 8, 0xC3, T, 1, false, gap)
	// write on the fly: 1 byte
	swimFrame(w, 0, 3, 2, T, 1, false, gap)
	swimFrame(w, 0, 8, 1, T, 1, false, gap)
	swimFrame(w, 0, 8, 0x00, T, 1, false, gap)
	swimFrame(w, 0, 8, 0x7F, T, 1, false, gap)
	swimFrame(w, 0, 8, 0x60, T, 1, false, gap)
	swimFrame(w, 0, 8, 0xA5, T, 1, false, gap)
	swimFrame(w, 0, 3, 0, T, 1, false, gap)
	r := DecodeSWIM(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	addr := ""
	for _, sp := range r.Spans {
		if sp.Kind != "data" {
			continue
		}
		switch {
		case sp.Text[0] == '@':
			addr += sp.Text[1:]
			if len(addr) == 6 {
				repo = append(repo, "A"+strings.ToLower(addr))
				addr = ""
			}
		case sp.Text[0] == 'N':
			repo = append(repo, "N"+strings.ToLower(sp.Text[1:]))
		case sp.Text[0] == 'W' && len(sp.Text) == 3, sp.Text[0] == 'R' && len(sp.Text) == 3:
			repo = append(repo, sp.Text[:1]+strings.ToLower(sp.Text[1:]))
		default:
			repo = append(repo, sp.Text)
		}
	}
	skipSigrokDefect(t, []string{"D"}, "swim:swim=D", "swim=protocol", "no attribute 'samplerate'")
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "swim:swim=D", "swim=protocol")
	var orc []string
	for _, a := range sortAnns(anns) {
		switch {
		case a.Text == "system reset":
			orc = append(orc, "SRST")
		case a.Text == "read on-the-fly":
			orc = append(orc, "ROTF")
		case a.Text == "write on-the-fly":
			orc = append(orc, "WOTF")
		case strings.HasPrefix(a.Text, "byte count 0x"):
			orc = append(orc, "N"+strings.TrimPrefix(a.Text, "byte count 0x"))
		case strings.HasPrefix(a.Text, "address 0x"):
			orc = append(orc, "A"+strings.TrimPrefix(a.Text, "address 0x"))
		case strings.HasPrefix(a.Text, "0x"):
			orc = append(orc, "?"+a.Text[2:])
		}
	}
	// sigrok's data annotations do not say the direction: ours do
	for i := range orc {
		if i < len(repo) && strings.HasPrefix(orc[i], "?") && len(repo[i]) == 3 && (repo[i][0] == 'W' || repo[i][0] == 'R') {
			orc[i] = repo[i][:1] + orc[i][1:]
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("SWIM differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func TestOracleSDQ(t *testing.T) {
	needSigrokPD(t, "sdq")
	const sr = 10000000
	T := 1 / 98425.0
	w := newTimeline(sr)
	w.add(1, 30e-6)
	sdqBreak(w, T)
	for _, v := range []int{0x33, 0x01, 0xFE, 0x80, 0x55} {
		sdqByte(w, v, T)
	}
	w.add(1, 50e-6)
	sdqBreak(w, T)
	for _, v := range []int{0xCC, 0x44} {
		sdqByte(w, v, T)
	}
	w.add(1, 50e-6)
	r := DecodeSDQ(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "sof":
			repo = append(repo, "BR")
		case "data":
			repo = append(repo, fmt.Sprintf("%02x", sp.Val))
		}
	}
	var orc []string
	pd := "sdq:sdq=D"
	all := append(sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, pd, "sdq=byte"),
		sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, pd, "sdq=break")...)
	for _, a := range sortAnns(all) {
		if a.Text == "Break" {
			orc = append(orc, "BR")
			continue
		}
		orc = append(orc, strings.TrimPrefix(a.Text, "Byte: 0x"))
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("SDQ differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func TestOraclePJDL(t *testing.T) {
	needSigrokPD(t, "pjdl")
	const sr = 2000000
	w := newTimeline(sr)
	w.add(0, 500e-6)
	frames := [][]int{pjonPacket(0x2A, 0x02, []int{0x11, 0x22, 0x33}, false), pjonPacket(0x05, 0x00, []int{0x48, 0x69}, true),
		pjonPacket(0x00, 0x00, []int{0xFF}, false)}
	for _, f := range frames {
		pjdlFrame(w, 1, f, 1e-3)
	}
	r := DecodePJDL(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK || r.FrameBits != 1 {
		t.Fatal(r.FrameBits, r.Error)
	}
	var repo []string
	cur := ""
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "sof":
			if cur != "" {
				repo = append(repo, cur)
			}
			cur = "INIT"
		case "data", "frame-error":
			cur += fmt.Sprintf(" %02x", sp.Val)
		}
	}
	repo = append(repo, cur)
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "pjdl:data=D", "pjdl=frame_bytes")
	var orc []string
	for _, a := range sortAnns(anns) {
		orc = append(orc, a.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("PJDL differs:\n repo   %v\n sigrok %v", repo, orc)
	}
	// the PJON layer: its CRC warnings fall where ours flag
	warn := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "pjdl:data=D,pjon", "pjon=warning")
	bad := 0
	for _, sp := range r.Spans {
		if sp.Kind == "frame-error" {
			bad++
		}
	}
	if bad != len(warn) {
		t.Fatalf("PJON: %d flagged bytes, sigrok %d warnings %v", bad, len(warn), warn)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func TestOracleDSI(t *testing.T) {
	const sr = 100000
	w := newTimeline(sr)
	w.add(0, 10e-3)
	vals := []int{0x00, 0xFF, 0x80, 0x55, 0xA5, 0x01, 0x7E}
	for _, v := range vals {
		dsiFrame(w, v, 12e-3)
	}
	r := DecodeDSI(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" {
			repo = append(repo, fmt.Sprintf("%02X", sp.Val))
		}
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "dsi:dsi=D", "dsi=raw")
	var orc []string
	for _, a := range sortAnns(anns) {
		if strings.HasPrefix(a.Text, "Data: ") {
			orc = append(orc, strings.TrimPrefix(a.Text, "Data: "))
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("DSI differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func TestOracleQi(t *testing.T) {
	const sr = 200000
	w := newTimeline(sr)
	w.add(0, 5e-3)
	pkts := [][]int{{0x01, 0x80}, {0x03, 0x05}, {0x02, 0x01}, {0x71, 0x11, 0x00, 0x1E, 0x81, 0x23, 0x45, 0x67}}
	for i, p := range pkts {
		qiPacket(w, p, i == 2, 8e-3)
	}
	r := DecodeQi(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "data":
			repo = append(repo, strings.ToLower(sp.Text))
		case "frame-error":
			repo = append(repo, "ERR", strings.ToLower(strings.TrimPrefix(sp.Text, "!"))) // sigrok marks it from the checksum byte's start
		}
	}
	pd := "qi:qi=D"
	all := append(sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, pd, "qi=bytes"),
		sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, pd, "qi=packets")...)
	var orc []string
	for _, a := range sortAnns(all) {
		switch {
		case len(a.Text) == 2:
			orc = append(orc, a.Text)
		case strings.HasPrefix(a.Text, "Checksum error") || strings.HasPrefix(a.Text, "Packet checksum") && strings.Contains(a.Text, "rr"):
			orc = append(orc, "ERR")
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("Qi differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
