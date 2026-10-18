// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"strings"
)

// The SPI layers (REQ-SDS-242): each packet is one transaction (Layer's
// comment), read off the one data line the scope holds. A read's data comes
// back on MISO, which a layer reading MOSI does not see: it counts the bytes
// the host clocked for it ("READ 0x001000, 64 bytes").

// eachNote writes every packet's note with one transaction reader.
// TRLC-LINKS: REQ-SDS-242
func eachNote(ps []Packet, f func(b []byte) (string, bool)) {
	for i := range ps {
		ps[i].Note, ps[i].NoteErr = f(ps[i].Bytes)
	}
}

// maxParts bounds the commands (and trace items) one note names: a long
// burst's note says how many more there are.
const maxParts = 8

// joinNotes joins the notes of the commands one transaction holds, at most
// maxParts of them.
// TRLC-LINKS: REQ-SDS-242
func joinNotes(parts []string) string {
	if len(parts) > maxParts {
		return strings.Join(parts[:maxParts], "; ") + fmt.Sprintf("; .. (+%d)", len(parts)-maxParts)
	}
	return strings.Join(parts, "; ")
}

// joinItems joins a trace burst's items, at most 2*maxParts of them.
// TRLC-LINKS: REQ-SDS-244
func joinItems(items []string) string {
	if len(items) > 2*maxParts {
		return strings.Join(items[:2*maxParts], " ") + fmt.Sprintf(" .. (+%d)", len(items)-2*maxParts)
	}
	return strings.Join(items, " ")
}

// ---- SPI flash (xx25 series NOR flash and EEPROM) ----

// flashCmd is a serial flash command: its name, whether a 24-bit address
// follows it, the dummy bytes after the address, and what the rest of the
// transaction is: data the host writes (shown), data the chip answers
// (counted), or nothing (exactly the command and its address).
type flashCmd struct {
	name  string
	addr  bool
	dummy int
	rest  int // flashNone, flashWrite, flashRead
	erase int // an erase's block size (its address must be a multiple)
}

// What follows a flash command's address.
const (
	flashNone = iota
	flashWrite
	flashRead
)

// flashCmds are the commands of the common 25-series parts (JEDEC 216
// families: Winbond, Macronix, Micron, Adesto, ISSI, GigaDevice).
var flashCmds = map[int]flashCmd{
	0x01: {name: "WRSR", rest: flashWrite}, 0x02: {name: "PP", addr: true, rest: flashWrite},
	0x03: {name: "READ", addr: true, rest: flashRead}, 0x04: {name: "WRDI"}, 0x05: {name: "RDSR", rest: flashRead},
	0x06: {name: "WREN"}, 0x0B: {name: "FAST_READ", addr: true, dummy: 1, rest: flashRead},
	0x20: {name: "SE", addr: true, erase: 4096}, 0x2B: {name: "RDSCUR", rest: flashRead}, 0x2F: {name: "WRSCUR"},
	0x32: {name: "QPP", addr: true}, 0x35: {name: "RDSR2", rest: flashRead},
	0x3B: {name: "DREAD", addr: true, dummy: 1, rest: flashRead}, 0x4B: {name: "RDUID", dummy: 4, rest: flashRead},
	0x52: {name: "BE32", addr: true, erase: 32768}, 0x5A: {name: "RDSFDP", addr: true, dummy: 1, rest: flashRead},
	0x60: {name: "CE"}, 0x66: {name: "RSTEN"}, 0x6B: {name: "QREAD", addr: true, dummy: 1, rest: flashRead},
	0x70: {name: "ESRY"}, 0x80: {name: "DSRY"}, 0x82: {name: "WRITE1", addr: true, rest: flashWrite},
	0x85: {name: "WRITE2", addr: true, rest: flashWrite}, 0x90: {name: "REMS", addr: true, rest: flashRead},
	0x99: {name: "RST"}, 0x9F: {name: "RDID", rest: flashRead}, 0xAB: {name: "RES", dummy: 3, rest: flashRead},
	0xAD: {name: "CP", addr: true, rest: flashWrite}, 0xB1: {name: "ENSO"}, 0xB7: {name: "EN4B"},
	0xB9: {name: "DP"}, 0xBB: {name: "2READ"}, 0xC1: {name: "EXSO"}, 0xC7: {name: "CE"},
	0xD7: {name: "STATUS", rest: flashRead}, 0xD8: {name: "BE", addr: true, erase: 65536}, 0xE9: {name: "EX4B"},
	0xEB: {name: "4READ"}, 0xEF: {name: "REMS2", addr: true, rest: flashRead},
}

// flashNote reads one flash transaction (the host's side, MOSI).
// TRLC-LINKS: REQ-SDS-242
func flashNote(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, ok := flashCmds[int(b[0])]
	if !ok {
		return fmt.Sprintf("? 0x%s, %s", hx(int(b[0]), 2), plural(len(b), "byte")), true
	}
	if c.name == "RES" && len(b) == 1 { // the release from deep power-down alone
		return "RES (release)", false
	}
	head := 1
	if c.addr {
		head += 3
	}
	if len(b) < head+c.dummy {
		return c.name + " (cut)", true
	}
	note := c.name
	if c.addr {
		a := be(b[1:4])
		switch {
		case c.erase > 0:
			note = fmt.Sprintf("%s %dK 0x%s", c.name, c.erase/1024, hx(a, 6))
			if a%c.erase != 0 {
				return note + " (not aligned)", true
			}
		default:
			note = fmt.Sprintf("%s 0x%s", c.name, hx(a, 6))
		}
	}
	rest := b[head+c.dummy:]
	switch c.rest {
	case flashWrite:
		if len(rest) > 0 {
			note += fmt.Sprintf(", %s: %s", plural(len(rest), "byte"), hexList(rest, 8))
		}
	case flashRead:
		if len(rest) > 0 {
			note += ", " + plural(len(rest), "byte")
		}
	default:
		if len(rest) > 0 {
			return note + fmt.Sprintf(" (+%s)", plural(len(rest), "byte")), true
		}
	}
	return note, false
}

