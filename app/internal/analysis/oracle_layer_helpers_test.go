// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// Oracle cross-checks of the layers against libsigrokdecode's stacked
// decoders (sigrok-cli -P spi,...,spiflash): the same waveform is decoded by
// the scope's bus decoder, grouped into packets and read by the layer, and by
// sigrok's bus decoder and the decoder stacked on it; the tests compare what
// both name (commands, registers, addresses, values). sigrok sees the lines
// the scope cannot hold (an SPI bus's chip select and its other data line);
// the scope reads its one data line with the bursts the clock's pauses make.
// Skips without sigrok-cli (fails under CI_REQUIRE_SIGROK=1), and a decoder
// the installed sigrok does not ship skips its test (Ubuntu's 0.5.3 lacks the
// newer ones, which CI checks against libsigrokdecode's pinned decoders).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"open-sds/app/internal/decode"
)

// orcNeed skips (or under CI_REQUIRE_SIGROK=1 fails) without sigrok-cli and
// without the decoders the test stacks.
// TRLC-LINKS: REQ-SDS-249
func orcNeed(t *testing.T, pds ...string) {
	t.Helper()
	if _, err := exec.LookPath("sigrok-cli"); err != nil {
		if os.Getenv("CI_REQUIRE_SIGROK") == "1" {
			t.Fatal("CI_REQUIRE_SIGROK=1 but sigrok-cli is not installed")
		}
		t.Skip("sigrok-cli not installed; skipping oracle cross-check")
	}
	out, err := exec.Command("sigrok-cli", "-L").CombinedOutput()
	if err != nil {
		t.Fatalf("sigrok-cli -L: %v\n%s", err, out)
	}
	for _, pd := range pds {
		if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(pd) + `\s`).Match(out) {
			t.Skipf("this sigrok has no %s decoder", pd)
		}
	}
}

// orcNewer reports a libsigrokdecode newer than the 0.5 releases (it ships
// ltc26x7, added after them): its decoders read more than 0.5.3's.
// TRLC-LINKS: REQ-SDS-249
func orcNewer() bool {
	out, err := exec.Command("sigrok-cli", "-L").CombinedOutput()
	return err == nil && regexp.MustCompile(`(?m)^\s+ltc26x7\s`).Match(out)
}

// orcAnn is one sigrok annotation.
type orcAnn struct {
	I0, I1 int
	Text   string
}

var orcAnnRe = regexp.MustCompile(`^(\d+)-(\d+) [\w-]+: (.*)$`)

// orcDecode writes the channels as a logic CSV and returns the annotations
// sigrok-cli prints for the -A selection.
// TRLC-LINKS: REQ-SDS-249
func orcDecode(t *testing.T, sr int, names []string, chans [][]byte, pd, ann string) []orcAnn {
	t.Helper()
	n := 0
	for _, c := range chans {
		n = max(n, len(c))
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(names, ",") + "\n")
	for i := 0; i < n; i++ {
		for k, c := range chans {
			if k > 0 {
				sb.WriteByte(',')
			}
			v := byte(1)
			if i < len(c) {
				v = c[i]
			} else if len(c) > 0 {
				v = c[len(c)-1]
			}
			sb.WriteByte('0' + v)
		}
		sb.WriteByte('\n')
	}
	csv := filepath.Join(t.TempDir(), "wave.csv")
	if err := os.WriteFile(csv, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-i", csv, "-I", fmt.Sprintf("csv:column_formats=%dl:samplerate=%d", len(chans), sr), "-P", pd,
		"-A", ann, "--protocol-decoder-samplenum"}
	out, err := exec.Command("sigrok-cli", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("sigrok-cli %v: %v\n%s", args, err, out)
	}
	var anns []orcAnn
	for _, line := range strings.Split(string(out), "\n") {
		m := orcAnnRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		i0, _ := strconv.Atoi(m[1])
		i1, _ := strconv.Atoi(m[2])
		anns = append(anns, orcAnn{i0, i1, strings.Trim(m[3], `"`)})
	}
	if os.Getenv("ORC_DUMP") != "" { // the oracle's own words, when writing a cross-check
		t.Logf("%s -A %s:\n%s", pd, ann, out)
	}
	return anns
}

