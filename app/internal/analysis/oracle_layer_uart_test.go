// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// Oracle cross-checks of the UART layers (oracle_layer_helpers_test.go).
// sigrok's amulet_ascii fails on a page change (its page buffer is never
// made), so the Amulet check leaves PAGE out.

import (
	"regexp"
	"strings"
	"testing"
)

// orcUARTRun renders the bursts, decodes them with the layer and with
// sigrok's stack (uart, then pd: one decoder or a comma list).
// TRLC-LINKS: REQ-SDS-244
func orcUARTRun(t *testing.T, layer, uart, pd string, baud float64, parity string, stops int, inv bool, bursts [][]byte, gap float64) ([]Packet, []orcAnn) {
	t.Helper()
	const sr = 4_000_000
	line := orcUARTWave(sr, baud, parity, stops, inv, bursts, gap)
	ps := orcUARTLayer(t, sr, line, int(baud), parity, inv, layer)
	last := pd[strings.LastIndex(pd, ",")+1:]
	anns := orcDecode(t, sr, []string{"RX"}, [][]byte{line}, uart+","+pd, last)
	if testing.Verbose() {
		t.Logf("repo %q\nsigrok %q", orcNotes(ps), orcTexts(anns))
	}
	return ps, anns
}

// TRLC-LINKS: REQ-SDS-244
func TestOracleLayerSBUS(t *testing.T) {
	orcNeed(t, "uart", "sbus_futaba")
	var a, b [16]int
	for k := range a {
		a[k], b[k] = 172+100*k, 1811-90*k
	}
	ps, anns := orcUARTRun(t, "sbus", "uart:rx=RX:baudrate=100000:parity=even:stop_bits=2:invert_rx=yes", "sbus_futaba",
		100000, "even", 2, true, [][]byte{sbusBytes(a, 0x0c), sbusBytes(b, 0x01)}, 30)
	var repo []string
	for _, n := range orcNotes(ps) {
		f := strings.Fields(strings.TrimPrefix(n, "S.BUS "))
		flags := map[string]bool{}
		for _, w := range f[16:] {
			flags[w] = true
		}
		g := func(k string) string {
			if flags[k] {
				return "1"
			}
			return "0"
		}
		repo = append(repo, strings.Join(f[:16], " ")+" "+g("ch17")+g("ch18")+g("lost")+g("failsafe"))
	}
	var orc []string
	ts := orcTexts(anns)
	for i := 0; i+21 < len(ts); i++ {
		if ts[i] == "0x0f" {
			orc = append(orc, strings.Join(ts[i+1:i+17], " ")+" "+strings.Join(ts[i+17:i+21], ""))
			i += 21
		}
	}
	orcEq(t, "S.BUS frames", repo, orc)
}

// TRLC-LINKS: REQ-SDS-244
func TestOracleLayerITM(t *testing.T) {
	orcNeed(t, "uart", "arm_itm")
	// sigrok writes a channel's characters out at the next stimulus word
	// that comes 16 byte times after them, in the order it writes them: the
	// text ends each burst, and both sides are sorted by where they start
	ps, anns := orcUARTRun(t, "arm_itm", "uart:rx=RX:baudrate=115200", "arm_itm", 115200, "none", 1, false, [][]byte{
		{0, 0, 0, 0, 0, 0x80, 0x0b, 1, 2, 3, 4, 0x01, 'h', 0x01, 'i'},
		{0x70, 0x10, 0x0a, 0x34, 0x12, 0x09, 'O', 0x09, 'K'},
		{0x17, 0x78, 0x56, 0x34, 0x12, 0x0b, 9, 8, 7, 6},
	}, 300)
	sortAnns(anns)
	var repo []string
	for _, n := range orcNotes(ps) {
		for _, it := range regexp.MustCompile(`SYNC|OVERFLOW|TS \+\d+|ch\d+ '[^']*'|ch\d+ 0x[0-9A-F]+|PC 0x[0-9A-F]+`).FindAllString(n, -1) {
			repo = append(repo, it)
		}
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		switch {
		case s == "Unhandled sync: 00 00 00 00 00 80":
			orc = append(orc, "SYNC")
		case s == "Overflow":
			orc = append(orc, "OVERFLOW")
		case regexp.MustCompile(`^Timestamp: \d+ \(exact\)$`).MatchString(s):
			orc = append(orc, "TS +1") // sigrok sums the timestamps: one here
		case regexp.MustCompile(`^\d+: "`).MatchString(s):
			m := regexp.MustCompile(`^(\d+): "(.*)$`).FindStringSubmatch(s)
			orc = append(orc, "ch"+m[1]+" '"+strings.TrimSuffix(m[2], `"`)+"'")
		case regexp.MustCompile(`^\d+: 0x[0-9a-f]+$`).MatchString(s):
			m := regexp.MustCompile(`^(\d+): 0x([0-9a-f]+)$`).FindStringSubmatch(s)
			orc = append(orc, "ch"+m[1]+" 0x"+strings.ToUpper(m[2]))
		case strings.HasPrefix(s, "PC: 0x"):
			orc = append(orc, "PC 0x"+strings.ToUpper(strings.TrimPrefix(s, "PC: 0x")))
		}
	}
	orcEq(t, "ITM packets", repo, orc)
}