// flashFits reports whether a transaction is a flash command of a length it
// takes: an erase of an aligned block, a page program within its page, a
// command without operands alone.
// TRLC-LINKS: REQ-SDS-247
func flashFits(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, ok := flashCmds[int(b[0])]
	if !ok || c.name == "2READ" || c.name == "4READ" || c.name == "QPP" {
		return "", false
	}
	if c.name == "RES" && len(b) == 1 { // the release from deep power-down
		return c.name, true
	}
	head := 1 + c.dummy
	if c.addr {
		head += 3
	}
	switch {
	case c.rest == flashNone:
		if len(b) != head || (c.erase > 0 && be(b[1:4])%c.erase != 0) {
			return "", false
		}
	case c.name == "PP":
		if len(b) < head+1 || int(b[3])+len(b)-head > 256 {
			return "", false
		}
	case c.rest == flashRead:
		if len(b) < head+1 {
			return "", false
		}
	case c.name == "WRSR":
		if len(b) < 2 || len(b) > 3 {
			return "", false
		}
	default:
		if len(b) < head+1 {
			return "", false
		}
	}
	return c.name, true
}

// flashDetect: three or more whole transactions, every one a flash command
// of the length it takes, two kinds of command among them at least.
// TRLC-LINKS: REQ-SDS-247
func flashDetect(ps []Packet) string {
	var names []string
	seen := map[string]bool{}
	n := 0
	for _, p := range ps {
		if !p.Complete {
			continue
		}
		if anyBad(p) {
			return ""
		}
		name, ok := flashFits(p.Bytes)
		if !ok {
			return ""
		}
		n++
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if n < 3 || len(names) < 2 {
		return ""
	}
	return fmt.Sprintf("flash commands %s in %d transactions", strings.Join(names, " "), n)
}

// flashMatch selects a transaction by its command and address.
// TRLC-LINKS: REQ-SDS-248
func flashMatch(p Packet) ([]string, int) {
	if len(p.Bytes) == 0 {
		return nil, -1
	}
	n := 1
	if c, ok := flashCmds[int(p.Bytes[0])]; ok && c.addr && len(p.Bytes) >= 4 {
		n = 4
	}
	return sofToks(p.Bytes[:n]), -1
}

// ---- SD card in SPI mode ----

// sdCmdNames name the SD card's commands in SPI mode; sdACmdNames the
// application commands (after CMD55).
var sdCmdNames = map[int]string{0: "GO_IDLE_STATE", 1: "SEND_OP_COND", 6: "SWITCH_FUNC", 8: "SEND_IF_COND",
	9: "SEND_CSD", 10: "SEND_CID", 12: "STOP_TRANSMISSION", 13: "SEND_STATUS", 16: "SET_BLOCKLEN",
	17: "READ_SINGLE_BLOCK", 18: "READ_MULTIPLE_BLOCK", 23: "SET_BLOCK_COUNT", 24: "WRITE_BLOCK",
	25: "WRITE_MULTIPLE_BLOCK", 27: "PROGRAM_CSD", 28: "SET_WRITE_PROT", 29: "CLR_WRITE_PROT",
	30: "SEND_WRITE_PROT", 32: "ERASE_WR_BLK_START_ADDR", 33: "ERASE_WR_BLK_END_ADDR", 38: "ERASE",
	42: "LOCK_UNLOCK", 55: "APP_CMD", 56: "GEN_CMD", 58: "READ_OCR", 59: "CRC_ON_OFF"}

var sdACmdNames = map[int]string{6: "SET_BUS_WIDTH", 13: "SD_STATUS", 22: "SEND_NUM_WR_BLOCKS",
	23: "SET_WR_BLK_ERASE_COUNT", 41: "SD_SEND_OP_COND", 42: "SET_CLR_CARD_DETECT", 51: "SEND_SCR"}

// sdCRC7 is the CRC-7 (polynomial x^7 + x^3 + 1) of a command token's first
// five bytes, in the token's last byte with its end bit: (crc << 1) | 1.
// TRLC-LINKS: REQ-SDS-242
func sdCRC7(b []byte) int {
	crc := 0
	for _, v := range b {
		for k := 7; k >= 0; k-- {
			bit := int(v>>k) & 1
			fb := (crc >> 6 & 1) ^ bit
			crc = crc << 1 & 0x7f
			if fb == 1 {
				crc ^= 0x09
			}
		}
	}
	return crc
}

// sdState is the SD layer's memory across transactions: an APP_CMD makes
// the next command an application command.
type sdState struct{ app bool }

// sdNote reads the host's side of an SD transaction: command tokens (a start
// bit 0, the transmission bit 1, the index, a 32-bit argument and the CRC7),
// the 0xFF the host clocks while it waits, and a write's data block after its
// start token. A CRC7 that does not check is flagged; SPI mode lets a host
// leave the CRC off after CMD8, sending 0xFF or 0x01 in its place.
// TRLC-LINKS: REQ-SDS-242
func (st *sdState) note(b []byte) (string, bool) {
	var parts []string
	bad := false
	for i := 0; i < len(b); {
		v := b[i]
		switch {
		case v == 0xff:
			i++
		case v&0xc0 == 0x40 && i+6 <= len(b):
			t := b[i : i+6]
			idx := int(v & 0x3f)
			name, pre := sdCmdNames[idx], "CMD"
			if st.app {
				name, pre = sdACmdNames[idx], "ACMD"
			}
			st.app = !st.app && idx == 55
			s := fmt.Sprintf("%s%d", pre, idx)
			if name != "" {
				s += " " + name
			}
			if arg := be64(t[1:5]); arg != 0 {
				s += " 0x" + hx(arg, 8)
			}
			if c := int(t[5]); c != sdCRC7(t[:5])<<1|1 && c != 0xff && c != 0x01 {
				s += " (CRC7)"
				bad = true
			}
			parts = append(parts, s)
			i += 6
		case v == 0xfe || v == 0xfc: // a write's data token and block
			n := len(b) - i - 1
			parts = append(parts, fmt.Sprintf("data block, %s", plural(n, "byte")))
			i = len(b)
		case v == 0xfd:
			parts = append(parts, "stop token")
			i++
		default:
			if v&0xc0 == 0x40 {
				parts = append(parts, fmt.Sprintf("CMD%d (cut)", v&0x3f))
				bad = true
			} else {
				parts = append(parts, "? 0x"+hx(int(v), 2))
				bad = true
			}
			i = len(b)
		}
	}
	return joinNotes(parts), bad
}

// sdNotes reads a record's SD transactions in order.
// TRLC-LINKS: REQ-SDS-242
func sdNotes(ps []Packet) {
	st := &sdState{}
	eachNote(ps, st.note)
}

// sdDetect: two or more transactions that open (after the host's 0xFF
// fill) with a command token whose CRC7 checks: a token of six random bytes
// checks once in 128 times.
// TRLC-LINKS: REQ-SDS-247
func sdDetect(ps []Packet) string {
	n := 0
	for _, p := range ps {
		b := p.Bytes
		i := 0
		for i < len(b) && b[i] == 0xff {
			i++
		}
		if i+6 <= len(b) && b[i]&0xc0 == 0x40 && int(b[i+5]) == sdCRC7(b[i:i+5])<<1|1 {
			n++
		}
	}
	if n < 2 {
		return ""
	}
	return fmt.Sprintf("%d SD command tokens with a good CRC7", n)
}

// sdMatch selects a command by its token's index and argument.
// TRLC-LINKS: REQ-SDS-248
func sdMatch(p Packet) ([]string, int) {
	for i := 0; i+5 <= len(p.Bytes); i++ {
		if p.Bytes[i]&0xc0 == 0x40 {
			return hexToks(p.Bytes[i : i+5]), -1
		}
	}
	return nil, -1
}

// ---- register-map chips: a command byte, then the register's data ----

// regData writes the data of a register access the host writes, or counts
// the bytes it clocks for a read.
// TRLC-LINKS: REQ-SDS-242
func regData(write bool, d []byte) string {
	switch {
	case len(d) == 0:
		return ""
	case write:
		return " = " + hexList(d, 8)
	}
	return ", " + plural(len(d), "byte")
}

// reversed is bytes last first (a register sent least significant byte first).
// TRLC-LINKS: REQ-SDS-242
func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[len(b)-1-i] = v
	}
	return out
}