// orcTexts are the annotations' texts.
// TRLC-LINKS: REQ-SDS-249
func orcTexts(as []orcAnn) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Text)
	}
	return out
}

// orcEq fails the test when the scope's and sigrok's readings differ.
// TRLC-LINKS: REQ-SDS-249
func orcEq(t *testing.T, what string, repo, orc []string) {
	t.Helper()
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("%s differ:\n repo   %q\n sigrok %q", what, repo, orc)
	}
	if len(repo) == 0 {
		t.Fatalf("%s: nothing read", what)
	}
}

// orcLine is a logic line built by appending levels for durations; time
// accumulates in seconds so fractional samples per bit behave like an
// asynchronous capture.
type orcLine struct {
	sr   float64
	t    float64
	bits []byte
}

// TRLC-LINKS: REQ-SDS-249
func (w *orcLine) add(level byte, dur float64) {
	w.t += dur
	for len(w.bits) < int(w.t*w.sr) {
		w.bits = append(w.bits, level)
	}
}

// orcCodes are a logic line as the converter's two levels.
// TRLC-LINKS: REQ-SDS-249
func orcCodes(bits []byte) []uint8 {
	out := make([]uint8, len(bits))
	for i, b := range bits {
		out[i] = 56
		if b != 0 {
			out[i] = 200
		}
	}
	return out
}

// orcNotes are the layer's notes of the packets that carry one.
// TRLC-LINKS: REQ-SDS-249
func orcNotes(ps []Packet) []string {
	var out []string
	for _, p := range ps {
		if p.Note != "" {
			out = append(out, p.Note)
		}
	}
	return out
}

// ---- SPI: four lines for sigrok, clock and one data line for the scope ----

// orcSPITxn is one transaction: the bytes on MOSI and on MISO (MISO
// shorter: 0xFF after its end).
type orcSPITxn struct{ mosi, miso []byte }

// orcSPIWaves renders transactions at rate with chip select (active low;
// high between transactions for gap bit times), mode 0, MSB first.
// TRLC-LINKS: REQ-SDS-249
func orcSPIWaves(sr, rate float64, txns []orcSPITxn, gap float64) (clk, mosi, miso, cs []byte) {
	c, o, i, s := &orcLine{sr: sr}, &orcLine{sr: sr}, &orcLine{sr: sr}, &orcLine{sr: sr}
	bt := 1 / rate
	seg := func(cl, mo, mi, cs byte, d float64) {
		c.add(cl, d)
		o.add(mo, d)
		i.add(mi, d)
		s.add(cs, d)
	}
	seg(0, 0, 0, 1, 6*bt)
	for _, tx := range txns {
		seg(0, 0, 0, 0, bt) // chip select falls, the clock still idle
		for k, v := range tx.mosi {
			w := byte(0xff)
			if k < len(tx.miso) {
				w = tx.miso[k]
			}
			for b := 7; b >= 0; b-- {
				mb, wb := v>>b&1, w>>b&1
				seg(0, mb, wb, 0, bt/2)
				seg(1, mb, wb, 0, bt/2)
			}
		}
		seg(0, 0, 0, 0, bt)
		seg(0, 0, 0, 1, gap*bt)
	}
	seg(0, 0, 0, 1, 6*bt)
	return c.bits, o.bits, i.bits, s.bits
}

// orcSPILayer decodes the clock and one data line as the scope does and
// reads the packets with the layer.
// TRLC-LINKS: REQ-SDS-249
func orcSPILayer(t *testing.T, sr float64, clk, data []byte, layer string) []Packet {
	t.Helper()
	r := decode.DecodeSPI(orcCodes(clk), orcCodes(data), 1/sr, decode.SPICfg{MSB: true})
	if !r.OK {
		t.Fatalf("SPI decode: %s", r.Error)
	}
	return ApplyLayer(layer, "spi", Packets(r, len(clk)), LayerArgs{Addr: -1})
}