// TRLC-LINKS: REQ-SDS-244
func TestOracleLayerTPIU(t *testing.T) {
	orcNeed(t, "uart", "arm_tpiu", "arm_itm")
	// stream 1 the ITM, 2 another source; sigrok's TPIU passes stream 1 on
	// to its ITM decoder
	frames := append([]byte{0xff, 0xff, 0xff, 0x7f}, tpiuFrame([]tpiuItem{{id: 1}, {b: 0x0b}, {b: 1}, {b: 2}, {b: 3}, {b: 4},
		{id: 2}, {b: 0x10}, {b: 0x20}, {id: 1}, {b: 0x70}, {b: 0x0b}, {b: 5}, {b: 6}, {b: 7}, {b: 8}})...)
	ps, anns := orcUARTRun(t, "arm_tpiu", "uart:rx=RX:baudrate=115200", "arm_tpiu,arm_itm", 115200, "none", 1, false, [][]byte{frames}, 30)
	var repo []string
	for _, n := range orcNotes(ps) {
		for _, it := range regexp.MustCompile(`OVERFLOW|ch\d+ 0x[0-9A-F]+`).FindAllString(n, -1) {
			repo = append(repo, it)
		}
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		switch {
		case s == "Overflow":
			orc = append(orc, "OVERFLOW")
		case regexp.MustCompile(`^\d+: 0x[0-9a-f]+$`).MatchString(s):
			m := regexp.MustCompile(`^(\d+): 0x([0-9a-f]+)$`).FindStringSubmatch(s)
			orc = append(orc, "ch"+m[1]+" 0x"+strings.ToUpper(m[2]))
		}
	}
	orcEq(t, "TPIU stream 1 (ITM)", repo, orc)
	if n := strings.Join(orcNotes(ps), " "); !strings.Contains(n, "id2 10 20") {
		t.Fatalf("stream 2's bytes not named: %q", n)
	}
}

// TRLC-LINKS: REQ-SDS-244
func TestOracleLayerETMv3(t *testing.T) {
	orcNeed(t, "uart", "arm_etmv3")
	ps, anns := orcUARTRun(t, "arm_etmv3", "uart:rx=RX:baudrate=115200", "arm_etmv3", 115200, "none", 1, false, [][]byte{
		{0, 0, 0, 0, 0x80, 0x08, 0x20, 0x01, 0x01, 0x00, 0x08, 0x84, 0x25, 0x0c, 0x76, 0x7e, 0x88, 0x8c, 0x83, 0x02},
	}, 30)
	var repo []string
	for _, n := range orcNotes(ps) {
		for _, it := range regexp.MustCompile(`A-SYNC|I-SYNC 0x[0-9A-F]{8} (?:thumb|arm) (?:periodic|trace on|overflow|debug exit)|\d+E(?: \d+N)?|B 0x[0-9A-F]{8}|TRIGGER|EXC EXIT|EXC ENTRY`).FindAllString(n, -1) {
			repo = append(repo, strings.TrimSpace(it))
		}
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		switch {
		case s == "Synchronization":
			orc = append(orc, "A-SYNC")
		case strings.HasPrefix(s, "I-Sync: "):
			m := regexp.MustCompile(`^I-Sync: ([A-Za-z ]+), PC 0x([0-9a-f]{8}), (\w+) state`).FindStringSubmatch(s)
			reason := map[string]string{"Periodic": "periodic", "Tracing enabled": "trace on", "After overflow": "overflow",
				"Exit from debug": "debug exit"}[m[1]]
			orc = append(orc, "I-SYNC 0x"+strings.ToUpper(m[2])+" "+m[3]+" "+reason)
		case regexp.MustCompile(`^\d+ instructions executed`).MatchString(s):
			m := regexp.MustCompile(`^(\d+) instructions executed(?:, (\d+) skipped)?`).FindStringSubmatch(s)
			it := m[1] + "E"
			if m[2] != "" {
				it += " " + m[2] + "N"
			}
			orc = append(orc, it)
		case strings.HasPrefix(s, "Branch to 0x"):
			orc = append(orc, "B 0x"+strings.ToUpper(s[12:20]))
		case s == "Trigger event":
			orc = append(orc, "TRIGGER")
		case s == "Exception exit":
			orc = append(orc, "EXC EXIT")
		case s == "Exception entry":
			orc = append(orc, "EXC ENTRY")
		}
	}
	orcEq(t, "ETM packets", repo, orc)
}

