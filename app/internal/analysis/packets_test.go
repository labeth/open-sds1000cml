// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"open-sds/app/internal/decode"
)

// Waveform builders for the decoders, after the decode package's own test
// generators (uartWave, i2cWave, spiWave, canStdFrame, manchesterWave,
// sentWave): codes 40 (low) and 210 (high).

const lvLo, lvHi = uint8(40), uint8(210)

// level is the code of a logic level.
// TRLC-LINKS: REQ-SDS-214
func level(b int) uint8 {
	if b != 0 {
		return lvHi
	}
	return lvLo
}

// line appends n samples of a level.
// TRLC-LINKS: REQ-SDS-214
func line(w []uint8, v uint8, n int) []uint8 {
	for i := 0; i < n; i++ {
		w = append(w, v)
	}
	return w
}

// uartFrame appends one 8N1 frame (LSB first); stop is the stop bit's level
// and parity, when not "", adds an even or odd parity bit (a wrong one when
// badParity).
// TRLC-LINKS: REQ-SDS-214
func uartFrame(w []uint8, b byte, spb int, stop int, parity string, badParity bool) []uint8 {
	w = line(w, lvLo, spb)
	ones := 0
	for c := 0; c < 8; c++ {
		bit := int(b>>c) & 1
		ones += bit
		w = line(w, level(bit), spb)
	}
	if parity != "" {
		p := ones & 1 // even parity: the bit that makes the count even
		if parity == "odd" {
			p ^= 1
		}
		if badParity {
			p ^= 1
		}
		w = line(w, level(p), spb)
	}
	return line(w, level(stop), spb)
}

// uartMsgs renders messages of back-to-back frames, idle bits before, between
// and after them.
// TRLC-LINKS: REQ-SDS-214
func uartMsgs(msgs [][]byte, spb, lead, gap, trail int) []uint8 {
	w := line(nil, lvHi, lead*spb)
	for m, msg := range msgs {
		if m > 0 {
			w = line(w, lvHi, gap*spb)
		}
		for _, b := range msg {
			w = uartFrame(w, b, spb, 1, "", false)
		}
	}
	return line(w, lvHi, trail*spb)
}

// packetBytes is the hex of each packet's bytes, "*" marking a flagged byte,
// "~" an incomplete packet, and the address and fault when set.
// TRLC-LINKS: REQ-SDS-214
func packetBytes(ps []Packet) []string {
	var out []string
	for _, p := range ps {
		var b strings.Builder
		if !p.Complete {
			b.WriteString("~")
		}
		if p.HasAddr {
			rw := "W"
			if p.Read {
				rw = "R"
			}
			fmt.Fprintf(&b, "@%X%s:", p.Addr, rw)
		}
		for i, v := range p.Bytes {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%02X", v)
			if p.ByteErr[i] {
				b.WriteString("*")
			}
		}
		if p.Err != "" {
			b.WriteString(" !" + p.Err)
		}
		out = append(out, b.String())
	}
	return out
}

// checkPackets compares packetBytes with want and checks the invariants.
// TRLC-LINKS: REQ-SDS-214
func checkPackets(t *testing.T, name string, r decode.Result, n int, want ...string) []Packet {
	t.Helper()
	if !r.OK && len(r.Spans) == 0 {
		t.Fatalf("%s: decode failed: %s", name, r.Error)
	}
	ps := Packets(r, n)
	got := packetBytes(ps)
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("%s:\n got %q\nwant %q\n(decode %q)", name, got, want, r.Text)
	}
	for i, p := range ps {
		if p.I0 < 0 || p.I1 >= n || p.I0 > p.I1 || len(p.ByteErr) != len(p.Bytes) {
			t.Errorf("%s: packet %d span [%d, %d] of %d, %d bytes %d flags",
				name, i, p.I0, p.I1, n, len(p.Bytes), len(p.ByteErr))
		}
		if i > 0 && p.I0 < ps[i-1].I0 {
			t.Errorf("%s: packet %d starts before packet %d", name, i, i-1)
		}
	}
	return ps
}

