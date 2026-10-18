// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import "fmt"

// The 1-Wire, MDIO and USB layers (REQ-SDS-245, REQ-SDS-246).

// ---- 1-Wire network layer and devices ----

// owROMCmds name the 1-Wire ROM commands and whether a ROM code follows.
var owROMCmds = map[int]struct {
	name string
	rom  bool
}{0x33: {"READ ROM", true}, 0x0f: {"READ ROM", true}, 0x55: {"MATCH ROM", true}, 0xf0: {"SEARCH ROM", true},
	0xec: {"ALARM SEARCH", true}, 0xcc: {"SKIP ROM", false}, 0x3c: {"OD SKIP ROM", false},
	0x69: {"OD MATCH ROM", true}, 0xa5: {"RESUME", false}}

// owTxn is one 1-Wire transaction as the network layer reads it: the ROM
// command, the ROM code (family, serial, CRC) when one follows, and the
// function bytes after.
type owTxn struct {
	cmd    string
	rom    []byte
	romBad bool
	fn     []byte
	ok     bool // a ROM command the network layer knows
}

// owRead reads a reset-opened packet's bytes as a transaction.
// TRLC-LINKS: REQ-SDS-245
func owRead(p Packet) owTxn {
	b := p.Bytes
	if len(b) == 0 {
		return owTxn{}
	}
	c, ok := owROMCmds[int(b[0])]
	if !ok {
		return owTxn{cmd: "ROM 0x" + hx(int(b[0]), 2), fn: b[1:]}
	}
	t := owTxn{cmd: c.name, ok: true, fn: b[1:]}
	if c.rom {
		if len(b) < 9 {
			t.rom, t.fn, t.romBad = b[1:], nil, true
			return t
		}
		t.rom, t.fn = b[1:9], b[9:]
		for k := 1; k < 9 && k < len(p.ByteErr); k++ {
			t.romBad = t.romBad || p.ByteErr[k]
		}
	}
	return t
}

// owROMText writes a ROM code as family-serial, the serial most
// significant byte first ("28-0000056A1B2C").
// TRLC-LINKS: REQ-SDS-245
func owROMText(r []byte) string {
	if len(r) < 8 {
		return "(cut ROM)"
	}
	return hx(int(r[0]), 2) + "-" + hx(int(r[6]), 2) + hx(int(r[5]), 2) + hx(int(r[4]), 2) + hx(int(r[3]), 2) +
		hx(int(r[2]), 2) + hx(int(r[1]), 2)
}

// owHead writes a transaction's ROM command and code.
// TRLC-LINKS: REQ-SDS-245
func owHead(t owTxn) string {
	s := t.cmd
	if t.rom != nil {
		s += " " + owROMText(t.rom)
		if t.romBad {
			s += " (CRC)"
		}
	}
	return s
}

// owNotes writes each 1-Wire transaction with dev, the device layer's
// reading of its function bytes ("" leaves them as hex).
// TRLC-LINKS: REQ-SDS-245
func owNotes(ps []Packet, dev func(fn []byte) (string, bool)) {
	for i := range ps {
		p := &ps[i]
		if len(p.Bytes) == 0 {
			if p.Err == "" {
				p.Note = "RESET"
			}
			continue
		}
		t := owRead(*p)
		s := owHead(t)
		bad := t.romBad || !t.ok
		if len(t.fn) > 0 {
			f, fb := "", false
			if dev != nil {
				f, fb = dev(t.fn)
			}
			if f == "" {
				f = hexList(t.fn, 10)
			}
			s += ": " + f
			bad = bad || fb
		}
		p.Note, p.NoteErr = s, bad
	}
}

// owNetNotes is the network layer alone.
// TRLC-LINKS: REQ-SDS-245
func owNetNotes(ps []Packet) { owNotes(ps, nil) }

// owFamilies returns the family codes the record's ROM codes name.
// TRLC-LINKS: REQ-SDS-247
func owFamilies(ps []Packet) map[int]bool {
	f := map[int]bool{}
	for _, p := range ps {
		if t := owRead(p); t.ok && len(t.rom) == 8 && !t.romBad {
			f[int(t.rom[0])] = true
		}
	}
	return f
}

// owNetDetect: a ROM command after a reset with a presence pulse.
// TRLC-LINKS: REQ-SDS-247
func owNetDetect(ps []Packet) string {
	for _, p := range ps {
		if t := owRead(p); t.ok && p.Complete {
			return "1-Wire ROM command " + t.cmd
		}
	}
	return ""
}