// TRLC-LINKS: REQ-SDS-244
func TestOracleLayerAmulet(t *testing.T) {
	orcNeed(t, "uart", "amulet_ascii")
	ps, anns := orcUARTRun(t, "amulet", "uart:rx=RX:baudrate=115200", "amulet_ascii", 115200, "none", 1, false, [][]byte{
		{0xd0, '1', 'A'}, {0xd5, '0', '2', 'F', 'F'}, {0xd6, '0', '3', '1', '2', '3', '4'}, {0xd7, '0', '4', 'h', 'i', 0},
		{0xd1, '2', '2'}, {0xf0}, {0xf1},
	}, 30)
	repo := orcMatch(orcNotes(ps), `^([A-Z]+)`)
	var orc []string
	for _, s := range orcTexts(anns) { // a command's field and its whole say it alike: once each
		if m := regexp.MustCompile(`^Command: .* \(([A-Z]+)\)$`).FindStringSubmatch(s); m != nil && (len(orc) == 0 || orc[len(orc)-1] != m[1]) {
			orc = append(orc, m[1])
		}
	}
	orcEq(t, "Amulet commands", repo, orc)
	// the addresses and values both read
	orcEq(t, "Amulet values", orcMatch(orcNotes(ps), `^SBV 0x02 = 0x(FF)$|^SWV 0x03 = 0x(1234)$`),
		orcMatch(orcTexts(anns), `^Value: 0x(FF)$|^Value: 0x(1234)$`))
}

// TRLC-LINKS: REQ-SDS-244
func TestOracleLayerPAN1321(t *testing.T) {
	orcNeed(t, "uart", "pan1321")
	ps, anns := orcUARTRun(t, "pan1321", "uart:tx=RX:baudrate=115200", "pan1321", 115200, "none", 1, false, [][]byte{
		[]byte("AT+JSLN=05,hello\r\n"), []byte("AT+JSEC=1,1,2,04,1234\r\n"), []byte("AT+JAAC=1\r\n"), []byte("AT+JRES\r\n"),
		[]byte("AT+JAAC=0\r\n"), []byte("AT+JPRO=1\r\n"),
	}, 30)
	var repo []string
	for _, n := range orcNotes(ps) {
		repo = append(repo, strings.TrimPrefix(n, "PAN1321 "))
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		switch {
		case strings.HasPrefix(s, "Host set the Bluetooth name to "):
			orc = append(orc, "name '"+strings.Trim(strings.TrimPrefix(s, "Host set the Bluetooth name to "), `"`)+"'")
		case strings.HasPrefix(s, "Host set the Bluetooth PIN to "):
			orc = append(orc, "PIN "+strings.Trim(strings.TrimPrefix(s, "Host set the Bluetooth PIN to "), `"`))
		case s == "Auto-accept new connections":
			orc = append(orc, "auto-accept on")
		case s == "Don't auto-accept new connections":
			orc = append(orc, "auto-accept off")
		case s == "Triggering a software reset":
			orc = append(orc, "reset")
		case s == "Entering production mode":
			orc = append(orc, "production mode 1")
		}
	}
	orcEq(t, "PAN1321 commands", repo, orc)
}