// nrf24Note reads an nRF24L01(+) command: R_REGISTER 000aaaaa and
// W_REGISTER 001aaaaa (multi-byte registers least significant byte first),
// the payload commands and the single-byte ones.
// TRLC-LINKS: REQ-SDS-242
func nrf24Note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, d := int(b[0]), b[1:]
	switch {
	case c&0xe0 == 0x00 || c&0xe0 == 0x20:
		w := c&0xe0 == 0x20
		r := c & 0x1f
		name := regName(nrf24Regs, r)
		op := "R_REGISTER "
		if w {
			op = "W_REGISTER "
			if len(d) > 1 {
				return op + name + " = " + hexList(reversed(d), 8), false
			}
		}
		return op + name + regData(w, d), false
	case c == 0x50:
		return "ACTIVATE" + regData(true, d), len(d) != 1 || d[0] != 0x73
	case c == 0x61:
		return "R_RX_PAYLOAD" + regData(false, d), false
	case c == 0x60:
		return "R_RX_PL_WID", false
	case c == 0xa0, c == 0xb0:
		op := map[bool]string{true: "W_TX_PAYLOAD", false: "W_TX_PAYLOAD_NOACK"}[c == 0xa0]
		return fmt.Sprintf("%s %s %s", op, plural(len(d), "byte"), quoted(d, 16)), false
	case c&0xf8 == 0xa8:
		return fmt.Sprintf("W_ACK_PAYLOAD pipe %d %s %s", c&7, plural(len(d), "byte"), quoted(d, 16)), false
	case c == 0xe1:
		return "FLUSH_TX", false
	case c == 0xe2:
		return "FLUSH_RX", false
	case c == 0xe3:
		return "REUSE_TX_PL", false
	case c == 0xff:
		return "NOP", false
	}
	return "? 0x" + hx(c, 2), true
}

