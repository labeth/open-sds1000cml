// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the UART-framed protocols against sigrok's stacked
// uart -> lin / midi / modbus decoders and its dmx512 decoder, on the same
// waveforms (oracle_test.go).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// oracleItem is one decoded field: a kind tag, a value and whether it checked.
type oracleItem struct {
	Kind string
	Val  int
	OK   bool
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func TestOracleLIN(t *testing.T) {
	const sr = 1000000
	w := newTimeline(sr)
	w.idle(200e-6)
	linFrame(w, 19200, 13, 0x3c, []int{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80}, true)
	linFrame(w, 19200, 14, 0x12, []int{0xA5, 0x5A}, false)
	linFrame(w, 19200, 13, 0x07, []int{0x00, 0xFF, 0x33, 0x44}, false)
	// a frame with a wrong checksum
	bit := 1 / 19200.0
	w.add(0, 13*bit)
	w.add(1, bit)
	tlUART(w, 0x55, 19200, "none", 1)
	tlUART(w, linPIDParity(0x21), 19200, "none", 1)
	w.add(1, 2*bit)
	tlUART(w, 0x11, 19200, "none", 1)
	tlUART(w, 0x99, 19200, "none", 1)
	w.idle(40 * bit)
	linFrame(w, 19200, 13, 0x01, []int{0x01}, false)

	r := DecodeLIN(w.codes(), 1.0/sr, LINCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []oracleItem
	for _, sp := range r.Spans {
		ok := sp.Kind == "data"
		switch {
		case strings.HasPrefix(strings.TrimPrefix(sp.Text, "!"), "ID"):
			repo = append(repo, oracleItem{"ID", sp.Val & 0x3f, ok})
		case strings.HasPrefix(strings.TrimPrefix(sp.Text, "!"), "CK"):
			repo = append(repo, oracleItem{"C", sp.Val, ok})
		case sp.Kind == "data" || sp.Kind == "frame-error" && sp.Text != "!SYNC":
			repo = append(repo, oracleItem{"D", sp.Val, ok})
		}
	}
	pd := "uart:rx=RX:baudrate=19200,lin:version=2"
	good := sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, pd, "lin=data")
	bad := sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, pd, "lin=inline_error")
	type at struct {
		a  ann
		ok bool
	}
	var all []at
	for _, a := range good {
		all = append(all, at{a, true})
	}
	for _, a := range bad {
		all = append(all, at{a, false})
	}
	// merge in sample order (the classes are printed separately)
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].a.I0 < all[j-1].a.I0; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	idRe := regexp.MustCompile(`^ID: ([0-9A-F]{2})`)
	hexRe := regexp.MustCompile(`0x([0-9A-F]{2})`)
	var orc []oracleItem
	for _, x := range all {
		txt := x.a.Text
		switch {
		case strings.HasPrefix(txt, "Sync") || txt == "Error":
		case idRe.MatchString(txt):
			v, _ := strconv.ParseInt(idRe.FindStringSubmatch(txt)[1], 16, 32)
			orc = append(orc, oracleItem{"ID", int(v), x.ok})
		case strings.HasPrefix(txt, "Checksum"):
			v, _ := strconv.ParseInt(hexRe.FindStringSubmatch(txt)[1], 16, 32)
			orc = append(orc, oracleItem{"C", int(v), x.ok})
		case strings.HasPrefix(txt, "Data"):
			v, _ := strconv.ParseInt(hexRe.FindStringSubmatch(txt)[1], 16, 32)
			orc = append(orc, oracleItem{"D", int(v), x.ok})
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("LIN fields differ:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func TestOracleDMX512(t *testing.T) {
	// sigrok's DMX512 decoder (0.5) reads a packet to its 512th slot and
	// a slot from the edge after a mark, so the packets here are full ones
	// with a mark between the slots.
	needSigrok(t)
	if !sigrokLogicDMX(t) {
		t.Skip("this sigrok's DMX512 decoder is stacked on UART; the cross-check is written for the 0.5 one CI runs")
	}
	const sr = 4000000
	w := newTimeline(sr)
	w.idle(20e-6)
	var want []int
	for p := 0; p < 2; p++ {
		slots := make([]int, 512)
		for i := range slots {
			slots[i] = (i*7 + p*31) & 0xff
		}
		dmxPacketGap(w, 120, 12, 8, p*0x55, slots)
		want = append(append(want, p*0x55), slots...)
	}
	r := DecodeDMX512(w.codes(), 1.0/sr, DMXCfg{})
	if !r.OK || strings.Contains(r.Text, "!") {
		t.Fatalf("repo: ok=%v %s", r.OK, r.Error)
	}
	anns := sigrokDecode(t, sr, []string{"DMX"}, [][]byte{w.bits}, "dmx512:dmx=DMX", "dmx512=data")
	var orc []int
	for _, a := range anns {
		v, err := strconv.Atoi(strings.Fields(a.Text)[0])
		if err != nil {
			t.Fatalf("annotation %q", a.Text)
		}
		orc = append(orc, v)
	}
	eqBytes(t, "DMX512 slots", r.Bytes, orc)
	eqBytes(t, "DMX512 generated", r.Bytes, want)
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func TestOracleMIDI(t *testing.T) {
	const sr = 1000000
	w := newTimeline(sr)
	w.idle(200e-6)
	msgs := []int{0x90, 0x3C, 0x64, 0x3E, 0x50, 0x81, 0x3C, 0x00, 0xB2, 0x07, 0x64, 0xC3, 0x05,
		0xD4, 0x40, 0xE5, 0x00, 0x40, 0xF8, 0x90, 0x40, 0x7F}
	for _, b := range msgs {
		tlUART(w, b, 31250, "none", 1)
	}
	w.idle(2e-3)
	r := DecodeMIDI(w.codes(), 1.0/sr, MIDICfg{})
	if !r.OK || strings.Contains(r.Text, "!") {
		t.Fatalf("repo: %q %s", r.Text, r.Error)
	}
	// channel messages: the repo's channel per message (running status
	// repeats the last), sigrok's "Channel N:" annotations
	var repo []int
	chans := 0
	need, got := 0, 0
	for _, sp := range r.Spans {
		if sp.Kind != "data" {
			continue
		}
		switch {
		case sp.Val >= 0xF8:
		case sp.Val >= 0x80:
			chans, need, got = sp.Val&0xf+1, midiDataLen(sp.Val), 0
		default:
			if got++; got == need {
				repo = append(repo, chans)
				got = 0
			}
		}
	}
	anns := sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, "uart:rx=RX:baudrate=31250,midi", "midi=text-verbose")
	chRe := regexp.MustCompile(`^Channel (\d+):`)
	var orc []int
	for _, a := range anns {
		if m := chRe.FindStringSubmatch(a.Text); m != nil {
			v, _ := strconv.Atoi(m[1])
			orc = append(orc, v)
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("channel messages differ: repo %v sigrok %v", repo, orc)
	}
	real := sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, "uart:rx=RX:baudrate=31250,midi", "midi=text-sysreal-verbose")
	if len(real) != 1 {
		t.Fatalf("sigrok real-time messages %d, want 1", len(real))
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func TestOracleModbus(t *testing.T) {
	const sr = 2000000
	for _, tc := range []struct {
		parity string
		stops  int
	}{{"even", 1}, {"none", 2}} {
		w := newTimeline(sr)
		w.idle(3e-3)
		// server responses (sigrok reads the server->client channel as such)
		frames := [][]int{{0x01, 0x03, 0x02, 0x12, 0x34}, {0x01, 0x03, 0x06, 0x02, 0x2B, 0x00, 0x00, 0x00, 0x64},
			{0x11, 0x06, 0x00, 0x01, 0x00, 0x03}, {0x0A, 0x05, 0x00, 0xAC, 0xFF, 0x00}}
		for _, f := range frames {
			modbusFrame(w, 19200, tc.parity, tc.stops, f)
		}
		// a frame with a broken CRC
		for _, v := range []int{0x05, 0x03, 0x02, 0x00, 0x10, 0x12, 0x34} {
			tlUART(w, v, 19200, tc.parity, tc.stops)
		}
		w.idle(3e-3)
		r := DecodeModbus(w.codes(), 1.0/sr, ModbusCfg{})
		if !r.OK || r.Parity != tc.parity {
			t.Fatalf("%s: ok=%v parity %q %s", tc.parity, r.OK, r.Parity, r.Error)
		}
		var repo []oracleItem
		for k, sp := range r.Spans {
			if sp.Kind != "sof" {
				continue
			}
			addr := r.Spans[k+1]
			fn := r.Spans[k+2]
			end := k + 1
			for end < len(r.Spans) && r.Spans[end].Kind != "sof" {
				end++
			}
			crcOK := r.Spans[end-1].Kind == "data"
			repo = append(repo, oracleItem{"S", addr.Val, true}, oracleItem{"F", fn.Val, true}, oracleItem{"C", 0, crcOK})
		}
		stop := "1.0"
		if tc.stops == 2 {
			stop = "2.0"
		}
		pd := fmt.Sprintf("uart:rx=RX:baudrate=19200:parity=%s:stop_bits=%s,modbus:scchannel=RX", tc.parity, stop)
		ids := sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, pd, "modbus=sc-server-id")
		fns := sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, pd, "modbus=sc-function")
		crcs := sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, pd, "modbus=sc-crc")
		for _, e := range sigrokDecode(t, sr, []string{"RX"}, [][]byte{w.bits}, pd, "modbus=sc-error") {
			if strings.HasPrefix(e.Text, "CRC should be") {
				crcs = append(crcs, e)
			}
		}
		for i := 1; i < len(crcs); i++ {
			for j := i; j > 0 && crcs[j].I0 < crcs[j-1].I0; j-- {
				crcs[j], crcs[j-1] = crcs[j-1], crcs[j]
			}
		}
		if len(ids) != len(fns) || len(ids) != len(crcs) {
			t.Fatalf("%s: sigrok ids %d functions %d crcs %d", tc.parity, len(ids), len(fns), len(crcs))
		}
		idRe := regexp.MustCompile(`Slave ID:? (\d+)`)
		fnRe := regexp.MustCompile(`Function (\d+)`)
		var orc []oracleItem
		for i := range ids {
			id, fn := idRe.FindStringSubmatch(ids[i].Text), fnRe.FindStringSubmatch(fns[i].Text)
			if id == nil || fn == nil {
				t.Fatalf("%s: annotations %q %q", tc.parity, ids[i].Text, fns[i].Text)
			}
			a, _ := strconv.Atoi(id[1])
			f, _ := strconv.Atoi(fn[1])
			orc = append(orc, oracleItem{"S", a, true}, oracleItem{"F", f, true}, oracleItem{"C", 0, crcs[i].Text == "CRC correct"})
		}
		if fmt.Sprint(repo) != fmt.Sprint(orc) {
			t.Fatalf("%s: frames differ:\n repo   %v\n sigrok %v", tc.parity, repo, orc)
		}
	}
}