// orcSPI is the sigrok stack for an SPI layer: the four lines.
// TRLC-LINKS: REQ-SDS-249
func orcSPI(t *testing.T, sr int, clk, mosi, miso, cs []byte, pd, ann string) []orcAnn {
	return orcDecode(t, sr, []string{"CLK", "MOSI", "MISO", "CS"}, [][]byte{clk, mosi, miso, cs},
		"spi:clk=CLK:mosi=MOSI:miso=MISO:cs=CS,"+pd, ann)
}

// ---- I2C ----

// orcI2CTxn is one I2C transfer: no stop leaves the bus to a repeated START.
type orcI2CTxn struct {
	addr   int
	read   bool
	data   []byte
	noStop bool
	nak    bool // the address not acknowledged
}

// orcI2CWaves renders transfers at fclk.
// TRLC-LINKS: REQ-SDS-249
func orcI2CWaves(sr, fclk float64, txns []orcI2CTxn) (scl, sda []byte) {
	c, d := &orcLine{sr: sr}, &orcLine{sr: sr}
	bt := 1 / fclk
	cur := byte(1)
	seg := func(cl, da byte, dur float64) {
		c.add(cl, dur)
		d.add(da, dur)
		cur = da
	}
	bit := func(b byte) {
		seg(0, b, bt/2)
		seg(1, b, bt/2)
	}
	byteAck := func(v byte, nak bool) {
		for k := 7; k >= 0; k-- {
			bit(v >> k & 1)
		}
		if nak {
			bit(1)
		} else {
			bit(0)
		}
	}
	seg(1, 1, 4*bt)
	for _, tx := range txns {
		seg(0, cur, bt/4) // a repeated START: release SDA while SCL is low
		seg(0, 1, bt/4)
		seg(1, 1, bt/2)
		seg(1, 0, bt/2) // START
		a := byte(tx.addr << 1)
		if tx.read {
			a |= 1
		}
		byteAck(a, tx.nak)
		if !tx.nak {
			for k, v := range tx.data {
				byteAck(v, tx.read && k == len(tx.data)-1)
			}
		}
		if !tx.noStop {
			seg(0, cur, bt/4)
			seg(0, 0, bt/4)
			seg(1, 0, bt/2)
			seg(1, 1, bt/2) // STOP
			seg(1, 1, 3*bt)
		}
	}
	seg(1, 1, 4*bt)
	return c.bits, d.bits
}

// orcI2CLayer decodes the bus as the scope does and reads it with the layer.
// TRLC-LINKS: REQ-SDS-249
func orcI2CLayer(t *testing.T, sr float64, scl, sda []byte, layer string) []Packet {
	t.Helper()
	r := decodeI2COrFail(t, sr, scl, sda)
	return ApplyLayer(layer, "i2c", Packets(r, len(scl)), LayerArgs{Addr: -1})
}

// decodeI2COrFail decodes the bus as the scope does.
// TRLC-LINKS: REQ-SDS-249
func decodeI2COrFail(t *testing.T, sr float64, scl, sda []byte) decode.Result {
	t.Helper()
	r := decode.DecodeI2C(orcCodes(scl), orcCodes(sda), 1/sr, decode.I2CCfg{})
	if !r.OK {
		t.Fatalf("I2C decode: %s", r.Error)
	}
	return r
}

// orcI2C is the sigrok stack for an I2C layer.
// TRLC-LINKS: REQ-SDS-249
func orcI2C(t *testing.T, sr int, scl, sda []byte, pd, ann string) []orcAnn {
	return orcDecode(t, sr, []string{"SCL", "SDA"}, [][]byte{scl, sda}, "i2c:scl=SCL:sda=SDA,"+pd, ann)
}

// ---- UART ----