// nrf905Note reads an nRF905 command.
// TRLC-LINKS: REQ-SDS-242
func nrf905Note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, d := int(b[0]), b[1:]
	switch {
	case c&0xf0 == 0x00:
		return fmt.Sprintf("W_CONFIG %d%s", c&0x0f, regData(true, d)), c&0x0f > 9
	case c&0xf0 == 0x10:
		return fmt.Sprintf("R_CONFIG %d%s", c&0x0f, regData(false, d)), c&0x0f > 9
	case c == 0x20:
		return "W_TX_PAYLOAD" + regData(true, d), false
	case c == 0x21:
		return "R_TX_PAYLOAD" + regData(false, d), false
	case c == 0x22:
		return "W_TX_ADDRESS" + regData(true, d), false
	case c == 0x23:
		return "R_TX_ADDRESS" + regData(false, d), false
	case c == 0x24:
		return "R_RX_PAYLOAD" + regData(false, d), false
	case c&0xf0 == 0x80:
		if len(d) < 1 {
			return "CHANNEL_CONFIG (cut)", true
		}
		ch := (c&1)<<8 | int(d[0])
		band := map[bool]string{false: "433MHz", true: "868/915MHz"}[c&2 != 0]
		pwr := []string{"-10dBm", "-2dBm", "+6dBm", "+10dBm"}[c>>2&3]
		return fmt.Sprintf("CHANNEL_CONFIG CH %d %s %s", ch, band, pwr), false
	}
	return "? 0x" + hx(c, 2), true
}

// cc1101Note reads a CC1101 header byte: R/W, burst, a 6-bit address; the
// addresses 0x30..0x3D are command strobes, or status registers when read
// in burst.
// TRLC-LINKS: REQ-SDS-242
func cc1101Note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, d := int(b[0]), b[1:]
	a := c & 0x3f
	if a >= 0x30 && a <= 0x3d {
		if c&0x40 == 0 {
			s, ok := cc1101Strobes[a]
			if !ok {
				return "STROBE 0x" + hx(a, 2), true
			}
			return "STROBE " + s, false
		}
		if c&0x80 != 0 {
			return "STATUS READ " + regName(cc1101Regs, a) + regData(false, d), false
		}
		return "? 0x" + hx(c, 2), true
	}
	op := []string{"WRITE ", "BURST WRITE ", "READ ", "BURST READ "}[c>>6]
	return op + regName(cc1101Regs, a) + regData(c&0x80 == 0, d), false
}

// rfm12Note reads an RFM12 transaction: 16-bit commands, the top bits of
// the first byte naming each.
// TRLC-LINKS: REQ-SDS-242
func rfm12Note(b []byte) (string, bool) {
	var parts []string
	bad := false
	for i := 0; i < len(b); i += 2 {
		if i+1 >= len(b) {
			parts = append(parts, "(cut)")
			bad = true
			break
		}
		c, v := int(b[i]), int(b[i+1])
		var s string
		switch {
		case c == 0x80:
			s = fmt.Sprintf("CONFIG %sMHz %spF", []string{"315", "433", "868", "915"}[v>>4&3], fixed(int64(85+(v&0xf)*5), 10, 1))
		case c == 0x82:
			s = "POWER 0x" + hx(v, 2)
		case c&0xf0 == 0xa0:
			s = fmt.Sprintf("FREQ %d", (c&0xf)<<8|v)
		case c == 0xc6:
			den := int64(29 * (v&0x7f + 1) * (1 + 7*(v>>7)))
			s = "DATA RATE " + fixed(10000, den, 3) + "kbps"
		case c&0xf8 == 0x90:
			s = "RX CTRL BW " + []string{"Reserved", "400kHz", "340kHz", "270kHz", "200kHz", "134kHz", "67kHz", "Reserved"}[v>>5]
		case c == 0xc2:
			s = "DATA FILTER"
		case c == 0xca:
			s = fmt.Sprintf("FIFO RESET level %d", v>>4)
		case c == 0xce:
			s = "SYNC 0x" + hx(v, 2)
		case c == 0xb0:
			s = "FIFO READ"
		case c == 0xc4:
			s = "AFC " + []string{"Off", "Once", "During receiving", "Always"}[v>>6]
		case c&0xfe == 0x98:
			s = fmt.Sprintf("TX CTRL %dkHz", 15*(v>>4))
		case c == 0xcc:
			s = "PLL"
		case c == 0xb8:
			s = "TX BYTE 0x" + hx(v, 2)
		case c == 0xfe:
			s = "RESET"
		case c&0xe0 == 0xe0:
			s = "WAKE-UP TIMER"
		case c == 0xc8:
			s = "LOW DUTY CYCLE"
		case c == 0xc0:
			s = "LOW BATTERY " + fixed(int64(225+(v&0xf)*10), 100, 2) + "V"
		case c == 0x00:
			s = "STATUS READ"
		default:
			s = "? 0x" + hx(c, 2) + hx(v, 2)
			bad = true
		}
		parts = append(parts, s)
	}
	return joinNotes(parts), bad
}

