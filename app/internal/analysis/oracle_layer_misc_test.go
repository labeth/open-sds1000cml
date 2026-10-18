// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// Oracle cross-checks of the 1-Wire, MDIO and USB layers
// (oracle_layer_helpers_test.go): onewire_network and the device decoders
// stacked on it, cfp on mdio, usb_request on usb_packet.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// orcOWRun decodes 1-Wire transactions with the layer and with sigrok's
// onewire_link and the stack on it.
// TRLC-LINKS: REQ-SDS-245
func orcOWRun(t *testing.T, layer, pd string, txns [][]byte) ([]Packet, []orcAnn) {
	t.Helper()
	const sr = 1_000_000
	line := orcOWWave(sr, txns)
	ps := orcOWLayer(t, sr, line, layer)
	last := pd[strings.LastIndex(pd, ",")+1:]
	anns := orcDecode(t, sr, []string{"OW"}, [][]byte{line}, "onewire_link:owr=OW,"+pd, last)
	if testing.Verbose() {
		t.Logf("repo %q\nsigrok %q", orcNotes(ps), orcTexts(anns))
	}
	return ps, anns
}

// orcSigrokROM writes sigrok's 64-bit ROM value (family in the low byte)
// as the scope's family-serial.
// TRLC-LINKS: REQ-SDS-245
func orcSigrokROM(s string) string {
	s = strings.ToUpper(s)
	return s[14:16] + "-" + s[2:14]
}

// TRLC-LINKS: REQ-SDS-245
func TestOracleLayerOneWireNetwork(t *testing.T) {
	orcNeed(t, "onewire_link", "onewire_network")
	rom := orcROM(0x28, 0x2c, 0x1b, 0x6a, 0x05, 0, 0)
	ps, anns := orcOWRun(t, "onewire_net", "onewire_network", [][]byte{append(append([]byte{0x55}, rom...), 0x44),
		{0xcc, 0xbe, 0x12}, append([]byte{0x33}, rom...), {0xa5, 0x01}})
	var repo []string
	for _, n := range orcNotes(ps) {
		m := regexp.MustCompile(`^([A-Z ]+?)(?: ([0-9A-F]{2}-[0-9A-F]{12}))?(?:: ([0-9A-F ]+))?$`).FindStringSubmatch(n)
		repo = append(repo, m[1]+"|"+m[2]+"|"+m[3])
	}
	names := map[string]string{"Match ROM": "MATCH ROM", "Skip ROM": "SKIP ROM", "Read ROM": "READ ROM", "Resume": "RESUME"}
	var orc []string
	cur := ""
	var rom2, data []string
	flush := func() {
		if cur != "" {
			orc = append(orc, cur+"|"+strings.Join(rom2, "")+"|"+strings.Join(data, " "))
		}
		cur, rom2, data = "", nil, nil
	}
	for _, s := range orcTexts(anns) {
		switch {
		case strings.HasPrefix(s, "ROM command: "):
			flush()
			cur = names[regexp.MustCompile(`'(.*)'`).FindStringSubmatch(s)[1]]
		case strings.HasPrefix(s, "ROM: 0x"):
			rom2 = append(rom2, orcSigrokROM(s[7:23]))
		case strings.HasPrefix(s, "Data: 0x"):
			data = append(data, strings.ToUpper(s[8:10]))
		}
	}
	flush()
	orcEq(t, "1-Wire transactions", repo, orc)
}