// orcUARTWave renders bursts of characters (8 data bits, parity, stops),
// gap bit times of idle after each burst; inverted idles low.
// TRLC-LINKS: REQ-SDS-249
func orcUARTWave(sr, baud float64, parity string, stops int, inverted bool, bursts [][]byte, gap float64) []byte {
	w := &orcLine{sr: sr}
	bt := 1 / baud
	lv := func(b byte) byte {
		if inverted {
			return 1 - b
		}
		return b
	}
	w.add(lv(1), 20*bt)
	for _, burst := range bursts {
		for _, v := range burst {
			w.add(lv(0), bt)
			ones := 0
			for k := 0; k < 8; k++ {
				b := v >> k & 1
				ones += int(b)
				w.add(lv(b), bt)
			}
			if parity == "even" || parity == "odd" {
				p := byte(ones & 1)
				if parity == "odd" {
					p ^= 1
				}
				w.add(lv(p), bt)
			}
			w.add(lv(1), float64(stops)*bt)
		}
		w.add(lv(1), gap*bt)
	}
	w.add(lv(1), 20*bt)
	return w.bits
}

// orcUARTLayer decodes the line as the scope does and reads it with the layer.
// TRLC-LINKS: REQ-SDS-249
func orcUARTLayer(t *testing.T, sr float64, line []byte, baud int, parity string, inverted bool, layer string) []Packet {
	t.Helper()
	r := decode.DecodeUART(orcCodes(line), 1/sr, decode.UARTCfg{Baud: baud, Parity: parity, Inverted: inverted})
	if !r.OK {
		t.Fatalf("UART decode: %s", r.Error)
	}
	return ApplyLayer(layer, "uart", Packets(r, len(line)), LayerArgs{Addr: -1})
}

// ---- 1-Wire ----

// orcOWReset appends a reset with a presence pulse.
// TRLC-LINKS: REQ-SDS-249
func orcOWReset(w *orcLine) {
	w.add(0, 500e-6)
	w.add(1, 30e-6)
	w.add(0, 120e-6)
	w.add(1, 350e-6)
}

// orcOWByte appends a byte's time slots, least significant bit first.
// TRLC-LINKS: REQ-SDS-249
func orcOWByte(w *orcLine, v byte) {
	for k := 0; k < 8; k++ {
		if v>>k&1 == 1 {
			w.add(0, 6e-6)
			w.add(1, 64e-6)
		} else {
			w.add(0, 60e-6)
			w.add(1, 10e-6)
		}
	}
}

// orcOWWave renders transactions: each a reset, then its bytes.
// TRLC-LINKS: REQ-SDS-249
func orcOWWave(sr float64, txns [][]byte) []byte {
	w := &orcLine{sr: sr}
	w.add(1, 200e-6)
	for _, tx := range txns {
		orcOWReset(w)
		for _, v := range tx {
			orcOWByte(w, v)
		}
		w.add(1, 300e-6)
	}
	w.add(1, 500e-6)
	return w.bits
}

// orcOWLayer decodes the line as the scope does and reads it with the layer.
// TRLC-LINKS: REQ-SDS-249
func orcOWLayer(t *testing.T, sr float64, line []byte, layer string) []Packet {
	t.Helper()
	r := decode.DecodeOneWire(orcCodes(line), 1/sr, decode.OneWireCfg{})
	if !r.OK {
		t.Fatalf("1-Wire decode: %s", r.Error)
	}
	return ApplyLayer(layer, "onewire", Packets(r, len(line)), LayerArgs{Addr: -1})
}

// orcROM is a ROM code with its CRC-8.
// TRLC-LINKS: REQ-SDS-249
func orcROM(family byte, serial ...byte) []byte {
	r := append([]byte{family}, serial...)
	return append(r, byte(crc8Maxim(r)))
}

// ---- MDIO ----

// orcMDIOFrame is one management frame.
type orcMDIOFrame struct{ st, op, port, dev, data int }

// orcMDIOWaves renders frames (a preamble of 32 ones each) at per a bit.
// TRLC-LINKS: REQ-SDS-249
func orcMDIOWaves(sr, per float64, fs []orcMDIOFrame) (mdc, mdio []byte) {
	c, d := &orcLine{sr: sr}, &orcLine{sr: sr}
	bit := func(b byte) {
		c.add(0, per/2)
		d.add(b, per/2)
		c.add(1, per/2)
		d.add(b, per/2)
	}
	put := func(v, n int) {
		for k := n - 1; k >= 0; k-- {
			bit(byte(v >> k & 1))
		}
	}
	c.add(0, 4*per)
	d.add(1, 4*per)
	for _, f := range fs {
		for i := 0; i < 32; i++ {
			bit(1)
		}
		put(f.st, 2)
		put(f.op, 2)
		put(f.port, 5)
		put(f.dev, 5)
		bit(1)
		bit(0)
		put(f.data, 16)
		c.add(0, per/2)
		d.add(1, per/2)
		c.add(1, 8*per)
		d.add(1, 8*per)
	}
	return c.bits, d.bits
}