// mrf24Note reads MRF24J40 accesses: a short address (0aaaaaaw, one data
// byte) or a long one (1aaaaaaa aaaw0000, one data byte); a transaction may
// hold several.
// TRLC-LINKS: REQ-SDS-242
func mrf24Note(b []byte) (string, bool) {
	var parts []string
	for i := 0; i < len(b); {
		if b[i]&0x80 == 0 {
			if i+2 > len(b) {
				parts = append(parts, "(cut)")
				return joinNotes(parts), true
			}
			r := int(b[i]>>1) & 0x3f
			w := b[i]&1 == 1
			parts = append(parts, map[bool]string{true: "W ", false: "R "}[w]+regName(mrf24Short, r)+regData(w, b[i+1:i+2]))
			i += 2
			continue
		}
		if i+3 > len(b) {
			parts = append(parts, "(cut)")
			return joinNotes(parts), true
		}
		dw := int(b[i])<<8 | int(b[i+1])
		w := dw&0x10 != 0
		r := dw >> 5 & 0x3ff
		var name string
		switch {
		case r >= 0x300:
			name = "RX[0x" + hx(r-0x300, 2) + "]"
		case r >= 0x2c0:
			name = "Reserved 0x" + hx(r, 3)
		case r >= 0x280:
			name = "KEY[0x" + hx(r-0x280, 2) + "]"
		case r >= 0x200:
			name = regName(mrf24Long, r)
		case r >= 0x180:
			name = "TX GTS2[0x" + hx(r-0x180, 2) + "]"
		case r >= 0x100:
			name = "TX GTS1[0x" + hx(r-0x100, 2) + "]"
		case r >= 0x80:
			name = "TX beacon[0x" + hx(r-0x80, 2) + "]"
		default:
			name = "TX[0x" + hx(r, 2) + "]"
		}
		parts = append(parts, map[bool]string{true: "W ", false: "R "}[w]+name+regData(w, b[i+2:i+3]))
		i += 3
	}
	return joinNotes(parts), false
}

// enc28j60Banks are the ENC28J60's four control register banks; addresses
// 0x1B..0x1F (EIE, EIR, ESTAT, ECON2, ECON1) are in every bank.
var enc28j60Banks = [4]map[int]string{enc28j60Bank0, enc28j60Bank1, enc28j60Bank2, enc28j60Bank3}

// encState is the ENC28J60 layer's memory: the bank ECON1's BSEL bits
// select (-1 not yet seen).
type encState struct{ bank int }

// encReg names control register a in the bank selected.
// TRLC-LINKS: REQ-SDS-242
func (st *encState) reg(a int) string {
	if a >= 0x1b {
		return enc28j60Bank0[a]
	}
	if st.bank < 0 {
		return "bank? 0x" + hx(a, 2)
	}
	return regName(enc28j60Banks[st.bank], a)
}

// note reads an ENC28J60 opcode (3 bits) and argument (5 bits): read and
// write control register, read and write buffer memory, bit field set and
// clear, system reset; a write or bit operation on ECON1 moves the bank.
// TRLC-LINKS: REQ-SDS-242
func (st *encState) note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, d := int(b[0]), b[1:]
	a := c & 0x1f
	switch c >> 5 {
	case 0:
		return "RCR " + st.reg(a) + regData(false, d), false
	case 1:
		if a == 0x1a {
			return "RBM" + regData(false, d), false
		}
	case 2:
		s := "WCR " + st.reg(a) + regData(true, d)
		if a == 0x1f && len(d) > 0 {
			st.bank = int(d[0]) & 3
		}
		return s, len(d) == 0
	case 3:
		if a == 0x1a {
			return "WBM " + plural(len(d), "byte") + regData(true, d), false
		}
	case 4:
		s := "BFS " + st.reg(a) + regData(true, d)
		if a == 0x1f && len(d) > 0 && st.bank >= 0 {
			st.bank |= int(d[0]) & 3
		}
		return s, len(d) == 0
	case 5:
		s := "BFC " + st.reg(a) + regData(true, d)
		if a == 0x1f && len(d) > 0 && st.bank >= 0 {
			st.bank &^= int(d[0]) & 3
		}
		return s, len(d) == 0
	case 7:
		if c == 0xff {
			return "SRC (soft reset)", false
		}
	}
	return "? 0x" + hx(c, 2), true
}

// encNotes reads a record's ENC28J60 transactions in order.
// TRLC-LINKS: REQ-SDS-242
func encNotes(ps []Packet) {
	st := &encState{bank: -1}
	eachNote(ps, st.note)
}

// st25State is the ST25R39xx layer's memory: a Space-B or test access
// prefix, which redirects the register access after it.
type st25State struct{ space int }