// TestPacketsUART: messages split by idle gaps, cut at the record's edges, a
// break, framing and parity errors.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsUART(t *testing.T) {
	const spb = 16
	ct := 1.0 / (float64(spb) * 115200)
	cfg := decode.UARTCfg{Baud: 115200}
	msgs := [][]byte{[]byte("Hello"), []byte("World!"), {0x55, 0xAA, 0x0D, 0x0A}}
	w := uartMsgs(msgs, spb, 40, 30, 40)
	checkPackets(t, "three messages", decode.DecodeUART(w, ct, cfg), len(w),
		"48 65 6C 6C 6F", "57 6F 72 6C 64 21", "55 AA 0D 0A")

	// gaps of up to 12 bits between bytes stay in the packet (1.5 bytes is 15)
	var w2 []uint8
	w2 = line(w2, lvHi, 40*spb)
	for i, b := range []byte("ABCD") {
		w2 = uartFrame(w2, b, spb, 1, "", false)
		w2 = line(w2, lvHi, []int{0, 3, 12, 0}[i]*spb)
	}
	w2 = line(w2, lvHi, 20*spb)
	w2 = uartFrame(w2, 'E', spb, 1, "", false)
	w2 = line(w2, lvHi, 40*spb)
	checkPackets(t, "inter-byte gaps", decode.DecodeUART(w2, ct, cfg), len(w2), "41 42 43 44", "45")

	// the record opens in the first message, half a bit before its third
	// byte, and ends in the last, inside its second byte: both are cut
	cut := w[40*spb+20*spb-8 : len(w)-40*spb-25*spb]
	ps := checkPackets(t, "cut at both edges", decode.DecodeUART(cut, ct, cfg), len(cut),
		"~6C 6C 6F", "57 6F 72 6C 64 21", "~55")
	if ps[1].I0 <= ps[0].I1 || ps[2].I0 <= ps[1].I1 {
		t.Errorf("packets overlap: %+v", ps)
	}

	// a break (the line held low 20 bits) ends a packet; framing errors on
	// non-zero bytes and parity errors are flagged bytes of their packet
	var w3 []uint8
	w3 = line(w3, lvHi, 40*spb)
	w3 = uartFrame(w3, 'a', spb, 1, "", false)
	w3 = uartFrame(w3, 'b', spb, 1, "", false)
	w3 = line(w3, lvLo, 20*spb) // break
	w3 = line(w3, lvHi, 2*spb)  // mark after break
	w3 = uartFrame(w3, 'c', spb, 1, "", false)
	w3 = uartFrame(w3, 'd', spb, 0, "", false) // stop bit low
	w3 = line(w3, lvHi, spb)
	w3 = uartFrame(w3, 'e', spb, 1, "", false)
	w3 = line(w3, lvHi, 40*spb)
	checkPackets(t, "break and framing error", decode.DecodeUART(w3, ct, cfg), len(w3), "61 62", "63 64* 65")

	var w4 []uint8
	w4 = line(w4, lvHi, 40*spb)
	for i, b := range []byte{0x10, 0x20, 0x30} {
		w4 = uartFrame(w4, b, spb, 1, "even", i == 1)
	}
	w4 = line(w4, lvHi, 40*spb)
	checkPackets(t, "parity error", decode.DecodeUART(w4, ct, decode.UARTCfg{Baud: 115200, Parity: "even"}),
		len(w4), "10 20* 30")
}

// i2cBus builds SCL and SDA together (h samples per half clock).
// TRLC-LINKS: REQ-SDS-214
type i2cBus struct {
	scl, sda []uint8
	h        int
}

// TRLC-LINKS: REQ-SDS-214
func (b *i2cBus) seg(c, d uint8, n int) {
	b.scl = line(b.scl, c, n)
	b.sda = line(b.sda, d, n)
}

// TRLC-LINKS: REQ-SDS-214
func (b *i2cBus) idle(n int) { b.seg(lvHi, lvHi, n) }