// TRLC-LINKS: REQ-SDS-245
func TestOracleLayerDS28EA00(t *testing.T) {
	orcNeed(t, "onewire_link", "onewire_network", "ds28ea00")
	rom := orcROM(0x42, 1, 2, 3, 4, 5, 6)
	sp := []byte{0x91, 0x01, 0x4b, 0x46, 0x7f, 0xff, 0x0f, 0x10, 0}
	sp[8] = byte(crc8Maxim(sp[:8]))
	sp2 := []byte{0x5e, 0xff, 0x4b, 0x46, 0x7f, 0xff, 0x02, 0x10, 0}
	sp2[8] = byte(crc8Maxim(sp2[:8]))
	ps, anns := orcOWRun(t, "ds28ea00", "onewire_network,ds28ea00", [][]byte{
		append(append([]byte{0x55}, rom...), 0x44), append(append(append([]byte{0x55}, rom...), 0xbe), sp...),
		append(append(append([]byte{0x55}, rom...), 0xbe), sp2...), append(append([]byte{0x55}, rom...), 0xb4)})
	names := map[string]string{"Convert temperature": "Convert T", "Read scratchpad": "Read scratchpad", "Read power mode": "Read power mode"}
	repo := orcMatch(orcNotes(ps), `: (Convert T|Read scratchpad|Read power mode)(?: (-?\d+\.\d+) C)?`)
	var orc []string
	ts := orcTexts(anns)
	for i, s := range ts {
		m := regexp.MustCompile(`^Function command: 0x[0-9a-f]{2} '(.*)'$`).FindStringSubmatch(s)
		if m == nil {
			continue
		}
		temp := ""
		if m[1] == "Read scratchpad" { // the temperature in sixteenths from the first two bytes sigrok read
			lo, _ := strconv.ParseUint(strings.TrimPrefix(ts[i+1], "Scratchpad data: 0x"), 16, 8)
			hi, _ := strconv.ParseUint(strings.TrimPrefix(ts[i+2], "Scratchpad data: 0x"), 16, 8)
			temp = fmt.Sprintf("%.4f", float64(int16(uint16(hi)<<8|uint16(lo)))/16)
		}
		orc = append(orc, names[m[1]]+"|"+temp)
	}
	orcEq(t, "DS28EA00 functions", repo, orc)
}

// TRLC-LINKS: REQ-SDS-245
func TestOracleLayerDS2408(t *testing.T) {
	orcNeed(t, "onewire_link", "onewire_network", "ds2408")
	rom := orcROM(0x29, 1, 2, 3, 4, 5, 6)
	ps, anns := orcOWRun(t, "ds2408", "onewire_network,ds2408", [][]byte{
		append(append([]byte{0x55}, rom...), 0xf0, 0x88, 0x00, 0xff), append(append([]byte{0x55}, rom...), 0x5a, 0x0f, 0xf0, 0xaa, 0x0f),
		append(append([]byte{0x55}, rom...), 0xc3, 0xaa, 0xaa)})
	repo := orcMatch(orcNotes(ps), `: (Read PIO registers @0x0088: FF|Channel access write 0x0F done|Reset activity latches done)$`)
	var orc []string
	for _, s := range strings.Split(strings.Join(orcTexts(anns), "\n"), "Reset/presence: true") {
		switch {
		case strings.Contains(s, "Read PIO Registers") && strings.Contains(s, "Target address: 0x0088") && strings.Contains(s, "Data: 0xff"):
			orc = append(orc, "Read PIO registers @0x0088: FF")
		case strings.Contains(s, "Channel Access Write") && strings.Contains(s, "Data: 0x0f (bit-inversion correct") && strings.Contains(s, "Success"):
			orc = append(orc, "Channel access write 0x0F done")
		case strings.Contains(s, "Reset Activity Latches") && strings.Contains(s, "Success"):
			orc = append(orc, "Reset activity latches done")
		}
	}
	orcEq(t, "DS2408 functions", repo, orc)
}