// note reads an ST25R39xx transaction: register write 00aaaaaa and read
// 01aaaaaa (space A, or B or test after their prefix), FIFO load and read,
// the passive target memory loads and read, direct commands 11xxxxxx.
// TRLC-LINKS: REQ-SDS-242
func (st *st25State) note(b []byte) (string, bool) {
	var parts []string
	bad := false
	for i := 0; i < len(b); i++ {
		c := int(b[i])
		d := b[i+1:]
		if c <= 0x7f {
			t, sp := st25RegsA, ""
			switch st.space {
			case 1:
				t, sp = st25RegsB, "B "
			case 2:
				t, sp = st25RegsTest, "T "
			}
			st.space = 0
			w := c&0x40 == 0
			op := map[bool]string{true: "WRITE ", false: "READ "}[w]
			parts = append(parts, op+sp+regName(t, c&0x3f)+regData(w, d))
			break
		}
		switch c {
		case 0x80:
			parts = append(parts, "FIFO LOAD"+regData(true, d))
		case 0x9f:
			parts = append(parts, "FIFO READ"+regData(false, d))
		case 0xa0, 0xa8, 0xac:
			parts = append(parts, map[int]string{0xa0: "PT LOAD A", 0xa8: "PT LOAD F", 0xac: "PT LOAD TSN"}[c]+regData(true, d))
		case 0xbf:
			parts = append(parts, "PT READ"+regData(false, d))
		case 0xfb:
			st.space = 1
			parts = append(parts, "SPACE B")
			continue
		case 0xfc:
			st.space = 2
			parts = append(parts, "TEST ACCESS")
			continue
		default:
			if n, ok := st25Cmds[c]; ok && c >= 0xc0 {
				parts = append(parts, "CMD "+n)
				continue // a direct command may be followed by another
			}
			parts = append(parts, "? 0x"+hx(c, 2))
			bad = true
		}
		break
	}
	return joinNotes(parts), bad
}

// st25Notes reads a record's ST25R39xx transactions in order.
// TRLC-LINKS: REQ-SDS-242
func st25Notes(ps []Packet) {
	st := &st25State{}
	eachNote(ps, st.note)
}

// adxl345Note reads an ADXL345 access: R/W (1 read), MB (multiple bytes),
// a 6-bit register address, the data of consecutive registers after it.
// TRLC-LINKS: REQ-SDS-242
func adxl345Note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, d := int(b[0]), b[1:]
	w := c&0x80 == 0
	a := c & 0x3f
	op := map[bool]string{true: "W ", false: "R "}[w]
	s := op + regName(adxl345Regs, a)
	if w && len(d) == 1 {
		s += " = " + hx(int(d[0]), 2) + adxl345Value(a, int(d[0]))
		return s, false
	}
	return s + regData(w, d), c&0x40 == 0 && len(d) > 1
}

// adxl345Value interprets a register written: the output data rate, the
// range, the power control's measure bit.
// TRLC-LINKS: REQ-SDS-242
func adxl345Value(a, v int) string {
	switch a {
	case 0x2c:
		rates := []string{"0.10", "0.20", "0.39", "0.78", "1.56", "3.13", "6.25", "12.5", "25", "50", "100", "200",
			"400", "800", "1600", "3200"}
		return " (" + rates[v&0xf] + " Hz)"
	case 0x31:
		return fmt.Sprintf(" (+-%dg)", 2<<(v&3))
	case 0x2d:
		if v&0x08 != 0 {
			return " (measure)"
		}
		return " (standby)"
	}
	return ""
}

// adnsNote reads ADNS-5020 accesses: pairs of a register byte (bit 7 set
// for a write) and its data.
// TRLC-LINKS: REQ-SDS-242
func adnsNote(b []byte) (string, bool) {
	var parts []string
	for i := 0; i < len(b); i += 2 {
		c := int(b[i])
		r := c & 0x7f
		name := regName(adns5020Regs, r)
		if r > 0x63 {
			name = "Unknown 0x" + hx(r, 2)
		}
		if i+1 >= len(b) {
			parts = append(parts, name+" (cut)")
			return joinNotes(parts), true
		}
		if c&0x80 != 0 {
			parts = append(parts, "W "+name+" = "+hx(int(b[i+1]), 2))
		} else {
			parts = append(parts, "R "+name)
		}
	}
	return joinNotes(parts), false
}

// ade7758Note reads an ADE7758 access: bit 7 set for a write, the register,
// then its data, 1 to 3 bytes as wide as the register.
// TRLC-LINKS: REQ-SDS-242
func ade7758Note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c, d := int(b[0]), b[1:]
	r := c & 0x7f
	name, ok := ade7758Regs[r]
	if !ok {
		return "? register 0x" + hx(r, 2), true
	}
	want := (ade7758Bits[r] + 7) / 8
	w := c&0x80 != 0
	op := map[bool]string{true: "W ", false: "R "}[w]
	if len(d) < want {
		return op + name + " (short)", true
	}
	if w {
		return op + name + " = 0x" + hx(be(d[:want]), 2*want), len(d) != want
	}
	return op + name + ", " + plural(want, "byte"), len(d) != want
}

// adf435xNote reads ADF4350/1 register writes: 32-bit words most
// significant bit first, the register number in the low three bits.
// TRLC-LINKS: REQ-SDS-242
func adf435xNote(b []byte) (string, bool) {
	if len(b)%4 != 0 || len(b) == 0 {
		return fmt.Sprintf("? %s (32-bit words)", plural(len(b), "byte")), true
	}
	var parts []string
	for i := 0; i < len(b); i += 4 {
		w := be64(b[i : i+4])
		f := func(off, n int) int { return int(w >> off & (1<<n - 1)) }
		var s string
		switch w & 7 {
		case 0:
			s = fmt.Sprintf("R0 INT %d FRAC %d", f(15, 16), f(3, 12))
		case 1:
			s = fmt.Sprintf("R1 MOD %d phase %d prescaler %s", f(3, 12), f(15, 12), []string{"4/5", "8/9"}[f(27, 1)])
		case 2:
			s = fmt.Sprintf("R2 R %d CP %d MUXOUT %d", f(14, 10), f(9, 4), f(26, 3))
		case 3:
			s = fmt.Sprintf("R3 clock divider %d", f(3, 12))
		case 4:
			s = fmt.Sprintf("R4 RF divider /%d power %ddBm out %s", 1<<f(20, 3), []int{-4, -1, 2, 5}[f(3, 2)],
				[]string{"off", "on"}[f(5, 1)])
		case 5:
			s = fmt.Sprintf("R5 LD pin %d", f(22, 2))
		default:
			s = fmt.Sprintf("R%d 0x%s", w&7, hx(w, 8))
		}
		parts = append(parts, s)
	}
	return joinNotes(parts), false
}