// ---- USB full speed ----

// orcUSBLSB is v as n bits, least significant first.
// TRLC-LINKS: REQ-SDS-249
func orcUSBLSB(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v >> i & 1
	}
	return out
}

// orcUSBRev reverses the low count bits of v.
// TRLC-LINKS: REQ-SDS-249
func orcUSBRev(v, count int) int {
	out := 0
	for i := 0; i < count; i++ {
		if v>>i&1 == 1 {
			out |= 1 << (count - 1 - i)
		}
	}
	return out
}

// orcUSBCRC5 and orcUSBCRC16 are the token and data CRCs over the wire bits.
// TRLC-LINKS: REQ-SDS-249
func orcUSBCRC5(bits []int) int {
	crc := 0x1f
	for _, b := range bits {
		crc <<= 1
		if b != crc>>5 {
			crc ^= 0x25
		}
		crc &= 0x1f
	}
	return orcUSBRev(crc^0x1f, 5)
}

// TRLC-LINKS: REQ-SDS-249
func orcUSBCRC16(bits []int) int {
	crc := 0xffff
	for _, b := range bits {
		crc <<= 1
		if b != crc>>16 {
			crc ^= 0x18005
		}
		crc &= 0xffff
	}
	return orcUSBRev(crc^0xffff, 16)
}

// orcUSBPacket is one packet's bit-stuffed bits: SYNC, PID, payload.
// TRLC-LINKS: REQ-SDS-249
func orcUSBPacket(pid int, payload []int) []int {
	bits := []int{0, 0, 0, 0, 0, 0, 0, 1}
	bits = append(bits, orcUSBLSB(pid&0xf|(^pid&0xf)<<4, 8)...)
	bits = append(bits, payload...)
	var out []int
	ones := 0
	for _, b := range bits {
		out = append(out, b)
		if b == 1 {
			if ones++; ones == 6 {
				out = append(out, 0)
				ones = 0
			}
		} else {
			ones = 0
		}
	}
	return out
}

// orcUSBToken and orcUSBData are a token's and a data packet's payload bits.
// TRLC-LINKS: REQ-SDS-249
func orcUSBToken(addr, ep int) []int {
	b := append(orcUSBLSB(addr, 7), orcUSBLSB(ep, 4)...)
	return append(b, orcUSBLSB(orcUSBCRC5(b), 5)...)
}

// TRLC-LINKS: REQ-SDS-249
func orcUSBData(d []byte) []int {
	var b []int
	for _, v := range d {
		b = append(b, orcUSBLSB(int(v), 8)...)
	}
	return append(b, orcUSBLSB(orcUSBCRC16(b), 16)...)
}

// orcUSBWaves renders packets as the full-speed pair (J: D+ high), NRZI,
// an EOP after each and gap bit times of idle.
// TRLC-LINKS: REQ-SDS-249
func orcUSBWaves(sr float64, packets [][]int, gap float64) (dp, dm []byte) {
	bt := 1.0 / 12e6
	p, m := &orcLine{sr: sr}, &orcLine{sr: sr}
	sym := func(a, b byte, cells float64) {
		p.add(a, cells*bt)
		m.add(b, cells*bt)
	}
	emit := func(j bool, cells float64) {
		if j {
			sym(1, 0, cells)
		} else {
			sym(0, 1, cells)
		}
	}
	emit(true, 20)
	for _, bits := range packets {
		j := true
		for _, b := range bits {
			if b == 0 {
				j = !j
			}
			emit(j, 1)
		}
		sym(0, 0, 2)
		emit(true, 1+gap)
	}
	emit(true, 20)
	return p.bits, m.bits
}
