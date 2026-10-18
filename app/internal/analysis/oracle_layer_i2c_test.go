// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// Oracle cross-checks of the I2C layers (oracle_layer_helpers_test.go).
// sigrok's mlx90614 reads a temperature only from a write after a repeated
// START (its state machine's shortcut), never from the chip's SMBus word
// read, so the MLX90614 layer is held to the datasheet by its own tests;
// i2cdemux has no annotations of its own to compare.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// orcI2CRun decodes the transfers with the layer and with sigrok's stack.
// TRLC-LINKS: REQ-SDS-243
func orcI2CRun(t *testing.T, layer, pd, ann string, txns []orcI2CTxn) ([]Packet, []orcAnn) {
	t.Helper()
	const sr, f = 2_000_000, 100_000
	scl, sda := orcI2CWaves(sr, f, txns)
	ps := orcI2CLayer(t, sr, scl, sda, layer)
	anns := orcI2C(t, sr, scl, sda, pd, ann)
	if testing.Verbose() {
		t.Logf("repo %q\nsigrok %q", orcNotes(ps), orcTexts(anns))
	}
	return ps, anns
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerEDID(t *testing.T) {
	orcNeed(t, "i2c", "edid")
	edid := edidBlock()
	ps, anns := orcI2CRun(t, "edid", "edid", "edid", []orcI2CTxn{{addr: 0x50, data: []byte{0}, noStop: true},
		{addr: 0x50, read: true, data: edid}})
	repo := orcMatch(orcNotes(ps), `^EDID ([A-Z]{3}) ([0-9A-F]{4}) '(.*)' (\d+)x(\d+)@\d+Hz v(\d)\.(\d) checksum (ok)$`)
	ts := strings.Join(orcTexts(anns), "\n")
	get := func(re string) string {
		m := regexp.MustCompile(re).FindStringSubmatch(ts)
		if m == nil {
			t.Fatalf("sigrok said no %s", re)
		}
		return m[1]
	}
	orc := []string{strings.Join([]string{get(`(?m)^([A-Z]{3})(?: \(|$)`), strings.ToUpper(get(`(?m)^Product 0x([0-9a-f]{4})$`)),
		get(`(?m)^Monitor name\n([^\n]+)$`), get(`(?m)^Horizontal active: (\d+)`), get(`(?m)^Vertical active: (\d+)`),
		get(`(?m)^Version (\d+)$`), get(`(?m)^Revision (\d+)$`), strings.ToLower(get(`(?m)^Checksum: \d+ \((OK)\)$`))}, "|")}
	orcEq(t, "EDID", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerXFP(t *testing.T) {
	orcNeed(t, "i2c", "xfp")
	m := make([]byte, 256)
	m[0], m[127], m[128] = 6, 1, 6
	m[96], m[97] = 0x23, 0x50 // 35.3125 C
	m[102], m[103] = 0x13, 0x88
	m[104], m[105] = 0x0f, 0xa0
	copy(m[148:164], "ACME OPTICS     ")
	copy(m[168:184], "XFP-10G-SR      ")
	m[186], m[187] = 0x66, 0x58
	copy(m[196:212], "SN0001          ")
	copy(m[212:220], "26101800") // the manufacture date code
	ps, anns := orcI2CRun(t, "xfp", "xfp", "xfp", []orcI2CTxn{{addr: 0x50, data: []byte{0}, noStop: true},
		{addr: 0x50, read: true, data: m}})
	repo := orcMatch(orcNotes(ps), `^XFP (XFP) (\d+\.\d) C .* TX (\d\.\d\d)\d mW RX (\d\.\d\d)\d mW XFP '([^']*)' PN '([^']*)' (\d+)\.\d\d nm SN '([^']*)'$`)
	ts := strings.Join(orcTexts(anns), "\n")
	get := func(re string) string {
		r := regexp.MustCompile(re).FindStringSubmatch(ts)
		if r == nil {
			t.Fatalf("sigrok said no %s", re)
		}
		return r[1]
	}
	orc := []string{strings.Join([]string{get(`(?m)^Module identifier: (XFP)$`), get(`(?m)^Module temperature: (\d+\.\d) C$`),
		get(`(?m)^Measured TX output power: (\d\.\d\d) mW$`), get(`(?m)^Measured RX input power: (\d\.\d\d) mW$`),
		get(`(?m)^Vendor: (.+)$`), get(`(?m)^Vendor part number: (.+)$`), get(`(?m)^Wavelength: (\d+) nm$`),
		get(`(?m)^Vendor serial number: (.+)$`)}, "|")}
	orcEq(t, "XFP fields", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerEEPROM24xx(t *testing.T) {
	orcNeed(t, "i2c", "eeprom24xx")
	ps, anns := orcI2CRun(t, "eeprom24xx", "eeprom24xx", "eeprom24xx", []orcI2CTxn{
		{addr: 0x50, data: []byte{0x10, 0xab}}, {addr: 0x50, data: []byte{0x20, 1, 2, 3}},
		{addr: 0x50, data: []byte{0x10}, noStop: true}, {addr: 0x50, read: true, data: []byte{0xab}},
		{addr: 0x50, data: []byte{0x20}, noStop: true}, {addr: 0x50, read: true, data: []byte{1, 2, 3}},
		{addr: 0x50, read: true, data: []byte{4}}})
	// the operation, its word address and its bytes
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^EEPROM 50 (byte write|page write|read) @0x([0-9A-F]+)(?:, \d+ bytes?)?: ([0-9A-F ]+)$`) {
		f := strings.Split(s, "|")
		repo = append(repo, f[0]+"|"+orcHex(f[1])+"|"+f[2])
	}
	var orc []string
	for _, s := range orcMatch(orcTexts(anns), `^(Byte write|Page write|Random access read|Sequential random read) \(addr=([0-9A-F]+), \d+ bytes?\): ([0-9A-F ]+)$`) {
		f := strings.Split(s, "|")
		op := map[string]string{"Byte write": "byte write", "Page write": "page write", "Random access read": "read",
			"Sequential random read": "read"}[f[0]]
		orc = append(orc, op+"|"+orcHex(f[1])+"|"+f[2])
	}
	// the current address read: its byte and the address both count on to
	cur := orcMatch(orcTexts(anns), `^Data byte ([0-9A-F]+): ([0-9A-F]{2})$`)
	orc = append(orc, "read|"+orcHex(strings.Split(cur[len(cur)-1], "|")[0])+"|"+strings.Split(cur[len(cur)-1], "|")[1])
	orcEq(t, "EEPROM operations", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerDS1307(t *testing.T) {
	orcNeed(t, "i2c", "ds1307")
	ps, anns := orcI2CRun(t, "ds1307", "ds1307", "ds1307", []orcI2CTxn{
		{addr: 0x68, data: []byte{0}, noStop: true}, {addr: 0x68, read: true, data: []byte{0x56, 0x34, 0x12, 0x07, 0x18, 0x10, 0x26}},
		{addr: 0x68, data: []byte{0, 0x00, 0x45, 0x23, 0x02, 0x31, 0x12, 0x99}}})
	repo := orcMatch(orcNotes(ps), `^RTC (read|write) (\d{4})-(\d\d)-(\d\d) (\d\d:\d\d:\d\d) ([A-Z][a-z]{2})$`)
	var orc []string
	for _, s := range orcMatch(orcTexts(anns), `^(Read|Written) date/time: ([A-Z][a-z]{2})[a-z]*, (\d\d)\.(\d\d)\.(\d{4}) (\d\d:\d\d:\d\d)$`) {
		f := strings.Split(s, "|")
		op := map[string]string{"Read": "read", "Written": "write"}[f[0]]
		orc = append(orc, strings.Join([]string{op, f[4], f[3], f[2], f[5], f[1]}, "|"))
	}
	orcEq(t, "DS1307 date and time", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerRTC8564(t *testing.T) {
	orcNeed(t, "i2c", "rtc8564")
	ps, anns := orcI2CRun(t, "rtc8564", "rtc8564", "rtc8564", []orcI2CTxn{
		{addr: 0x51, data: []byte{2}, noStop: true}, {addr: 0x51, read: true, data: []byte{0x56, 0x34, 0x12, 0x18, 0x03, 0x10, 0x26}}})
	repo := orcMatch(orcNotes(ps), `^RTC read \d\d(\d\d)-(\d\d)-(\d\d) (\d\d:\d\d:\d\d) `)
	var orc []string
	for _, s := range orcMatch(orcTexts(anns), `^Read date/time: (\d\d)\.(\d\d)\.(\d\d) (\d\d:\d\d:\d\d)$`) {
		f := strings.Split(s, "|")
		orc = append(orc, strings.Join([]string{f[2], f[1], f[0], f[3]}, "|"))
	}
	orcEq(t, "RTC-8564 date and time", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerATSHA204A(t *testing.T) {
	orcNeed(t, "i2c", "atsha204a")
	rd := atshaCmd(0x02, 0x00, 0x0010, nil)
	nonce := atshaCmd(0x16, 0x00, 0x0000, make([]byte, 20))
	ps, anns := orcI2CRun(t, "atsha204a", "atsha204a", "atsha204a", []orcI2CTxn{
		{addr: 0x64, data: rd.Bytes}, {addr: 0x64, read: true, data: []byte{4, 0x00, 0x03, 0x40}},
		{addr: 0x64, data: nonce.Bytes}, {addr: 0x64, data: []byte{0x01}}})
	repo := orcMatch(orcNotes(ps), `^ATSHA204A (\w+)`)
	var orc []string
	ts := orcTexts(anns)
	for _, s := range ts {
		switch {
		case strings.HasPrefix(s, "Opcode: "):
			orc = append(orc, strings.TrimPrefix(s, "Opcode: "))
		case s == "Status: Command success":
			orc = append(orc, "status")
		case s == "Word addr: SLEEP":
			orc = append(orc, "sleep")
		}
	}
	orcEq(t, "ATSHA204A commands", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerNunchuk(t *testing.T) {
	orcNeed(t, "i2c", "nunchuk")
	ps, anns := orcI2CRun(t, "nunchuk", "nunchuk", "nunchuk", []orcI2CTxn{
		{addr: 0x52, data: []byte{0x40, 0x00}}, {addr: 0x52, read: true, data: []byte{0x80, 0x7f, 0x80, 0x81, 0xb0, 0x5d}},
		{addr: 0x52, read: true, data: []byte{0x10, 0xe0, 0x40, 0x41, 0x42, 0x02}}})
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^Nunchuk stick (\d+)/(\d+) accel (\d+)/(\d+)/(\d+)( Z)?( C)?$`) {
		f := strings.Split(s, "|")
		repo = append(repo, fmt.Sprintf("%s/%s %s/%s/%s Z:%v C:%v", f[0], f[1], f[2], f[3], f[4], f[5] != "", f[6] != ""))
	}
	repo = append(orcMatch(orcNotes(ps), `^Nunchuk (init)$`), repo...)
	var orc []string
	for _, s := range orcTexts(anns) {
		if s == "Initialize Nunchuk" {
			orc = append(orc, "init")
		}
		if m := regexp.MustCompile(`^Analog stick: (\d+)/(\d+), accelerometer: (\d+)/(\d+)/(\d+), Z: (not )?pressed, C: (not )?pressed$`).FindStringSubmatch(s); m != nil {
			orc = append(orc, fmt.Sprintf("%s/%s %s/%s/%s Z:%v C:%v", m[1], m[2], m[3], m[4], m[5], m[6] == "", m[7] == ""))
		}
	}
	// sigrok's register counter steps past the sixth byte to 1, so it reads
	// the first report only; the scope reads every one from its first byte
	orcEq(t, "Nunchuk reports", repo[:2], orc)
	if len(repo) != 3 || repo[2] != "16/224 256/260/264 Z:true C:false" {
		t.Fatalf("second report %q", repo)
	}
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerHDCP(t *testing.T) {
	orcNeed(t, "i2c", "hdcp")
	ps, anns := orcI2CRun(t, "hdcp", "hdcp", "hdcp", []orcI2CTxn{
		{addr: 0x3a, data: []byte{0x41}, noStop: true}, {addr: 0x3a, read: true, data: []byte{0x02, 0x10}},
		{addr: 0x3a, data: []byte{0x50}, noStop: true}, {addr: 0x3a, read: true, data: []byte{0x04}},
		{addr: 0x3a, data: []byte{0x70}, noStop: true}, {addr: 0x3a, read: true, data: []byte{0x34, 0x06}},
		{addr: 0x3a, data: []byte{0x60, 0x02, 1, 2}}})
	var repo []string
	for _, n := range orcNotes(ps) {
		switch {
		case regexp.MustCompile(`^HDCP read Bstatus (\d+) devices depth (\d+)( HDMI)?$`).MatchString(n):
			m := regexp.MustCompile(`^HDCP read Bstatus (\d+) devices depth (\d+)( HDMI)?$`).FindStringSubmatch(n)
			repo = append(repo, fmt.Sprintf("Bstatus %s %s %v", m[1], m[2], m[3] != ""))
		case strings.HasPrefix(n, "HDCP read HDCP2Version "):
			repo = append(repo, strings.TrimPrefix(n, "HDCP read HDCP2Version "))
		case regexp.MustCompile(`^HDCP read RxStatus length (\d+)( ready)?( reauth)?$`).MatchString(n):
			m := regexp.MustCompile(`^HDCP read RxStatus length (\d+)( ready)?( reauth)?$`).FindStringSubmatch(n)
			repo = append(repo, fmt.Sprintf("RxStatus %v %v %s", m[3] != "", m[2] != "", m[1]))
		case strings.HasPrefix(n, "HDCP write Write_Message "):
			repo = append(repo, "Write_Message "+strings.TrimPrefix(n, "HDCP write Write_Message "))
		}
	}
	var orc []string
	for _, s := range orcTexts(anns) {
		switch {
		case strings.HasPrefix(s, "1.4 Bstatus, "):
			m := regexp.MustCompile(`, (\d+) devices, depth (\d+), hdmi mode (True|False)$`).FindStringSubmatch(s)
			orc = append(orc, fmt.Sprintf("Bstatus %s %s %v", m[1], m[2], m[3] == "True"))
		case s == "HDCP2" || s == "NOT HDCP2":
			orc = append(orc, map[string]string{"HDCP2": "HDCP2", "NOT HDCP2": "not HDCP2"}[s])
		case strings.HasPrefix(s, "RxStatus, "):
			m := regexp.MustCompile(`reauth (True|False), ready (True|False), length (\d+)$`).FindStringSubmatch(s)
			orc = append(orc, fmt.Sprintf("RxStatus %v %v %s", m[1] == "True", m[2] == "True", m[3]))
		case strings.HasPrefix(s, "Write_Message, "):
			orc = append(orc, "Write_Message "+strings.TrimPrefix(s, "Write_Message, "))
		}
	}
	orcEq(t, "HDCP registers", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerLM75(t *testing.T) {
	orcNeed(t, "i2c", "lm75")
	// sigrok's LM75 reads the temperature register (its pointer at power-up)
	// and does not sign the value: temperatures above zero
	ps, anns := orcI2CRun(t, "lm75", "lm75", "lm75=celsius", []orcI2CTxn{
		{addr: 0x48, read: true, data: []byte{0x19, 0x80}}, {addr: 0x48, read: true, data: []byte{0x64, 0x00}},
		{addr: 0x4f, read: true, data: []byte{0x00, 0x80}}})
	orcEq(t, "LM75 temperatures", orcMatch(orcNotes(ps), `Temperature (-?\d+\.\d) C$`), orcMatch(orcTexts(anns), `^Temperature: (-?\d+\.\d) °C$`))
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerLTC26x7(t *testing.T) {
	orcNeed(t, "i2c", "ltc26x7")
	ps, anns := orcI2CRun(t, "ltc26x7", "ltc26x7", "ltc26x7", []orcI2CTxn{
		{addr: 0x10, data: []byte{0x30, 0x80, 0x00}}, {addr: 0x10, data: []byte{0x01, 0xff, 0xff}}, {addr: 0x10, data: []byte{0x0f, 0x40, 0x00}}})
	names := map[string]string{"Write": "Write Input Register", "Write+Update": "Write and Power Up DAC", "Update": "Update DAC"}
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `^LTC26x7 (Write\+Update|Write|Update) (DAC A|DAC B|all DACs) 0x([0-9A-F]{4})`) {
		f := strings.Split(s, "|")
		v, _ := strconv.ParseUint(f[2], 16, 16)
		dac := f[1]
		if dac == "all DACs" {
			dac = "All DACs"
		}
		repo = append(repo, names[f[0]]+"|"+dac+"|"+fmt.Sprintf("%.6f", 1.5*float64(v)/0xffff))
	}
	var orc []string
	ts := orcTexts(anns)
	for i := 0; i+2 < len(ts); i++ {
		if strings.HasPrefix(ts[i], "Write") || strings.HasPrefix(ts[i], "Update") {
			if m := regexp.MustCompile(`^(\d\.\d{6})V$`).FindStringSubmatch(ts[i+2]); m != nil {
				orc = append(orc, ts[i]+"|"+ts[i+1]+"|"+m[1])
			}
		}
	}
	orcEq(t, "LTC26x7 writes", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerTCA6408A(t *testing.T) {
	orcNeed(t, "i2c", "tca6408a")
	ps, anns := orcI2CRun(t, "tca6408a", "tca6408a", "tca6408a", []orcI2CTxn{
		{addr: 0x20, data: []byte{3, 0xf0}}, {addr: 0x20, data: []byte{1, 0x0f}}, {addr: 0x20, data: []byte{2, 0x01}},
		{addr: 0x20, data: []byte{0}, noStop: true}, {addr: 0x20, read: true, data: []byte{0xaa}}})
	var repo []string
	for _, s := range orcMatch(orcNotes(ps), `(Input|Output|Polarity|Config)=([0-9A-F]{2})`) {
		repo = append(repo, s)
	}
	names := map[string]string{"State of inputs": "Input", "Outputs set": "Output", "Polarity inverted": "Polarity", "Configuration": "Config"}
	var orc []string
	for _, s := range orcMatch(orcTexts(anns), `^(State of inputs|Outputs set|Polarity inverted|Configuration): ([0-9A-F]{2})$`) {
		f := strings.Split(s, "|")
		orc = append(orc, names[f[0]]+"|"+f[1])
	}
	orcEq(t, "TCA6408A registers", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerPCA9571(t *testing.T) {
	orcNeed(t, "i2c", "pca9571")
	ps, anns := orcI2CRun(t, "pca9571", "pca9571", "pca9571", []orcI2CTxn{
		{addr: 0x25, data: []byte{0x55}}, {addr: 0x25, read: true, data: []byte{0x55}}, {addr: 0x25, data: []byte{0xa0}}})
	repo := orcMatch(orcNotes(ps), `^PCA9571 (outputs|read) ([0-9A-F]{2})$`)
	var orc []string
	for _, s := range orcMatch(orcTexts(anns), `^(Outputs set|Outputs read): ([0-9A-F]{2})$`) {
		f := strings.Split(s, "|")
		orc = append(orc, map[string]string{"Outputs set": "outputs", "Outputs read": "read"}[f[0]]+"|"+f[1])
	}
	orcEq(t, "PCA9571 ports", repo, orc)
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerMCP23017(t *testing.T) {
	orcNeed(t, "i2c", "mcp230xx")
	ps, anns := orcI2CRun(t, "mcp23017", "mcp230xx", "mcp230xx", []orcI2CTxn{
		{addr: 0x20, data: []byte{0x00, 0x00, 0xff}}, {addr: 0x20, data: []byte{0x12}, noStop: true},
		{addr: 0x20, read: true, data: []byte{0x55, 0xaa}}, {addr: 0x21, data: []byte{0x14, 0x01}}})
	var repo []string
	for _, n := range orcNotes(ps) {
		m := regexp.MustCompile(`^MCP23017 2[01] (read|write) (.*)$`).FindStringSubmatch(n)
		if m == nil {
			continue
		}
		for _, kv := range strings.Fields(m[2]) {
			f := strings.SplitN(kv, "=", 2)
			repo = append(repo, map[string]string{"read": "Read", "write": "Write"}[m[1]]+" "+f[0]+": "+f[1])
		}
	}
	orcEq(t, "MCP23017 registers", repo, orcMatch(orcTexts(anns), `^((?:Read|Write) [A-Z]+: [0-9A-F]{2})$`))
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerMCP23008(t *testing.T) {
	orcNeed(t, "i2c", "mcp230xx")
	const sr, f = 2_000_000, 100_000
	scl, sda := orcI2CWaves(sr, f, []orcI2CTxn{{addr: 0x20, data: []byte{0x00, 0x0f}}, {addr: 0x20, data: []byte{0x09}, noStop: true},
		{addr: 0x20, read: true, data: []byte{0x5a}}, {addr: 0x20, data: []byte{0x06, 0xff, 0x00}}})
	ps := orcI2CLayer(t, sr, scl, sda, "mcp23008")
	anns := orcDecode(t, sr, []string{"SCL", "SDA"}, [][]byte{scl, sda}, "i2c:scl=SCL:sda=SDA,mcp230xx:type=MCP23008", "mcp230xx")
	var repo []string
	for _, n := range orcNotes(ps) {
		m := regexp.MustCompile(`^MCP23008 20 (read|write) (.*)$`).FindStringSubmatch(n)
		if m == nil {
			continue
		}
		for _, kv := range strings.Fields(m[2]) {
			f := strings.SplitN(kv, "=", 2)
			repo = append(repo, map[string]string{"read": "Read", "write": "Write"}[m[1]]+" "+f[0]+": "+f[1])
		}
	}
	orcEq(t, "MCP23008 registers", repo, orcMatch(orcTexts(anns), `^((?:Read|Write) [A-Z]+: [0-9A-F]{2})$`))
}

// TRLC-LINKS: REQ-SDS-243
func TestOracleLayerMXC6225XU(t *testing.T) {
	orcNeed(t, "i2c", "mxc6225xu")
	// sigrok writes the axes unsigned: values under 128
	ps, anns := orcI2CRun(t, "mxc6225xu", "mxc6225xu", "mxc6225xu", []orcI2CTxn{
		{addr: 0x15, data: []byte{0}, noStop: true}, {addr: 0x15, read: true, data: []byte{0x10, 0x30}},
		{addr: 0x15, data: []byte{0}, noStop: true}, {addr: 0x15, read: true, data: []byte{0x7f, 0x01}}})
	var repo []string
	for _, n := range orcNotes(ps) {
		for _, m := range regexp.MustCompile(`([XY]OUT)=(-?\d+)`).FindAllStringSubmatch(n, -1) {
			repo = append(repo, m[1]+": "+m[2])
		}
	}
	orcEq(t, "MXC6225XU axes", repo, orcMatch(orcTexts(anns), `^([XY]OUT: -?\d+)$`))
}

// TRLC-LINKS: REQ-SDS-249
func TestOracleLayerI2CFilter(t *testing.T) {
	orcNeed(t, "i2c", "i2cfilter", "mcp230xx")
	// sigrok's filter forwards one address's transfers to the decoder stacked
	// on it; the scope's filter keeps them in the packet list, read here by
	// the MCP23017 layer
	const sr, f = 2_000_000, 100_000
	scl, sda := orcI2CWaves(sr, f, []orcI2CTxn{{addr: 0x20, data: []byte{0x00, 0xff}}, {addr: 0x21, data: []byte{0x00, 0x0f}},
		{addr: 0x21, data: []byte{0x12}, noStop: true}, {addr: 0x21, read: true, data: []byte{0x55}}, {addr: 0x20, data: []byte{0x14, 0x01}}})
	r := decodeI2COrFail(t, sr, scl, sda)
	kept := ApplyLayer("i2cfilter", "i2c", Packets(r, len(scl)), LayerArgs{Addr: 0x21})
	ps := ApplyLayer("mcp23017", "i2c", kept, LayerArgs{Addr: -1})
	anns := orcDecode(t, sr, []string{"SCL", "SDA"}, [][]byte{scl, sda}, "i2c:scl=SCL:sda=SDA,i2cfilter:address=33,mcp230xx", "mcp230xx")
	var repo []string
	for _, n := range orcNotes(ps) {
		m := regexp.MustCompile(`^MCP23017 21 (read|write) (.*)$`).FindStringSubmatch(n)
		if m == nil {
			if !strings.HasPrefix(n, "MCP23017 21 register ") {
				t.Fatalf("filtered note %q", n)
			}
			continue
		}
		for _, kv := range strings.Fields(m[2]) {
			f := strings.SplitN(kv, "=", 2)
			repo = append(repo, map[string]string{"read": "Read", "write": "Write"}[m[1]]+" "+f[0]+": "+f[1])
		}
	}
	orcEq(t, "filtered transfers", repo, orcMatch(orcTexts(anns), `^((?:Read|Write) [A-Z]+: [0-9A-F]{2})$`))
}