// ad5626Note reads an AD5626 load: the 12-bit code of a 16-bit transfer
// (four leading bits ignored), at 1 mV a step of its 4.095 V range.
// TRLC-LINKS: REQ-SDS-242
func ad5626Note(b []byte) (string, bool) {
	if len(b) != 2 {
		return fmt.Sprintf("? %s (16-bit loads)", plural(len(b), "byte")), true
	}
	v := be(b) & 0xfff
	return fmt.Sprintf("DAC %s V (0x%s)", fixed(int64(v), 1000, 3), hx(v, 3)), false
}

// ad79x0Note reads an AD7910/AD7920 conversion on MISO: four leading zeros
// and 12 bits; fewer than 10 clocks power the converter down, and an all-ones
// code is the conversion after power-up, invalid.
// TRLC-LINKS: REQ-SDS-242
func ad79x0Note(b []byte) (string, bool) {
	switch {
	case len(b) == 1:
		return "ADC power-down", false
	case len(b) != 2:
		return fmt.Sprintf("? %s", plural(len(b), "byte")), true
	}
	v := be(b)
	if v == 0xfff {
		return "ADC power-up (invalid)", false
	}
	return fmt.Sprintf("ADC %d/4095 (%s Vref)", v&0xfff, fixed(int64(v&0xfff), 4095, 4)), v > 0xfff
}

// ltc242xNote reads an LTC2421/2 conversion on MISO: 24 bits, end of
// conversion (0 ready), the channel, the sign and 20 bits; the reading is a
// share of the reference, -2 to +2 (sign bit included as the offset).
// TRLC-LINKS: REQ-SDS-242
func ltc242xNote(b []byte) (string, bool) {
	if len(b) != 3 {
		return fmt.Sprintf("? %s (24-bit conversions)", plural(len(b), "byte")), true
	}
	v := be(b)
	x := int64(v&0x3fffff) - 1<<21
	return fmt.Sprintf("CH%d %s Vref", v>>22&1, fixed(x, 0xfffff, 6)), v&0x800000 != 0
}

// max72xxRegs name the MAX7219/7221 control registers.
var max72xxRegs = map[int]string{0: "No-op", 9: "Decode", 10: "Intensity", 11: "Scan limit", 12: "Shutdown", 15: "Display test"}

// max72xxNote reads MAX7219/7221 writes: (register, data) pairs, one per
// chip of a daisy chain, the first pair for the last chip.
// TRLC-LINKS: REQ-SDS-242
func max72xxNote(b []byte) (string, bool) {
	if len(b) == 0 || len(b)%2 != 0 {
		return fmt.Sprintf("? %s (pairs)", plural(len(b), "byte")), len(b) > 0
	}
	var parts []string
	bad := false
	for i := 0; i < len(b); i += 2 {
		r, v := int(b[i]&0x0f), int(b[i+1])
		var s string
		switch {
		case r >= 1 && r <= 8:
			s = fmt.Sprintf("Digit %d = %s", r, hx(v, 2))
		case r == 10:
			s = fmt.Sprintf("Intensity %d", v&0xf)
		case r == 11:
			s = fmt.Sprintf("Scan limit %d", 1+v&7)
		case r == 12:
			s = "Shutdown " + map[bool]string{true: "off", false: "on"}[v&1 == 1]
		case r == 15:
			s = "Display test " + map[bool]string{true: "on", false: "off"}[v&1 == 1]
		case r == 9:
			s = "Decode " + hx(v, 2)
		case r == 0:
			s = "No-op"
		default:
			s = "? register " + hx(r, 2)
			bad = true
		}
		if len(b) > 2 {
			s = fmt.Sprintf("chip %d: %s", i/2+1, s)
		}
		parts = append(parts, s)
	}
	return joinNotes(parts), bad
}