// start: SDA falls while SCL is high.
// TRLC-LINKS: REQ-SDS-214
func (b *i2cBus) start() { b.seg(lvHi, lvLo, b.h) }

// restart: a repeated START after an acknowledge.
// TRLC-LINKS: REQ-SDS-214
func (b *i2cBus) restart() {
	b.seg(lvLo, lvHi, b.h)
	b.seg(lvHi, lvHi, b.h/2)
	b.seg(lvHi, lvLo, b.h/2)
}

// byte sends v MSB first and the 9th clock with ack (SDA low) or not.
// TRLC-LINKS: REQ-SDS-214
func (b *i2cBus) byte(v int, ack bool) {
	for k := 7; k >= 0; k-- {
		d := level((v >> k) & 1)
		b.seg(lvLo, d, b.h)
		b.seg(lvHi, d, b.h)
	}
	a := lvHi
	if ack {
		a = lvLo
	}
	b.seg(lvLo, a, b.h)
	b.seg(lvHi, a, b.h)
}

// stop: SDA rises while SCL is high.
// TRLC-LINKS: REQ-SDS-214
func (b *i2cBus) stop() {
	b.seg(lvLo, lvLo, b.h)
	b.seg(lvHi, lvLo, b.h/2)
	b.seg(lvHi, lvHi, b.h)
}

// TestPacketsI2C: START to STOP with the address and direction, a repeated
// START, an address nobody acknowledges, a refused write byte, and a record
// that ends inside a transaction.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsI2C(t *testing.T) {
	b := &i2cBus{h: 20}
	b.idle(80)
	b.start() // write 0x55 0xAA to 0x24
	b.byte(0x24<<1, true)
	b.byte(0x55, true)
	b.byte(0xAA, true)
	b.stop()
	b.idle(60)
	b.start() // write register 0x10, then read two bytes (the last NAKed by the master)
	b.byte(0x50<<1, true)
	b.byte(0x10, true)
	b.restart()
	b.byte(0x50<<1|1, true)
	b.byte(0x01, true)
	b.byte(0x02, false)
	b.stop()
	b.idle(60)
	b.start() // nobody at 0x33
	b.byte(0x33<<1, false)
	b.stop()
	b.idle(60)
	b.start() // the slave refuses the second byte
	b.byte(0x24<<1, true)
	b.byte(0x01, true)
	b.byte(0x02, false)
	b.stop()
	b.idle(60)
	b.start() // cut: the record ends mid-byte
	b.byte(0x24<<1, true)
	b.byte(0x77, true)
	b.seg(lvLo, lvHi, 40)
	r := decode.DecodeI2C(b.scl, b.sda, 1e-6, decode.I2CCfg{})
	checkPackets(t, "transactions", r, len(b.scl),
		"@24W:55 AA", "@50W:10", "@50R:01 02", "@33W: !NAK", "@24W:01 02*", "~@24W:77 !no STOP")
}

// spiBus renders SPI mode 0, MSB first: bursts of bytes back to back with
// gap idle half clocks between them (h samples per half clock).
// TRLC-LINKS: REQ-SDS-214
func spiBus(bursts [][]byte, h, lead, gap int) (clk, data []uint8) {
	seg := func(c, d uint8, n int) {
		clk = line(clk, c, n)
		data = line(data, d, n)
	}
	seg(lvLo, lvLo, lead*h)
	for i, burst := range bursts {
		if i > 0 {
			seg(lvLo, lvLo, gap*h)
		}
		for _, v := range burst {
			for k := 7; k >= 0; k-- {
				d := level(int(v>>k) & 1)
				seg(lvLo, d, h)
				seg(lvHi, d, h)
			}
		}
	}
	seg(lvLo, lvLo, lead*h)
	return clk, data
}

