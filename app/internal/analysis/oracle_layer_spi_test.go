// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// Oracle cross-checks of the SPI layers (oracle_layer_helpers_test.go): each
// transaction a burst on the data line (MOSI unless the layer reads MISO),
// sigrok given the chip select and the other line too. Each test compares
// what both name: the command, the register, the value written (the values
// read come back on the line the scope does not hold).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// orcSPIRun decodes txns with the layer (reading MOSI, or MISO with miso)
// and with sigrok's stack, logging both under -v.
// TRLC-LINKS: REQ-SDS-242
func orcSPIRun(t *testing.T, layer, pd, ann string, txns []orcSPITxn, miso bool) ([]Packet, []orcAnn) {
	t.Helper()
	const sr, rate = 2_000_000, 100_000
	clk, mo, mi, cs := orcSPIWaves(sr, rate, txns, 12)
	data := mo
	if miso {
		data = mi
	}
	ps := orcSPILayer(t, sr, clk, data, layer)
	anns := orcSPI(t, sr, clk, mo, mi, cs, pd, ann)
	if testing.Verbose() {
		t.Logf("repo %q\nsigrok %q", orcNotes(ps), orcTexts(anns))
	}
	return ps, anns
}

// orcMatch keeps the texts matching re, as their submatches joined by |.
// TRLC-LINKS: REQ-SDS-242
func orcMatch(texts []string, re string) []string {
	r := regexp.MustCompile(re)
	var out []string
	for _, s := range texts {
		if m := r.FindStringSubmatch(s); m != nil {
			out = append(out, strings.Join(m[1:], "|"))
		}
	}
	return out
}