// owFamilyDetect detects a device layer by the family code in a ROM code.
// TRLC-LINKS: REQ-SDS-247
func owFamilyDetect(ps []Packet, name string, codes ...int) string {
	f := owFamilies(ps)
	for _, c := range codes {
		if f[c] {
			return fmt.Sprintf("1-Wire ROM code of family 0x%s (%s)", hx(c, 2), name)
		}
	}
	return ""
}

// owMatch: the ROM command (and the ROM code) that opens the transaction.
// TRLC-LINKS: REQ-SDS-248
func owMatch(p Packet) ([]string, int) {
	t := owRead(p)
	if !t.ok {
		return nil, -1
	}
	n := 1
	if len(t.rom) == 8 {
		n = 9
	}
	return sofToks(p.Bytes[:n]), -1
}

// crc8Maxim is the 1-Wire CRC-8 (polynomial x^8+x^5+x^4+1, reflected).
// TRLC-LINKS: REQ-SDS-245
func crc8Maxim(b []byte) int {
	crc := 0
	for _, v := range b {
		crc ^= int(v)
		for k := 0; k < 8; k++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8c
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// crc16Maxim is the 1-Wire CRC-16 (polynomial 0x8005 reflected, inverted).
// TRLC-LINKS: REQ-SDS-245
func crc16Maxim(b []byte) int {
	crc := 0
	for _, v := range b {
		x := int(v)
		for k := 0; k < 8; k++ {
			if (x^crc)&1 != 0 {
				crc = crc>>1 ^ 0xa001
			} else {
				crc >>= 1
			}
			x >>= 1
		}
	}
	return crc ^ 0xffff
}

// ds28ea00Fn reads DS28EA00 function commands (the DS18B20 shares its
// scratchpad): Convert T, the scratchpad read with the temperature in
// sixteenths of a degree and its CRC-8, the scratchpad write, and the rest.
// TRLC-LINKS: REQ-SDS-245
func ds28ea00Fn(fn []byte) (string, bool) {
	names := map[int]string{0x4e: "Write scratchpad", 0xbe: "Read scratchpad", 0x48: "Copy scratchpad",
		0x44: "Convert T", 0xb4: "Read power mode", 0xb8: "Recall EEPROM", 0xf5: "PIO read", 0xa5: "PIO write",
		0x99: "Chain"}
	name, ok := names[int(fn[0])]
	if !ok {
		return "? function 0x" + hx(int(fn[0]), 2), true
	}
	d := fn[1:]
	switch fn[0] {
	case 0xbe:
		if len(d) < 2 {
			return name, false
		}
		t := int(int16(uint16(d[1])<<8 | uint16(d[0])))
		s := name + " " + fixed(int64(t), 16, 4) + " C"
		if len(d) >= 9 {
			if crc8Maxim(d[:8]) != int(d[8]) {
				return s + " CRC bad", true
			}
			s += " CRC ok"
		}
		return s, false
	case 0x4e:
		return name + " " + hexList(d, 3), false
	case 0xa5:
		if len(d) >= 2 {
			return fmt.Sprintf("%s 0x%s", name, hx(int(d[0]), 2)), d[1] != ^d[0]
		}
	}
	if len(d) > 0 {
		return name + " " + hexList(d, 8), false
	}
	return name, false
}

// ds2408Fn reads DS2408 function commands: the PIO register read (target
// address and data), channel access read and write (the data and its
// complement, AA for done), the conditional search register write, the
// activity latch reset.
// TRLC-LINKS: REQ-SDS-245
func ds2408Fn(fn []byte) (string, bool) {
	d := fn[1:]
	switch fn[0] {
	case 0xf0, 0xcc:
		name := map[byte]string{0xf0: "Read PIO registers", 0xcc: "Write cond. search register"}[fn[0]]
		if len(d) < 2 {
			return name, false
		}
		s := fmt.Sprintf("%s @0x%s", name, hx(int(d[1])<<8|int(d[0]), 4))
		if len(d) > 2 {
			s += ": " + hexList(d[2:], 8)
		}
		return s, false
	case 0xf5:
		return "Channel access read " + hexList(d, 8), false
	case 0x5a:
		if len(d) < 2 {
			return "Channel access write", false
		}
		s := "Channel access write 0x" + hx(int(d[0]), 2)
		bad := d[1] != ^d[0]
		if bad {
			s += " (complement bad)"
		}
		if len(d) >= 3 {
			s += map[bool]string{true: " done", false: " failed"}[d[2] == 0xaa]
		}
		return s, bad
	case 0xc3:
		s := "Reset activity latches"
		if len(d) > 0 {
			s += map[bool]string{true: " done", false: " ?"}[d[len(d)-1] == 0xaa]
		}
		return s, false
	case 0x3c:
		return "Disable test mode", false
	}
	return "? function 0x" + hx(int(fn[0]), 2), true
}

// ds243xFn reads DS2431/DS2432/DS2433 function commands: the scratchpad
// write and read (target address, data and CRC-16), copy, memory read and
// the DS2432's secret commands.
// TRLC-LINKS: REQ-SDS-245
func ds243xFn(fn []byte) (string, bool) {
	d := fn[1:]
	ta := func() string {
		if len(d) < 2 {
			return ""
		}
		return " @0x" + hx(int(d[1])<<8|int(d[0]), 4)
	}
	crc := func(upto int) (string, bool) {
		if len(fn) < upto+2 {
			return "", false
		}
		c := int(fn[upto]) | int(fn[upto+1])<<8
		if crc16Maxim(fn[:upto]) != c {
			return " CRC bad", true
		}
		return " CRC ok", false
	}
	switch fn[0] {
	case 0x0f:
		s := "Write scratchpad" + ta()
		if len(d) >= 10 {
			s += ": " + hexList(d[2:10], 8)
		}
		c, bad := crc(11)
		return s + c, bad
	case 0xaa:
		s := "Read scratchpad" + ta()
		if len(d) >= 3 {
			s += " E/S " + hx(int(d[2]), 2)
		}
		if len(d) >= 11 {
			s += ": " + hexList(d[3:11], 8)
		}
		c, bad := crc(12)
		return s + c, bad
	case 0x55:
		s := "Copy scratchpad" + ta()
		if len(d) >= 3 {
			s += " E/S " + hx(int(d[2]), 2)
		}
		return s, false
	case 0xf0:
		s := "Read memory" + ta()
		if len(d) > 2 {
			s += fmt.Sprintf(", %s: %s", plural(len(d)-2, "byte"), hexList(d[2:], 8))
		}
		return s, false
	case 0x5a:
		return "Load first secret" + ta(), false
	case 0x33:
		return "Compute next secret" + ta(), false
	case 0xa5:
		return "Read authenticated page" + ta(), false
	}
	return "? function 0x" + hx(int(fn[0]), 2), true
}

// ---- CFP module management over MDIO (clause 45) ----

// cfpRegion names the CFP register region an MDIO address falls in.
// TRLC-LINKS: REQ-SDS-246
func cfpRegion(a int) string {
	switch {
	case a >= 0x8000 && a <= 0x807f:
		return "NVR1"
	case a >= 0x8080 && a <= 0x80ff:
		return "NVR2"
	case a >= 0x8100 && a <= 0x817f:
		return "NVR3"
	case a >= 0x8180 && a <= 0x81ff:
		return "NVR4"
	case a >= 0x8400 && a <= 0x847f:
		return "Vendor NVR1"
	case a >= 0x8480 && a <= 0x84ff:
		return "Vendor NVR2"
	case a >= 0x8800 && a <= 0x887f:
		return "User NVR1"
	case a >= 0x8880 && a <= 0x88ff:
		return "User NVR2"
	case a >= 0xa000 && a <= 0xa07f:
		return "Module VR1"
	case a >= 0xa080 && a <= 0xa0ff:
		return "MLG VR1"
	}
	return ""
}

// cfpModules name the module identifiers (SFF-8024).
var cfpModules = map[int]string{0x0e: "CFP", 0x11: "CFP2", 0x12: "CFP4", 0x0c: "QSFP", 0x0d: "QSFP+", 0x06: "XFP",
	0x03: "SFP"}

// mdioFrame reads an MDIO packet: the header word (start, opcode, port and
// device address) and the data word.
// TRLC-LINKS: REQ-SDS-246
func mdioFrame(p Packet) (st, op, port, dev, data int, ok bool) {
	if len(p.Bytes) < 4 {
		return 0, 0, 0, 0, 0, false
	}
	h := be(p.Bytes[:2])
	return h >> 12 & 3, h >> 10 & 3, h >> 5 & 0x1f, h & 0x1f, be(p.Bytes[2:4]), true
}

// cfpNotes reads clause 45 frames as CFP register accesses: the address
// frame sets a port and device's register address, a read (or a read that
// increments it) and a write use it.
// TRLC-LINKS: REQ-SDS-246
func cfpNotes(ps []Packet) {
	addr := map[int]int{}
	for i := range ps {
		p := &ps[i]
		st, op, port, dev, data, ok := mdioFrame(*p)
		if !ok || st != 0 {
			continue
		}
		key := port<<5 | dev
		a, known := addr[key]
		reg := func(a int) string {
			s := "0x" + hx(a, 4)
			if r := cfpRegion(a); r != "" {
				s = r + " " + s
			}
			return s
		}
		switch op {
		case 0:
			addr[key] = data
			p.Note = fmt.Sprintf("CFP %d.%d address %s", port, dev, reg(data))
		case 1:
			if !known {
				p.Note = fmt.Sprintf("CFP %d.%d write (no address) 0x%s", port, dev, hx(data, 4))
				continue
			}
			p.Note = fmt.Sprintf("CFP %d.%d write %s = 0x%s", port, dev, reg(a), hx(data, 4))
		default:
			if !known {
				p.Note = fmt.Sprintf("CFP %d.%d read (no address) 0x%s", port, dev, hx(data, 4))
				continue
			}
			s := fmt.Sprintf("CFP %d.%d read %s = 0x%s", port, dev, reg(a), hx(data, 4))
			if a == 0x8000 {
				if m, ok := cfpModules[data&0xff]; ok {
					s += " (" + m + ")"
				}
			}
			p.Note, p.NoteErr = s, anyBad(*p)
			if op == 2 {
				addr[key] = a + 1
			}
		}
	}
}

// cfpDetect: two or more clause 45 address frames into CFP register regions.
// TRLC-LINKS: REQ-SDS-247
func cfpDetect(ps []Packet) string {
	n := 0
	for _, p := range ps {
		if st, op, _, _, data, ok := mdioFrame(p); ok && st == 0 && op == 0 && cfpRegion(data) != "" {
			n++
		}
	}
	if n < 2 {
		return ""
	}
	return fmt.Sprintf("%d clause 45 addresses in CFP register regions", n)
}

// ---- USB control and data transfers ----

// usbRequests name the standard requests; usbDescs the descriptor types.
var usbRequests = map[int]string{0: "GET_STATUS", 1: "CLEAR_FEATURE", 3: "SET_FEATURE", 5: "SET_ADDRESS",
	6: "GET_DESCRIPTOR", 7: "SET_DESCRIPTOR", 8: "GET_CONFIGURATION", 9: "SET_CONFIGURATION", 10: "GET_INTERFACE",
	11: "SET_INTERFACE", 12: "SYNCH_FRAME"}
var usbDescs = map[int]string{1: "Device", 2: "Configuration", 3: "String", 4: "Interface", 5: "Endpoint",
	6: "Device qualifier", 0x21: "HID", 0x22: "Report"}

// usbSetup reads a SETUP stage's eight bytes.
// TRLC-LINKS: REQ-SDS-246
func usbSetup(d []byte) string {
	rt, req := int(d[0]), int(d[1])
	val, idx, n := le(d[2:4]), le(d[4:6]), le(d[6:8])
	dir := map[bool]string{true: "IN", false: "OUT"}[rt&0x80 != 0]
	if rt&0x60 != 0 {
		kind := map[int]string{1: "class", 2: "vendor", 3: "reserved"}[rt>>5&3]
		return fmt.Sprintf("%s request 0x%s %s value 0x%s index 0x%s len %d", kind, hx(req, 2), dir, hx(val, 4), hx(idx, 4), n)
	}
	name, ok := usbRequests[req]
	if !ok {
		name = "request 0x" + hx(req, 2)
	}
	switch req {
	case 6, 7:
		dn, ok := usbDescs[val>>8]
		if !ok {
			dn = "type 0x" + hx(val>>8, 2)
		}
		s := fmt.Sprintf("%s %s", name, dn)
		if val&0xff != 0 || val>>8 == 3 {
			s += fmt.Sprintf(" %d", val&0xff)
		}
		return s + fmt.Sprintf(" len %d", n)
	case 5:
		return fmt.Sprintf("%s %d", name, val)
	case 9, 11:
		return fmt.Sprintf("%s %d", name, val)
	}
	return fmt.Sprintf("%s value 0x%s index 0x%s len %d", name, hx(val, 4), hx(idx, 4), n)
}

// usbState is the request layer's memory: the transaction's token, and the
// last SETUP request per address and endpoint (its IN data is the answer).
type usbState struct {
	tok   int // the token's PID (0 none)
	addr  int
	ep    int
	setup map[int][]byte
}

// note reads one USB packet in its transaction: a token names the address
// and endpoint, a SETUP's data the standard request, an IN or OUT's data
// its bytes (a device descriptor's IDs), a handshake the outcome.
// TRLC-LINKS: REQ-SDS-246
func (st *usbState) note(p Packet) (string, bool) {
	pid := p.Addr
	b := p.Bytes
	switch pid {
	case 0x1, 0x9, 0xd: // OUT, IN, SETUP
		if len(b) != 2 {
			return "token (cut)", true
		}
		w := le(b)
		st.tok, st.addr, st.ep = pid, w&0x7f, w>>7&0xf
		return fmt.Sprintf("%s %d.%d", map[int]string{1: "OUT", 9: "IN", 0xd: "SETUP"}[pid], st.addr, st.ep), p.Err != ""
	case 0x5:
		if len(b) == 2 {
			return fmt.Sprintf("SOF %d", le(b)&0x7ff), p.Err != ""
		}
		return "SOF", true
	case 0x3, 0xb, 0x7, 0xf: // DATA0 DATA1 DATA2 MDATA
		if len(b) < 2 {
			return "data (cut)", true
		}
		d := b[:len(b)-2]
		key := st.addr<<4 | st.ep
		tok := st.tok
		st.tok = 0
		if p.Err != "" {
			return "data CRC bad", true
		}
		switch {
		case tok == 0xd && len(d) == 8:
			st.setup[key] = append([]byte(nil), d...)
			return usbSetup(d), false
		case tok == 0x9 && len(d) >= 12 && d[1] == 1 && len(st.setup[key]) == 8 && st.setup[key][1] == 6 && st.setup[key][3] == 1:
			return fmt.Sprintf("Device USB %X.%02X class %s VID %s PID %s", d[3], d[2], hx(int(d[4]), 2), hx(le(d[8:10]), 4), hx(le(d[10:12]), 4)), false
		case len(d) == 0:
			return "zero-length data", false
		}
		return fmt.Sprintf("data %s: %s", plural(len(d), "byte"), hexList(d, 8)), false
	case 0x2:
		return "ACK", false
	case 0xa:
		return "NAK", false
	case 0xe:
		return "STALL", false
	case 0x6:
		return "NYET", false
	case 0xc:
		return "PRE", false
	}
	return "PID 0x" + hx(pid, 1), p.Err != ""
}

// usbNotes reads a record's USB packets in order.
// TRLC-LINKS: REQ-SDS-246
func usbNotes(ps []Packet) {
	st := &usbState{setup: map[int][]byte{}}
	for i := range ps {
		if !ps[i].HasAddr {
			continue
		}
		ps[i].Note, ps[i].NoteErr = st.note(ps[i])
	}
}

// usbDetect: a SETUP token followed by a DATA0 packet of eight bytes whose
// CRC checks.
// TRLC-LINKS: REQ-SDS-247
func usbDetect(ps []Packet) string {
	for i := 0; i+1 < len(ps); i++ {
		if ps[i].Addr == 0xd && ps[i].HasAddr && ps[i].Err == "" && ps[i+1].Addr == 0x3 && len(ps[i+1].Bytes) == 10 && ps[i+1].Err == "" {
			return "USB SETUP token and its eight-byte request"
		}
	}
	return ""
}

// usbMatch: a SETUP stage's request bytes (after the DATA0 start), or the
// packet's own first bytes.
// TRLC-LINKS: REQ-SDS-248
func usbMatch(p Packet) ([]string, int) {
	if len(p.Bytes) == 0 {
		return nil, -1
	}
	return sofToks(p.Bytes[:min(2, len(p.Bytes))]), -1
}

// owDevNotes returns a 1-Wire device layer's note writer.
// TRLC-LINKS: REQ-SDS-245
func owDevNotes(dev func(fn []byte) (string, bool)) func(ps []Packet) {
	return func(ps []Packet) { owNotes(ps, dev) }
}