// TestPacketsSPI: clock bursts are packets; the bursts cut by the record's
// edges are not complete.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsSPI(t *testing.T) {
	const h = 10
	clk, data := spiBus([][]byte{{0x9F}, {0x01, 0x02, 0x03, 0x04}, {0xDE, 0xAD, 0xBE, 0xEF}}, h, 60, 60)
	cfg := decode.SPICfg{MSB: true}
	checkPackets(t, "bursts", decode.DecodeSPI(clk, data, 1e-7, cfg), len(clk), "9F", "01 02 03 04", "DE AD BE EF")
	// open in the second burst, end in the third
	a, b := 60*h+16*h+60*h+16*h, len(clk)-60*h-20*h
	checkPackets(t, "cut", decode.DecodeSPI(clk[a:b], data[a:b], 1e-7, cfg), b-a, "~02 03 04", "~DE AD")
}

// canCRC15 is the CAN CRC-15 (polynomial 0x4599).
// TRLC-LINKS: REQ-SDS-214
func canCRC15(bits []int) int {
	crc := 0
	for _, b := range bits {
		next := ((crc >> 14) & 1) ^ (b & 1)
		crc = (crc << 1) & 0x7fff
		if next != 0 {
			crc ^= 0x4599
		}
	}
	return crc
}

// canFrameBits is a classic standard data or remote frame on the wire,
// stuffed, through the ACK delimiter, EOF and intermission; ack sets the ACK
// slot dominant.
// TRLC-LINKS: REQ-SDS-214
func canFrameBits(id int, rtr bool, data []byte, ack bool) []int {
	msb := func(v, n int) []int {
		out := make([]int, n)
		for k := range out {
			out[k] = (v >> (n - 1 - k)) & 1
		}
		return out
	}
	r := 0
	if rtr {
		r = 1
	}
	bits := append([]int{0}, msb(id, 11)...)
	bits = append(bits, r, 0, 0)
	bits = append(bits, msb(len(data), 4)...)
	if !rtr {
		for _, d := range data {
			bits = append(bits, msb(int(d), 8)...)
		}
	}
	bits = append(bits, msb(canCRC15(bits), 15)...)
	var wire []int
	runVal, runLen := -1, 0
	for _, b := range bits {
		if runLen == 5 {
			wire = append(wire, 1-runVal)
			runVal, runLen = 1-runVal, 1
		}
		wire = append(wire, b)
		if b == runVal {
			runLen++
		} else {
			runVal, runLen = b, 1
		}
	}
	if runLen == 5 {
		wire = append(wire, 1-runVal)
	}
	a := 1
	if ack {
		a = 0
	}
	wire = append(wire, 1, a, 1)
	for i := 0; i < 10; i++ {
		wire = append(wire, 1)
	}
	return wire
}

// TestPacketsCAN: the bench capture of the FPGA CAN source, then synthetic
// frames: a remote frame, an unacknowledged one, and one cut by the record.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsCAN(t *testing.T) {
	blob, err := os.ReadFile("../decode/testdata/can_envelope_100us.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		SampleS float64 `json:"sample_s"`
		C1      []int   `json:"c1"`
	}
	if err := json.Unmarshal(blob, &f); err != nil {
		t.Fatal(err)
	}
	c1 := make([]uint8, len(f.C1))
	for i, v := range f.C1 {
		c1[i] = uint8(v)
	}
	r := decode.DecodeCANFD(c1, f.SampleS, decode.CANFDCfg{DominantLow: true})
	found := false
	for _, p := range Packets(r, len(c1)) {
		if p.Complete && p.HasAddr && p.Addr == 0x123 && fmt.Sprintf("% X", p.Bytes) == "55 AA 0F F0" && p.Err == "" {
			found = true
		}
	}
	if !found {
		t.Errorf("bench capture: no complete ID 123 packet 55 AA 0F F0 in %q", packetBytes(Packets(r, len(c1))))
	}

	const spb = 10
	var wire []int
	for i := 0; i < 12; i++ {
		wire = append(wire, 1)
	}
	wire = append(wire, canFrameBits(0x123, false, []byte{0x55, 0xAA, 0x0F, 0xF0}, true)...)
	wire = append(wire, canFrameBits(0x7FF, true, []byte{0, 0}, true)...) // a remote frame, DLC 2
	wire = append(wire, canFrameBits(0x0A5, false, []byte{0x01}, false)...)
	last := canFrameBits(0x321, false, []byte{0xC0, 0xFF, 0xEE}, true)
	wire = append(wire, last[:len(last)-12]...) // cut after the CRC delimiter: no ACK slot
	var w []uint8
	for _, b := range wire {
		w = line(w, level(b), spb) // recessive high
	}
	r = decode.DecodeCANFD(w, 1e-7, decode.CANFDCfg{DominantLow: true})
	checkPackets(t, "frames", r, len(w), "@123W:55 AA 0F F0", "@7FFR:", "@A5W:01 !NAK", "~@321W:C0 FF EE")
}