// orcHex rewrites a hex number ("0x1", "1") as upper case without leading
// zeros, as both sides' values compare.
// TRLC-LINKS: REQ-SDS-242
func orcHex(s string) string {
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 64)
	if err != nil {
		return "?" + s
	}
	return fmt.Sprintf("%X", v)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerSPIFlash(t *testing.T) {
	orcNeed(t, "spi", "spiflash")
	ps, anns := orcSPIRun(t, "spiflash", "spiflash", "spiflash=commands", []orcSPITxn{
		{mosi: []byte{0x06}},
		{mosi: []byte{0x02, 0x00, 0x01, 0x00, 0xde, 0xad, 0xbe, 0xef}},
		{mosi: []byte{0x05, 0}, miso: []byte{0, 0x02}},
		{mosi: []byte{0x03, 0x00, 0x10, 0x00, 0, 0, 0, 0}, miso: []byte{0, 0, 0, 0, 1, 2, 3, 4}},
		{mosi: []byte{0x0b, 0x01, 0x23, 0x45, 0x00, 0, 0}, miso: []byte{0, 0, 0, 0, 0, 9, 8}},
		{mosi: []byte{0x06}},
		{mosi: []byte{0x20, 0x01, 0x00, 0x00}},
		{mosi: []byte{0x04}},
	}, false)
	// the command, the address and the bytes after it
	repo := orcMatch(orcNotes(ps), `^(\S+?)(?: 4K)?(?: 0x([0-9A-F]{6}))?(?:, (\d+) bytes?)?(?:: |$)`)
	names := map[string]string{"Write enable": "WREN", "Page program": "PP", "Read status register": "RDSR",
		"Read data": "READ", "Fast read data": "FAST_READ", "Write disable": "WRDI"}
	var orc []string
	for _, a := range anns {
		if m := regexp.MustCompile(`^Erase sector \d+ \(0x([0-9a-f]{6})\)`).FindStringSubmatch(a.Text); m != nil {
			orc = append(orc, "SE|"+strings.ToUpper(m[1])+"|")
			continue
		}
		m := regexp.MustCompile(`^(?:Command: )?([A-Za-z ]+?)(?: \(([A-Z/_]+)\)| \(addr 0x([0-9a-f]{6}), (\d+) bytes\))`).FindStringSubmatch(a.Text)
		if m == nil || names[m[1]] == "" {
			t.Fatalf("sigrok annotation %q", a.Text)
		}
		n := m[4]
		if names[m[1]] == "RDSR" {
			n = "1" // the scope counts the status byte the host clocked
		}
		orc = append(orc, names[m[1]]+"|"+strings.ToUpper(m[3])+"|"+n)
	}
	orcEq(t, "SPI flash commands", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerSDCard(t *testing.T) {
	orcNeed(t, "spi", "sdcard_spi")
	tok := func(cmd int, arg uint32) []byte {
		b := []byte{0x40 | byte(cmd), byte(arg >> 24), byte(arg >> 16), byte(arg >> 8), byte(arg)}
		return append(b, byte(sdCRC7(b)<<1|1))
	}
	// each command, then the host clocking 0xFF while the card answers R1 0x00
	cmd := func(c int, arg uint32) orcSPITxn {
		return orcSPITxn{mosi: append(tok(c, arg), 0xff, 0xff), miso: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}}
	}
	ps, anns := orcSPIRun(t, "sdcard", "sdcard_spi", "sdcard_spi",
		[]orcSPITxn{cmd(0, 0), cmd(55, 0), cmd(41, 0x40000000), cmd(16, 512), cmd(59, 1), cmd(17, 0x1000)}, false)
	repo := orcMatch(orcNotes(ps), `^(A?CMD\d+) (\S+)(?: 0x([0-9A-F]{8}))?$`)
	for i, r := range repo {
		f := strings.Split(r, "|")
		if f[2] != "" {
			f[2] = orcHex(f[2])
		}
		repo[i] = strings.Join(f, "|")
	}
	argv := orcMatch(orcTexts(anns), `^Argument: 0x([0-9a-f]+)$`)
	var orc []string
	for k, s := range orcMatch(orcTexts(anns), `^(A?CMD\d+) \(([A-Z_]+)\):`) {
		a := ""
		if k < len(argv) && orcHex(argv[k]) != "0" {
			a = orcHex(argv[k])
		}
		orc = append(orc, s+"|"+a)
	}
	orcEq(t, "SD commands", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerNRF24L01(t *testing.T) {
	orcNeed(t, "spi", "nrf24l01")
	ps, anns := orcSPIRun(t, "nrf24l01", "nrf24l01", "nrf24l01", []orcSPITxn{
		{mosi: []byte{0x20, 0x0e}}, {mosi: []byte{0x25, 0x4c}}, {mosi: []byte{0x30, 0xe7, 0xe6, 0xe5, 0xe4, 0xe3}},
		{mosi: []byte{0xa0, 'h', 'e', 'y'}}, {mosi: []byte{0xe1}}, {mosi: []byte{0xe2}}, {mosi: []byte{0x27, 0x70}},
	}, false)
	var repo []string
	for _, n := range orcNotes(ps) {
		if m := regexp.MustCompile(`^W_TX_PAYLOAD \d+ bytes? '(.*)'$`).FindStringSubmatch(n); m != nil {
			n = "W_TX_PAYLOAD " + m[1]
		}
		repo = append(repo, strings.ReplaceAll(n, " = ", "="))
	}
	for i := range repo { // a register's bytes run together on sigrok's side
		if k := strings.Index(repo[i], "="); k >= 0 {
			repo[i] = repo[i][:k] + strings.ReplaceAll(repo[i][k:], " ", "")
		}
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		switch {
		case strings.HasPrefix(s, "Cmd W_REGISTER: "):
			orc = append(orc, "W_REGISTER "+strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(s, "Cmd W_REGISTER: "), `"`, ""), " = ", "="))
		case s == "Cmd W_TX_PAYLOAD":
		case strings.HasPrefix(s, "TX payload = "):
			orc = append(orc, "W_TX_PAYLOAD "+strings.Trim(strings.TrimPrefix(s, "TX payload = "), `"`))
		case strings.HasPrefix(s, "Cmd "):
			orc = append(orc, strings.TrimPrefix(s, "Cmd "))
		}
	}
	orcEq(t, "nRF24L01 commands", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerNRF905(t *testing.T) {
	orcNeed(t, "spi", "nrf905")
	// sigrok's CHANNEL_CONFIG handler fails on a tuple, so the commands
	// cross-checked are the rest
	ps, anns := orcSPIRun(t, "nrf905", "nrf905", "nrf905=cmd", []orcSPITxn{
		{mosi: []byte{0x00, 0x6c, 0x0c}}, {mosi: []byte{0x22, 0xe7, 0xe7, 0xe7, 0xe7}}, {mosi: []byte{0x20, 1, 2, 3}},
		{mosi: []byte{0x10, 0, 0}}, {mosi: []byte{0x24, 0, 0}}, {mosi: []byte{0x23, 0}},
	}, false)
	repo := orcMatch(orcNotes(ps), `^([A-Z_]+)`)
	orc := orcMatch(orcTexts(anns), `^CMD: ([A-Z_]+) `)
	orcEq(t, "nRF905 commands", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerCC1101(t *testing.T) {
	orcNeed(t, "spi", "cc1101")
	ps, anns := orcSPIRun(t, "cc1101", "cc1101", "cc1101", []orcSPITxn{
		{mosi: []byte{0x30}}, {mosi: []byte{0x0d, 0x10}}, {mosi: []byte{0x7e, 0xc0, 0xc8}}, {mosi: []byte{0x80, 0}},
		{mosi: []byte{0xf5, 0}}, {mosi: []byte{0x34}}, {mosi: []byte{0x46, 1, 2, 3}},
	}, false)
	// the operation, the register and the bytes written
	repo := orcMatch(orcNotes(ps), `^((?:BURST |STATUS )?[A-Z]+) ([A-Z0-9_]+)(?: = ([0-9A-F ]+))?`)
	var orc []string
	for _, s := range orcMatch(orcTexts(anns), `^(Strobe|Write|Burst write|Read|Burst read|Status read):? ([A-Z0-9_]+)(?: \([0-9A-F]+\))?(?: = ([0-9A-F ]+))?$`) {
		f := strings.Split(s, "|")
		f[0] = strings.ToUpper(f[0])
		if f[0] == "READ" || f[0] == "STATUS READ" || f[0] == "BURST READ" {
			f[2] = "" // the value read comes on MISO
		}
		orc = append(orc, strings.Join(f, "|"))
	}
	orcEq(t, "CC1101 accesses", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerRFM12(t *testing.T) {
	orcNeed(t, "spi", "rfm12")
	// sigrok's frequency setting handler reads a third byte the command
	// does not have, so it is left out
	ps, anns := orcSPIRun(t, "rfm12", "rfm12", "rfm12=commands", []orcSPITxn{
		{mosi: []byte{0x80, 0xd7}}, {mosi: []byte{0x82, 0x39}}, {mosi: []byte{0xc6, 0x23}}, {mosi: []byte{0x94, 0xa2}},
		{mosi: []byte{0xca, 0x81}}, {mosi: []byte{0xb8, 0xaa}}, {mosi: []byte{0x00, 0x00}}, {mosi: []byte{0xc2, 0xac}},
		{mosi: []byte{0xcc, 0x77}}, {mosi: []byte{0xfe, 0x00}}, {mosi: []byte{0xc0, 0xe0}},
	}, false)
	names := map[string]string{"Configuration command": "CONFIG", "Power management": "POWER", "Data rate command": "DATA RATE",
		"Receiver control command": "RX CTRL", "FIFO and reset command": "FIFO RESET", "Transmitter register command": "TX BYTE",
		"Status read command": "STATUS READ", "Data filter command": "DATA FILTER", "PLL setting command": "PLL",
		"Software reset command": "RESET", "Low battery detector command": "LOW BATTERY"}
	repo := orcMatch(orcNotes(ps), `^(CONFIG|POWER|DATA RATE|RX CTRL|FIFO RESET|TX BYTE|STATUS READ|DATA FILTER|PLL|RESET|LOW BATTERY)\b`)
	var orc []string
	for _, s := range orcTexts(anns) {
		if n, ok := names[s]; ok {
			orc = append(orc, n)
		}
	}
	orcEq(t, "RFM12 commands", repo, orc)
	// and the values both read: the data rate and the low battery level
	var rate []string
	for _, s := range orcMatch(orcNotes(ps), `^DATA RATE (\d+\.\d+)kbps`) {
		v, _ := strconv.ParseFloat(s, 64)
		rate = append(rate, fmt.Sprintf("%.1f", v))
	}
	orate := orcMatch(orcTexts(anns), `^(\d+\.\d)kbps$`)
	orcEq(t, "RFM12 data rate", rate, orate)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerMRF24J40(t *testing.T) {
	orcNeed(t, "spi", "mrf24j40")
	ps, anns := orcSPIRun(t, "mrf24j40", "mrf24j40", "mrf24j40", []orcSPITxn{
		{mosi: []byte{0x01, 0x01}}, {mosi: []byte{0x02, 0}}, {mosi: []byte{0x80, 0x10, 0x41}}, {mosi: []byte{0xc0, 0x10, 0x07}},
		{mosi: []byte{0x37, 0x05}}, {mosi: []byte{0xe0, 0x10, 0x55}},
	}, false)
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^([WR]) (.+?)(?: = ([0-9A-F]+)|, 1 byte)$`) {
		f := strings.Split(s, "|")
		name := regexp.MustCompile(`^TX\[0x([0-9A-F]+)\]$`).ReplaceAllStringFunc(f[1], func(x string) string {
			return "TX:0x" + strings.ToLower(orcHex(x[5:len(x)-1]))
		})
		name = regexp.MustCompile(`^RX\[0x([0-9A-F]+)\]$`).ReplaceAllStringFunc(name, func(x string) string {
			v, _ := strconv.ParseUint(x[5:len(x)-1], 16, 16)
			return fmt.Sprintf("RX:%#x", 0x300+v) // sigrok writes the RX FIFO's register addresses
		})
		v := ""
		if f[0] == "W" {
			v = orcHex(f[2])
		}
		repo = append(repo, name+"|"+v)
	}
	var orc []string
	wr := map[int]bool{0: true, 2: true, 3: true, 4: true, 5: true} // the transactions that write
	for k, s := range orcMatch(orcTexts(anns), `^(.+): (0x[0-9a-f]+)$`) {
		f := strings.Split(s, "|")
		v := ""
		if wr[k] {
			v = orcHex(f[1])
		}
		orc = append(orc, f[0]+"|"+v)
	}
	orcEq(t, "MRF24J40 accesses", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerENC28J60(t *testing.T) {
	orcNeed(t, "spi", "enc28j60")
	ps, anns := orcSPIRun(t, "enc28j60", "enc28j60", "enc28j60", []orcSPITxn{
		{mosi: []byte{0x5f, 0x02}}, {mosi: []byte{0x14, 0, 0}}, {mosi: []byte{0x9f, 0x01}}, {mosi: []byte{0x02, 0, 0}},
		{mosi: []byte{0xbf, 0x03}}, {mosi: []byte{0x08, 0}}, {mosi: []byte{0x7a, 1, 2}}, {mosi: []byte{0x3a, 0, 0}}, {mosi: []byte{0xff}},
	}, false)
	ops := map[string]string{"Write Control Register": "WCR", "Read Control Register": "RCR", "Bit Field Set": "BFS",
		"Bit Field Clear": "BFC", "Write Buffer Memory": "WBM", "Read Buffer Memory": "RBM", "System Reset Command": "SRC"}
	repo := orcMatch(orcNotes(ps), `^(WCR|RCR|BFS|BFC|WBM|RBM|SRC)(?: ([A-Z][A-Z0-9]+))?`)
	var orc []string
	ts := orcTexts(anns)
	for i, s := range ts {
		op := ""
		for k, v := range ops {
			if strings.HasPrefix(s, k) {
				op = v
			}
		}
		if op == "" {
			continue
		}
		reg := ""
		if i+1 < len(ts) && strings.HasPrefix(ts[i+1], "Reg ") {
			reg = strings.TrimPrefix(ts[i+1], "Reg ")
			if reg == "Bank ? Addr 0x1F" {
				reg = "ECON1" // the bank is not known yet, but ECON1 is in every bank
			}
		}
		orc = append(orc, op+"|"+reg)
	}
	orcEq(t, "ENC28J60 opcodes", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerST25R39xx(t *testing.T) {
	orcNeed(t, "spi", "st25r39xx_spi")
	ps, anns := orcSPIRun(t, "st25r39xx", "st25r39xx_spi", "st25r39xx_spi", []orcSPITxn{
		{mosi: []byte{0x02, 0x80}}, {mosi: []byte{0x42, 0}}, {mosi: []byte{0xc1}}, {mosi: []byte{0x80, 1, 2}},
		{mosi: []byte{0x9f, 0, 0}}, {mosi: []byte{0xd5}}, {mosi: []byte{0x00, 0x0f}},
	}, false)
	var repo []string
	for _, n := range orcNotes(ps) {
		n = strings.Replace(n, "WRITE ", "Write ", 1)
		n = strings.Replace(n, "READ ", "Read ", 1)
		n = regexp.MustCompile(`, \d+ bytes?$`).ReplaceAllString(n, "")
		n = strings.Replace(n, "FIFO LOAD = ", "FIFOW ", 1)
		n = strings.Replace(n, "FIFO READ", "FIFOR", 1)
		repo = append(repo, n)
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		switch {
		case strings.HasPrefix(s, "Write: ") || strings.HasPrefix(s, "Read: "):
			m := regexp.MustCompile(`^(Write|Read): ([A-Z0-9_]+) \([0-9A-F]+\)(?: = ([0-9A-F ]+))?`).FindStringSubmatch(s)
			v := ""
			if m[1] == "Write" {
				v = " = " + m[3]
			}
			orc = append(orc, m[1]+" "+m[2]+v)
		case strings.HasPrefix(s, "Cmd "):
			orc = append(orc, "CMD "+strings.TrimPrefix(s, "Cmd "))
		case strings.HasPrefix(s, "FIFOW: "):
			orc = append(orc, "FIFOW "+strings.TrimPrefix(s, "FIFOW: "))
		case strings.HasPrefix(s, "FIFOR: "):
			orc = append(orc, "FIFOR")
		}
	}
	orcEq(t, "ST25R39xx accesses", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerADXL345(t *testing.T) {
	orcNeed(t, "spi", "adxl345")
	ps, anns := orcSPIRun(t, "adxl345", "adxl345", "adxl345", []orcSPITxn{
		{mosi: []byte{0x2d, 0x08}}, {mosi: []byte{0x2c, 0x0a}}, {mosi: []byte{0x31, 0x0b}}, {mosi: []byte{0x1e, 0x05}},
		{mosi: []byte{0x80, 0}, miso: []byte{0, 0xe5}},
	}, false)
	repo := orcMatch(orcNotes(ps), `^([WR]) ([A-Za-z0-9_]+)`)
	var orc []string
	ts := orcTexts(anns)
	for i := 0; i+3 < len(ts); i++ {
		if ts[i] == "WRITE REG" || ts[i] == "READ REG" {
			name := ts[i+3]
			if name == "0" {
				name = "DEVID" // sigrok writes the registers below 0x1D by their number
			}
			orc = append(orc, ts[i][:1]+"|"+name)
		}
	}
	orcEq(t, "ADXL345 accesses", repo, orc)
	// the rate and range the writes set
	orcEq(t, "ADXL345 values", orcMatch(orcNotes(ps), `\((100) Hz\)|\(\+-(16)g\)`), orcMatch(ts, `^(100)\.000000$|^\+/-(16) g$`))
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerADNS5020(t *testing.T) {
	orcNeed(t, "spi", "adns5020")
	ps, anns := orcSPIRun(t, "adns5020", "adns5020", "adns5020", []orcSPITxn{
		{mosi: []byte{0x02, 0}, miso: []byte{0, 0x80}}, {mosi: []byte{0x8d, 0x01}}, {mosi: []byte{0x00, 0}, miso: []byte{0, 0x12}},
		{mosi: []byte{0xba, 0x5a}},
	}, false)
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^([WR]) ([A-Za-z_]+)(?: = ([0-9A-F]+))?$`) {
		f := strings.Split(s, "|")
		if f[2] != "" {
			f[2] = orcHex(f[2])
		}
		repo = append(repo, f[1]+"|"+f[2])
	}
	var orc []string
	for k, s := range orcMatch(orcTexts(anns), `^([A-Za-z_]+): (\S+)$`) {
		f := strings.Split(s, "|")
		if k == 1 || k == 3 { // the writes: the value the host sent
			f[1] = orcHex(f[1])
		} else {
			f[1] = ""
		}
		orc = append(orc, strings.Join(f, "|"))
	}
	orcEq(t, "ADNS-5020 accesses", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerADE7758(t *testing.T) {
	orcNeed(t, "spi", "ade77xx")
	ps, anns := orcSPIRun(t, "ade77xx", "ade77xx", "ade77xx", []orcSPITxn{
		{mosi: []byte{0x93, 0x04}}, {mosi: []byte{0x9b, 0x12, 0x34}}, {mosi: []byte{0x0d, 0, 0, 0}, miso: []byte{0, 1, 2, 3}},
		{mosi: []byte{0x98, 0x01, 0x02, 0x03}},
	}, false)
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^([WR]) ([A-Za-z0-9_]+)(?: = 0x([0-9A-F]+)|, \d bytes?)$`) {
		f := strings.Split(s, "|")
		if f[2] != "" {
			f[2] = orcHex(f[2])
		}
		repo = append(repo, f[1]+"|"+f[2])
	}
	var orc []string
	for k, s := range orcMatch(orcTexts(anns), `^([A-Za-z0-9_]+): (0x[0-9a-f]+)$`) {
		f := strings.Split(s, "|")
		f[1] = orcHex(f[1])
		if k == 2 {
			f[1] = "" // a read: its value is MISO's
		}
		orc = append(orc, strings.Join(f, "|"))
	}
	orcEq(t, "ADE7758 accesses", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerADF435x(t *testing.T) {
	orcNeed(t, "spi", "adf435x")
	ps, anns := orcSPIRun(t, "adf435x", "adf435x", "adf435x", []orcSPITxn{
		{mosi: []byte{0x00, 0x58, 0x00, 0x05}}, {mosi: []byte{0x00, 0x8c, 0x80, 0x3c}}, {mosi: []byte{0x00, 0x00, 0x04, 0xb3}},
		{mosi: []byte{0x00, 0x00, 0x4e, 0x42}}, {mosi: []byte{0x08, 0x00, 0x80, 0x29}}, {mosi: []byte{0x00, 0x40, 0x00, 0x00}},
		{mosi: []byte{0x00, 0x58, 0x00, 0x05}}, // 0.5's decoder writes a word out at the next one
	}, false)
	var repo []string
	for _, n := range orcNotes(ps) {
		var f []string
		for _, m := range regexp.MustCompile(`^R(\d)|INT (\d+)|FRAC (\d+)|MOD (\d+)|phase (\d+)|prescaler (\S+)|clock divider (\d+)|R (\d+) CP|power ([-+]?\d+)dBm`).FindAllStringSubmatch(n, -1) {
			for _, g := range m[1:] {
				if g != "" {
					f = append(f, g)
				}
			}
		}
		repo = append(repo, strings.Join(f, " "))
	}
	var orc []string
	cur := ""
	for _, s := range orcTexts(anns) {
		if m := regexp.MustCompile(`^Register: (\d)$`).FindStringSubmatch(s); m != nil {
			if cur != "" {
				orc = append(orc, cur)
			}
			cur = m[1]
			continue
		}
		for _, re := range []string{`^INT: (\d+)$`, `^FRAC: (\d+)$`, `^MOD: (\d+)$`, `^Phase: (\d+)$`, `^Prescalar: (\S+)$`,
			`^Clock Divider: (\d+)$`, `^R Counter: (\d+)$`, `^Output Power: \+?([-+]?\d+)dBm$`} {
			if m := regexp.MustCompile(re).FindStringSubmatch(s); m != nil {
				cur += " " + m[1]
			}
		}
	}
	orc = append(orc, cur)
	repo, orc = repo[:6], orc[:min(6, len(orc))]
	// sigrok lists a register's fields from bit 3 up (FRAC before INT);
	// the scope's note writes INT first: both sorted
	for i := range orc {
		repo[i] = orcSortFields(repo[i])
		orc[i] = orcSortFields(orc[i])
	}
	orcEq(t, "ADF435x registers", repo, orc)
}

// orcSortFields sorts a note's fields after its first.
// TRLC-LINKS: REQ-SDS-242
func orcSortFields(s string) string {
	f := strings.Fields(s)
	if len(f) < 2 {
		return s
	}
	rest := f[1:]
	for i := 1; i < len(rest); i++ {
		for j := i; j > 0 && rest[j] < rest[j-1]; j-- {
			rest[j], rest[j-1] = rest[j-1], rest[j]
		}
	}
	return f[0] + " " + strings.Join(rest, " ")
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerAD5626(t *testing.T) {
	orcNeed(t, "spi", "ad5626")
	ps, anns := orcSPIRun(t, "ad5626", "ad5626", "ad5626", []orcSPITxn{
		{mosi: []byte{0x07, 0xff}}, {mosi: []byte{0x00, 0x01}}, {mosi: []byte{0x08, 0x00}}, {mosi: []byte{0x0f, 0xff}},
	}, false)
	orcEq(t, "AD5626 output", orcMatch(orcNotes(ps), `^DAC (\d\.\d{3}) V`), orcMatch(orcTexts(anns), `^(\d\.\d{3})V$`))
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerAD79x0(t *testing.T) {
	orcNeed(t, "spi", "ad79x0")
	ps, anns := orcSPIRun(t, "ad79x0", "ad79x0", "ad79x0", []orcSPITxn{
		{mosi: []byte{0, 0}, miso: []byte{0x07, 0xff}}, {mosi: []byte{0, 0}, miso: []byte{0x00, 0x10}},
		{mosi: []byte{0, 0}, miso: []byte{0x0f, 0xfe}},
	}, true)
	// the code as volts at sigrok's default reference, 1.5 V
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^ADC (\d+)/4095`) {
		v, _ := strconv.Atoi(s)
		repo = append(repo, fmt.Sprintf("%.6f", float64(v)/4095*1.5))
	}
	orcEq(t, "AD79x0 conversions", repo, orcMatch(orcTexts(anns), `^(\d\.\d{6})V$`))
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerLTC242x(t *testing.T) {
	orcNeed(t, "spi", "ltc242x")
	// readings a whole share of the reference, which both write exactly
	ps, anns := orcSPIRun(t, "ltc242x", "ltc242x", "ltc242x", []orcSPITxn{
		{mosi: []byte{0, 0, 0}, miso: []byte{0x20, 0x00, 0x00}}, {mosi: []byte{0, 0, 0}, miso: []byte{0x2f, 0xff, 0xff}},
		{mosi: []byte{0, 0, 0}, miso: []byte{0x50, 0x00, 0x01}}, {mosi: []byte{0, 0, 0}, miso: []byte{0x6f, 0xff, 0xff}},
	}, true)
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^CH(\d) (-?\d\.\d{6}) Vref`) {
		f := strings.Split(s, "|")
		v, _ := strconv.ParseFloat(f[1], 64)
		repo = append(repo, fmt.Sprintf("%.6f", v*1.5))
	}
	orcEq(t, "LTC242x conversions", repo, orcMatch(orcTexts(anns), `^(-?\d\.\d{6})V$`))
	orcEq(t, "LTC242x channels", orcMatch(orcNotes(ps), `^CH(\d)`), []string{"0", "0", "1", "1"})
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerMAX72xx(t *testing.T) {
	orcNeed(t, "spi", "max72xx")
	ps, anns := orcSPIRun(t, "max72xx", "max72xx", "max72xx", []orcSPITxn{
		{mosi: []byte{0x0c, 0x01}}, {mosi: []byte{0x0a, 0x08}}, {mosi: []byte{0x01, 0x7e}}, {mosi: []byte{0x09, 0xff}},
		{mosi: []byte{0x0b, 0x07}}, {mosi: []byte{0x0f, 0x00}}, {mosi: []byte{0x05, 0x30}},
	}, false)
	var repo []string
	for _, n := range orcNotes(ps) {
		n = regexp.MustCompile(`^Decode ([0-9A-F]{2})$`).ReplaceAllStringFunc(n, func(s string) string {
			v, _ := strconv.ParseUint(s[7:], 16, 8)
			return fmt.Sprintf("Decode 0b%08b", v)
		})
		n = regexp.MustCompile(`^(Shutdown|Intensity|Scan limit|Display test|Decode) `).ReplaceAllString(n, "$1: ")
		n = strings.Replace(n, " = ", ": ", 1)
		repo = append(repo, n)
	}
	orcEq(t, "MAX72xx writes", repo, orcTexts(anns))
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerAVRISP(t *testing.T) {
	orcNeed(t, "spi", "avr_isp")
	txns := []orcSPITxn{
		{mosi: []byte{0xac, 0x53, 0, 0}, miso: []byte{0, 0xac, 0x53, 0}},
		{mosi: []byte{0x30, 0, 0, 0}, miso: []byte{0, 0x30, 0, 0x1e}},
		{mosi: []byte{0x30, 0, 1, 0}, miso: []byte{0, 0x30, 0, 0x93}},
		{mosi: []byte{0x30, 0, 2, 0}, miso: []byte{0, 0x30, 0, 0x0a}}, // an ATmega88, a part 0.5.3 knows
		{mosi: []byte{0x50, 0, 0, 0}, miso: []byte{0, 0x50, 0, 0x62}},
		{mosi: []byte{0x58, 8, 0, 0}, miso: []byte{0, 0x58, 8, 0xd9}},
		{mosi: []byte{0xac, 0x80, 0, 0}, miso: []byte{0, 0xac, 0x80, 0}},
	}
	if orcNewer() { // the memory instructions came after 0.5
		txns = append(txns, orcSPITxn{mosi: []byte{0xa0, 1, 0x23, 0}, miso: []byte{0, 0xa0, 1, 0x55}},
			orcSPITxn{mosi: []byte{0x28, 0x01, 0x23, 0}, miso: []byte{0, 0x28, 1, 0x0c}},
			orcSPITxn{mosi: []byte{0x40, 0, 5, 0xab}, miso: []byte{0, 0x40, 0, 0}})
	}
	ps, anns := orcSPIRun(t, "avrisp", "avr_isp", "avr_isp=commands", txns, false)
	names := map[string]string{"Programming enable": "PROGRAMMING ENABLE", "Vendor code": "READ SIGNATURE 0",
		"Part family / memory size": "READ SIGNATURE 1", "Part number": "READ SIGNATURE 2", "Read EEPROM Memory": "READ EEPROM",
		"Read fuse bits": "READ FUSE", "Read fuse high bits": "READ HIGH FUSE", "Read program memory High": "READ FLASH H",
		"Load program memory page Low": "LOAD PAGE L", "Chip erase": "CHIP ERASE"}
	repo := orcMatch(orcNotes(ps), `^(PROGRAMMING ENABLE|READ SIGNATURE \d|READ EEPROM|READ FUSE|READ HIGH FUSE|READ FLASH H|LOAD PAGE L|CHIP ERASE)`)
	var orc []string
	for _, s := range orcTexts(anns) {
		name := s
		if k := strings.Index(s, ":"); k >= 0 {
			name = s[:k]
		}
		if n, ok := names[name]; ok {
			orc = append(orc, n)
		}
	}
	orcEq(t, "AVR ISP instructions", repo, orc)
	// the addresses and the page data
	var addrs []string
	for _, s := range orcMatch(orcNotes(ps), `^(?:READ EEPROM|READ FLASH [HL]|LOAD PAGE [HL]) 0x([0-9A-F]+)(?: = ([0-9A-F]{2}))?$`) {
		f := strings.Split(s, "|")
		addrs = append(addrs, orcHex(f[0])+"|"+f[1])
	}
	var oaddrs []string
	for _, s := range orcMatch(orcTexts(anns), `^(Read EEPROM Memory|Read program memory High|Load program memory page Low): \[0x([0-9a-f]+)\]: 0x([0-9a-f]{2})$`) {
		f := strings.Split(s, "|")
		d := ""
		if strings.HasPrefix(f[0], "Load") {
			d = strings.ToUpper(f[2])
		}
		oaddrs = append(oaddrs, orcHex(f[1])+"|"+d)
	}
	if orcNewer() {
		orcEq(t, "AVR ISP addresses", addrs, oaddrs)
	}
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerX2444M(t *testing.T) {
	orcNeed(t, "spi", "x2444m")
	// the X2444's chip enable is active high
	const sr, rate = 2_000_000, 100_000
	txns := []orcSPITxn{{mosi: []byte{0x84}}, {mosi: []byte{0x9b, 0x12, 0x34}}, {mosi: []byte{0x81}}, {mosi: []byte{0x85}},
		{mosi: []byte{0xae, 0, 0}, miso: []byte{0, 0x56, 0x78}}, {mosi: []byte{0x80}}}
	clk, mo, mi, cs := orcSPIWaves(sr, rate, txns, 12)
	for i := range cs {
		cs[i] ^= 1
	}
	ps := orcSPILayer(t, sr, clk, mo, "x2444m")
	anns := orcDecode(t, sr, []string{"CLK", "MOSI", "MISO", "CS"}, [][]byte{clk, mo, mi, cs},
		"spi:clk=CLK:mosi=MOSI:miso=MISO:cs=CS:cs_polarity=active-high,x2444m", "x2444m")
	if testing.Verbose() {
		t.Logf("repo %q\nsigrok %q", orcNotes(ps), orcTexts(anns))
	}
	repo := orcMatch(orcNotes(ps), `^([A-Z]+)(?: 0x([0-9A-F]))?(?: = 0x([0-9A-F]{4}))?$`)
	var orc []string
	for _, s := range orcMatch(orcTexts(anns), `^([A-Z]+)(?:: 0x([0-9a-f]) => 0x([0-9a-f]{4}))?$`) {
		f := strings.Split(s, "|")
		f[1] = strings.ToUpper(f[1])
		f[2] = strings.ToUpper(f[2])
		if f[0] == "READ" {
			f[2] = "" // the word read comes on MISO
		}
		orc = append(orc, strings.Join(f, "|"))
	}
	orcEq(t, "X2444 instructions", repo, orc)
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerRGBLED(t *testing.T) {
	orcNeed(t, "spi", "rgb_led_spi")
	ps, anns := orcSPIRun(t, "rgbled", "rgb_led_spi", "rgb_led_spi", []orcSPITxn{
		{mosi: []byte{0xff, 0x80, 0x00}}, {mosi: []byte{0x00, 0x10, 0x20, 0x30, 0x40, 0x50}},
	}, false)
	var repo []string
	for _, n := range orcNotes(ps) {
		for _, m := range regexp.MustCompile(`#([0-9A-F]{6})`).FindAllStringSubmatch(n, -1) {
			repo = append(repo, strings.ToLower(m[1]))
		}
	}
	orcEq(t, "RGB values", repo, orcMatch(orcTexts(anns), `^#([0-9a-f]{6})$`))
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerNESGamepad(t *testing.T) {
	orcNeed(t, "spi", "nes_gamepad")
	ps, anns := orcSPIRun(t, "nesgamepad", "nes_gamepad", "nes_gamepad", []orcSPITxn{
		{mosi: []byte{0}, miso: []byte{0xff}}, {mosi: []byte{0}, miso: []byte{0x7e}}, {mosi: []byte{0}, miso: []byte{0xa5}},
		{mosi: []byte{0}, miso: []byte{0x00}},
	}, true)
	dirs := strings.NewReplacer("Up", "North", "Down", "South", "Left", "West", "Right", "East", "+", " + ",
		"no button", "No button is pressed", "not connected", "Gamepad is not connected")
	var repo []string
	for _, n := range orcNotes(ps) {
		repo = append(repo, dirs.Replace(n))
	}
	orcEq(t, "NES buttons", repo, orcTexts(anns))
}

// TRLC-LINKS: REQ-SDS-242
func TestOracleLayerSSI32(t *testing.T) {
	orcNeed(t, "spi", "ssi32")
	ctrl := make([]byte, 64)
	copy(ctrl, []byte{0x01, 0x02, 4, 0x55, 9, 8, 7, 6})
	// the device's answer has no data (sigrok reads its size off MISO)
	ps, anns := orcSPIRun(t, "ssi32", "ssi32", "ssi32=tx", []orcSPITxn{{mosi: []byte{0x80, 0, 0, 0}},
		{mosi: ctrl, miso: []byte{0x01, 0x00, 0x00, 0x00}}}, false)
	var repo []string
	for _, n := range orcNotes(ps) {
		n = regexp.MustCompile(`^ACK 0x(..)$`).ReplaceAllString(n, "> ACK:0x$1")
		n = regexp.MustCompile(`^CTRL 0x(..) LUN 0x(..) SIZE (\d+) CRC 0x(..): (.*)$`).ReplaceAllStringFunc(n, func(s string) string {
			m := regexp.MustCompile(`^CTRL 0x(..) LUN 0x(..) SIZE (\d+) CRC 0x(..): (.*)$`).FindStringSubmatch(s)
			size, _ := strconv.Atoi(m[3])
			return fmt.Sprintf("> CTRL:0x%s, LUN:0x%s, SIZE:0x%02X, CRC:0x%s, DATA:0x%s", m[1], m[2], size, m[4], strings.ReplaceAll(m[5], " ", ""))
		})
		repo = append(repo, strings.ToLower(n))
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		orc = append(orc, strings.ToLower(s))
	}
	orcEq(t, "SSI32 messages", repo, orc)
}