// TRLC-LINKS: REQ-SDS-245
func TestOracleLayerDS243x(t *testing.T) {
	orcNeed(t, "onewire_link", "onewire_network", "ds243x")
	rom := orcROM(0x23, 1, 2, 3, 4, 5, 6)
	ws := []byte{0x0f, 0x10, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 0, 0}
	c := crc16Maxim(ws[:11])
	ws[11], ws[12] = byte(c), byte(c>>8)
	ps, anns := orcOWRun(t, "ds243x", "onewire_network,ds243x", [][]byte{
		append(append([]byte{0x55}, rom...), 0xf0, 0x20, 0x00, 1, 2, 3), append(append([]byte{0x55}, rom...), ws...)})
	var repo []string
	for _, n := range orcNotes(ps) {
		if m := regexp.MustCompile(`: Read memory @0x([0-9A-F]{4}), \d+ bytes: ([0-9A-F ]+)$`).FindStringSubmatch(n); m != nil {
			repo = append(repo, "read "+m[1]+" "+m[2])
		}
		if m := regexp.MustCompile(`: Write scratchpad @0x([0-9A-F]{4}): ([0-9A-F ]+) CRC (ok|bad)$`).FindStringSubmatch(n); m != nil {
			repo = append(repo, "write "+m[1]+" "+m[2]+" "+m[3])
		}
	}
	var orc []string
	for _, s := range strings.Split(strings.Join(orcTexts(anns), "\n"), "Reset/presence: true") {
		if strings.Contains(s, "Function command: Read memory") {
			ta := regexp.MustCompile(`Target address: 0x([0-9a-f]{4})`).FindStringSubmatch(s)[1]
			var d []string
			for _, m := range regexp.MustCompile(`(?m)^Data: 0x([0-9a-f]{2})$`).FindAllStringSubmatch(s, -1) {
				d = append(d, strings.ToUpper(m[1]))
			}
			orc = append(orc, "read "+strings.ToUpper(ta)+" "+strings.Join(d, " "))
		}
		if strings.Contains(s, "Function command: Write scratchpad") {
			ta := regexp.MustCompile(`Target address: 0x([0-9a-f]{4})`).FindStringSubmatch(s)[1]
			d := regexp.MustCompile(`Data: ((?:0x[0-9a-f]{2},?)+)`).FindStringSubmatch(s)[1]
			d = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(d, "0x", ""), ",", " "))
			crc := regexp.MustCompile(`CRC: (ok|error)`).FindStringSubmatch(s)[1]
			orc = append(orc, "write "+strings.ToUpper(ta)+" "+d+" "+map[string]string{"ok": "ok", "error": "bad"}[crc])
		}
	}
	orcEq(t, "DS243x functions", repo, orc)
}

// TRLC-LINKS: REQ-SDS-246
func TestOracleLayerCFP(t *testing.T) {
	orcNeed(t, "mdio", "cfp")
	const sr = 10_000_000
	mdc, mdio := orcMDIOWaves(sr, 1e-6, []orcMDIOFrame{{0, 0, 0, 1, 0x8000}, {0, 3, 0, 1, 0x000e}, {0, 0, 0, 1, 0x8090},
		{0, 3, 0, 1, 0x1234}, {0, 0, 0, 1, 0xa000}, {0, 3, 0, 1, 0x55}, {0, 0, 0, 1, 0x8410}, {0, 2, 0, 1, 0x99}})
	r := decodeMDIO(t, sr, mdc, mdio)
	ps := ApplyLayer("cfp", "mdio", Packets(r, len(mdc)), LayerArgs{Addr: -1})
	anns := orcDecode(t, sr, []string{"MDC", "MDIO"}, [][]byte{mdc, mdio}, "mdio:mdc=MDC:mdio=MDIO,cfp", "cfp")
	if testing.Verbose() {
		t.Logf("repo %q\nsigrok %q", orcNotes(ps), orcTexts(anns))
	}
	repo := orcMatch(orcNotes(ps), `^CFP 0\.1 read (NVR1|NVR2|Module VR1|Vendor NVR1) 0x[0-9A-F]{4} = 0x[0-9A-F]{4}(?: \((CFP)\))?$`)
	regions := map[string]string{"CFP NVR 1": "NVR1", "CFP NVR 2": "NVR2", "CFP Module VR 1": "Module VR1", "Vendor NVR 1": "Vendor NVR1"}
	var orc []string
	ts := orcTexts(anns)
	for i, s := range ts {
		k := strings.Index(s, ":")
		if k < 0 || regions[s[:k]] == "" {
			continue
		}
		mod := ""
		if i+1 < len(ts) && ts[i+1] == "Module identifier: CFP" {
			mod = "CFP"
		}
		orc = append(orc, regions[s[:k]]+"|"+mod)
	}
	orcEq(t, "CFP register reads", repo, orc)
}