// manchesterFrames renders IEEE Manchester frames (MSB first), idle high for
// gap bits between them.
// TRLC-LINKS: REQ-SDS-214
func manchesterFrames(frames [][]byte, spb, gap int) []uint8 {
	w := line(nil, lvHi, 6*spb)
	for f, fr := range frames {
		if f > 0 {
			w = line(w, lvHi, gap*spb)
		}
		for _, v := range fr {
			for k := 7; k >= 0; k-- {
				bit := int(v>>k) & 1
				w = line(w, level(1-bit), spb/2) // IEEE: a 1 rises mid-cell
				w = line(w, level(bit), spb-spb/2)
			}
		}
	}
	return line(w, lvHi, 6*spb)
}

// TestPacketsManchester: frames from their preamble; a frame cut by the
// record's end is not complete.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsManchester(t *testing.T) {
	const spb = 20
	w := manchesterFrames([][]byte{{0xAA, 0x12, 0x34}, {0xAA, 0xC3, 0x5A, 0x01}, {0xAA, 0x77}}, spb, 12)
	ct := 1.0 / (float64(spb) * 100000)
	cfg := decode.ManchesterCfg{IEEE: true, MSB: true}
	checkPackets(t, "frames", decode.DecodeManchester(w, ct, cfg), len(w), "AA 12 34", "AA C3 5A 01", "AA 77")
	// a bit of idle after the last frame is too little to know it ended
	cut := w[:len(w)-5*spb]
	checkPackets(t, "cut", decode.DecodeManchester(cut, ct, cfg), len(cut), "AA 12 34", "AA C3 5A 01", "~AA 77")
}

// sentTable is the SAE J2716 CRC-4 table.
var sentTable = [16]int{0, 13, 7, 10, 14, 3, 9, 4, 1, 12, 6, 11, 15, 2, 8, 5}

// sentCRC is the J2716 CRC-4 over the data nibbles (seed 5, augmented).
// TRLC-LINKS: REQ-SDS-214
func sentCRC(data []int) int {
	crc := 5
	for _, d := range data {
		crc = sentTable[crc] ^ (d & 0xf)
	}
	return sentTable[crc] & 0xf
}

// sentLine renders SENT frames (a SYNC and the nibble pulses, each 5 ticks
// low then high to fill its period) at tick samples per tick.
// TRLC-LINKS: REQ-SDS-214
func sentLine(frames [][]int, tick int) []uint8 {
	w := line(nil, lvHi, 8*tick)
	pulse := func(ticks int) {
		w = line(w, lvLo, 5*tick)
		w = line(w, lvHi, (ticks-5)*tick)
	}
	for _, nibs := range frames {
		pulse(56)
		for _, v := range nibs {
			pulse(12 + v)
		}
	}
	w = line(w, lvLo, 5*tick)
	return line(w, lvHi, 40*tick)
}