// avrNote reads AVR serial programming instructions, four bytes each.
// TRLC-LINKS: REQ-SDS-242
func avrNote(b []byte) (string, bool) {
	var parts []string
	bad := false
	for i := 0; i < len(b); i += 4 {
		if i+4 > len(b) {
			parts = append(parts, "(cut)")
			return joinNotes(parts), true
		}
		c := b[i : i+4]
		var s string
		switch {
		case c[0] == 0xac && c[1] == 0x53:
			s = "PROGRAMMING ENABLE"
		case c[0] == 0xac && c[1]&0x80 != 0:
			s = "CHIP ERASE"
		case c[0] == 0x50 && c[1] == 0x00 && c[2] == 0x00:
			s = "READ FUSE"
		case c[0] == 0x58 && c[1] == 0x08 && c[2] == 0x00:
			s = "READ HIGH FUSE"
		case c[0] == 0x50 && c[1] == 0x08 && c[2] == 0x00:
			s = "READ EXT FUSE"
		case c[0] == 0x30 && c[2] <= 2:
			s = fmt.Sprintf("READ SIGNATURE %d", c[2])
		case c[0] == 0x58 && c[1] == 0x00:
			s = "READ LOCK"
		case c[0] == 0xa0 && c[1]&0xc0 == 0:
			s = fmt.Sprintf("READ EEPROM 0x%s", hx(int(c[1]&1)<<8|int(c[2]), 3))
		case (c[0] == 0x20 || c[0] == 0x28) && c[1]&0xf0 == 0:
			s = fmt.Sprintf("READ FLASH %s 0x%s", map[bool]string{true: "H", false: "L"}[c[0] == 0x28], hx(int(c[1]&0x0f)<<8|int(c[2]), 3))
		case (c[0] == 0x40 || c[0] == 0x48) && c[1]&0xf0 == 0:
			s = fmt.Sprintf("LOAD PAGE %s 0x%s = %s", map[bool]string{true: "H", false: "L"}[c[0] == 0x48], hx(int(c[2]&0x1f), 2), hx(int(c[3]), 2))
		case c[0] == 0x4c && c[1]&0xf0 == 0:
			s = fmt.Sprintf("WRITE PAGE 0x%s", hx(int(c[1]&0x0f)<<3+int(c[2])<<5, 2))
		default:
			s = "? " + hexList(c, 4)
			bad = true
		}
		parts = append(parts, s)
	}
	return joinNotes(parts), bad
}

// avrDetect: a Programming Enable instruction (AC 53) and every whole
// transaction made of four-byte instructions the programmer sends.
// TRLC-LINKS: REQ-SDS-247
func avrDetect(ps []Packet) string {
	enable := false
	n := 0
	for _, p := range ps {
		if !p.Complete {
			continue
		}
		if _, bad := avrNote(p.Bytes); bad || len(p.Bytes) == 0 {
			return ""
		}
		if len(p.Bytes) >= 2 && p.Bytes[0] == 0xac && p.Bytes[1] == 0x53 {
			enable = true
		}
		n++
	}
	if !enable || n < 2 {
		return ""
	}
	return fmt.Sprintf("AVR Programming Enable (AC 53) among %d instructions", n)
}

// x2444Note reads an X2444 instruction: 1AAAAccc (the address in bits 6..3,
// bit 0 a READ's don't-care), a READ's or WRITE's 16-bit word after it.
// TRLC-LINKS: REQ-SDS-242
func x2444Note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	c := int(b[0])
	names := map[int]string{0x80: "WRDS", 0x81: "STO", 0x82: "SLEEP", 0x83: "WRITE", 0x84: "WREN", 0x85: "RCL", 0x86: "READ", 0x87: "READ"}
	name, ok := names[c&0x87]
	if !ok {
		return "? 0x" + hx(c, 2), true
	}
	if len(b) == 1 {
		return name, false
	}
	a := c >> 3 & 0x0f
	if name == "WRITE" {
		return fmt.Sprintf("WRITE 0x%X = 0x%s", a, hx(be(b[1:min(3, len(b))]), 4)), len(b) != 3
	}
	if name == "READ" {
		return fmt.Sprintf("READ 0x%X", a), len(b) != 3
	}
	return name, true
}

// rgbNote reads RGB LED data: three bytes a LED, red green blue.
// TRLC-LINKS: REQ-SDS-242
func rgbNote(b []byte) (string, bool) {
	if len(b) < 3 {
		return fmt.Sprintf("? %s (3 a LED)", plural(len(b), "byte")), len(b) > 0
	}
	var parts []string
	for i := 0; i+3 <= len(b); i += 3 {
		if len(parts) == 6 {
			parts = append(parts, "..")
			break
		}
		parts = append(parts, "#"+hx(be(b[i:i+3]), 6))
	}
	s := fmt.Sprintf("%s: %s", plural(len(b)/3, "LED"), strings.Join(parts, " "))
	return s, len(b)%3 != 0
}

// nesNote reads NES gamepad states on MISO: a button pressed reads 0, in the
// order A B Select Start Up Down Left Right from the first bit clocked.
// TRLC-LINKS: REQ-SDS-242
func nesNote(b []byte) (string, bool) {
	var parts []string
	names := []string{"A", "B", "Select", "Start", "Up", "Down", "Left", "Right"}
	for _, v := range b {
		switch v {
		case 0xff:
			parts = append(parts, "no button")
		case 0x00:
			parts = append(parts, "not connected")
		default:
			var on []string
			for k := 0; k < 8; k++ {
				if v>>(7-k)&1 == 0 {
					on = append(on, names[k])
				}
			}
			parts = append(parts, strings.Join(on, "+"))
		}
	}
	return joinNotes(parts), false
}

// ssi32Note reads SSI32 messages (host side): an acknowledge of four bytes
// (bit 7 of the first set), or a control message: control, LUN, size, CRC,
// then size bytes of data.
// TRLC-LINKS: REQ-SDS-242
func ssi32Note(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	if b[0]&0x80 != 0 {
		return "ACK 0x" + hx(int(b[0]), 2), len(b) != 4
	}
	if len(b) < 4 {
		return "CTRL (cut)", true
	}
	n := int(b[2])
	s := fmt.Sprintf("CTRL 0x%s LUN 0x%s SIZE %d CRC 0x%s", hx(int(b[0]), 2), hx(int(b[1]), 2), n, hx(int(b[3]), 2))
	if d := b[4:min(len(b), 4+n)]; len(d) > 0 {
		s += ": " + hexList(d, 8)
	}
	return s, 4+n > len(b)
}