// TRLC-LINKS: REQ-SDS-246
func TestOracleLayerUSBRequest(t *testing.T) {
	orcNeed(t, "usb_signalling", "usb_packet", "usb_request")
	const sr = 120_000_000
	setup := []byte{0x80, 0x06, 0x00, 0x01, 0x00, 0x00, 0x12, 0x00}
	desc := []byte{18, 1, 0x00, 0x02, 0, 0, 0, 64, 0x34, 0x12, 0x78, 0x56, 0, 1, 1, 2, 3, 1}
	setAddr := []byte{0x00, 0x05, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00}
	pk := [][]int{
		orcUSBPacket(0xd, orcUSBToken(0, 0)), orcUSBPacket(0x3, orcUSBData(setup)), orcUSBPacket(0x2, nil),
		orcUSBPacket(0x9, orcUSBToken(0, 0)), orcUSBPacket(0xb, orcUSBData(desc)), orcUSBPacket(0x2, nil),
		orcUSBPacket(0x1, orcUSBToken(0, 0)), orcUSBPacket(0xb, orcUSBData(nil)), orcUSBPacket(0x2, nil),
		orcUSBPacket(0xd, orcUSBToken(0, 0)), orcUSBPacket(0x3, orcUSBData(setAddr)), orcUSBPacket(0x2, nil),
		orcUSBPacket(0x9, orcUSBToken(0, 0)), orcUSBPacket(0xb, orcUSBData(nil)), orcUSBPacket(0x2, nil),
	}
	dp, dm := orcUSBWaves(sr, pk, 12)
	ps := orcUSBLayer(t, sr, dp, "usb_request")
	anns := orcDecode(t, sr, []string{"DP", "DM"}, [][]byte{dp, dm},
		"usb_signalling:dp=DP:dm=DM:signalling=full-speed,usb_packet:signalling=full-speed,usb_request", "usb_request")
	if testing.Verbose() {
		t.Logf("repo %q\nsigrok %q", orcNotes(ps), orcTexts(anns))
	}
	// the control transfers: the request the SETUP stage names (written from
	// sigrok's setup bytes with the USB 2.0 chapter 9 names), the device's
	// IDs from the descriptor it returned, the handshake that ends them
	var repo []string
	for _, n := range orcNotes(ps) {
		switch {
		case strings.HasPrefix(n, "GET_DESCRIPTOR") || strings.HasPrefix(n, "SET_ADDRESS"):
			repo = append(repo, n)
		case strings.HasPrefix(n, "Device USB"):
			repo = append(repo, n)
		}
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		m := regexp.MustCompile(`^SETUP (?:in|out): \[ ([0-9A-F ]+) \]\[ ?([0-9A-F ]*) ?\] : ACK$`).FindStringSubmatch(s)
		if m == nil {
			continue
		}
		var su []int
		for _, f := range strings.Fields(m[1]) {
			v, _ := strconv.ParseUint(f, 16, 8)
			su = append(su, int(v))
		}
		val, n := su[2]|su[3]<<8, su[6]|su[7]<<8
		switch su[1] {
		case 6:
			orc = append(orc, fmt.Sprintf("GET_DESCRIPTOR %s len %d", map[int]string{1: "Device", 2: "Configuration"}[val>>8], n))
		case 5:
			orc = append(orc, fmt.Sprintf("SET_ADDRESS %d", val))
		}
		if d := strings.Fields(m[2]); len(d) >= 12 && d[1] == "01" {
			orc = append(orc, fmt.Sprintf("Device USB %s.%s class %s VID %s%s PID %s%s", strings.TrimLeft(d[3], "0"), d[2], d[4], d[9], d[8], d[11], d[10]))
		}
	}
	orcEq(t, "USB control transfers", repo, orc)
}