// TestPacketsSENT: a frame per SYNC with its nibbles; a bad CRC flags the
// CRC nibble.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsSENT(t *testing.T) {
	f1 := []int{0x1, 0xA, 0x5, 0xF, 0x0, 0xC, 0x3, 0}
	f1[7] = sentCRC(f1[1:7])
	f2 := []int{0x0, 0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0}
	f2[7] = sentCRC(f2[1:7])
	bad := append([]int(nil), f2...)
	bad[7] ^= 0x3
	w := sentLine([][]int{f1, f2, bad}, 6)
	r := decode.DecodeSENT(w, 1e-6, decode.SENTCfg{Nibbles: 8})
	checkPackets(t, "frames", r, len(w),
		fmt.Sprintf("01 0A 05 0F 00 0C 03 %02X", f1[7]),
		fmt.Sprintf("00 01 02 03 04 05 06 %02X", f2[7]),
		fmt.Sprintf("00 01 02 03 04 05 06 %02X*", bad[7]))
}

// TestPacketsDegenerate: nothing decoded, spans out of order or out of the
// record, an unknown protocol, wide words.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsDegenerate(t *testing.T) {
	if ps := Packets(decode.Result{}, 100); ps != nil {
		t.Errorf("empty result: %v", ps)
	}
	if ps := Packets(decode.Result{Proto: "uart", Error: "flat"}, 100); ps != nil {
		t.Errorf("failed decode: %v", ps)
	}
	odd := decode.Result{Proto: "uart", SPB: 10, Spans: []decode.Span{
		{I0: 500, I1: 400, Kind: "data", Val: 1}, {I0: -50, I1: 30, Kind: "data", Val: 2},
		{I0: 10, I1: 9000, Kind: "data", Val: 3}, {I0: 1, I1: 2, Kind: "start"}, {I0: 0, I1: 0, Kind: "gap"},
	}}
	for _, p := range Packets(odd, 1000) {
		if p.I0 < 0 || p.I1 > 999 || p.I0 > p.I1 || len(p.Bytes) != len(p.ByteErr) {
			t.Errorf("odd spans gave %+v", p)
		}
	}
	// a 9-bit UART word and a 1553 word take two bytes each, most significant first
	wide := decode.Result{Proto: "uart", SPB: 10, Spans: []decode.Span{
		{I0: 200, I1: 290, Kind: "data", Val: 0x1A5}, {I0: 300, I1: 390, Kind: "frame-error", Val: 0x0FF}}}
	if got := packetBytes(Packets(wide, 2000)); strings.Join(got, "|") != "01 A5 00* FF*" {
		t.Errorf("wide UART words: %q", got)
	}
	mil := decode.Result{Proto: "mil1553", SPB: 20, Spans: []decode.Span{
		{I0: 100, I1: 160, Kind: "start", Text: "csync"}, {I0: 160, I1: 480, Kind: "data", Val: 0x2C45},
		{I0: 500, I1: 560, Kind: "start", Text: "dsync"}, {I0: 560, I1: 880, Kind: "data", Val: 0xBEEF},
		{I0: 880, I1: 900, Kind: "frame-error", Text: "!par"}}}
	if got := packetBytes(Packets(mil, 2000)); strings.Join(got, "|") != "@5R:2C 45 BE* EF* !parity" {
		t.Errorf("1553 message: %q", got)
	}
	unknown := decode.Result{Proto: "who-knows", Spans: []decode.Span{{I0: 100, I1: 110, Kind: "data", Val: 7}}}
	if ps := Packets(unknown, 0); len(ps) != 1 || ps[0].Complete {
		t.Errorf("unknown protocol, unknown record length: %+v", ps)
	}
}

// BenchmarkPackets: a long UART capture of many messages.
// TRLC-LINKS: REQ-SDS-214
func BenchmarkPackets(b *testing.B) {
	var msgs [][]byte
	for i := 0; i < 200; i++ {
		msgs = append(msgs, []byte(fmt.Sprintf("message %d: status OK\r\n", i)))
	}
	w := uartMsgs(msgs, 8, 40, 30, 40)
	r := decode.DecodeUART(w, 1/(8*115200.0), decode.UARTCfg{Baud: 115200})
	b.ReportAllocs()
	for b.Loop() {
		Packets(r, len(w))
	}
}
