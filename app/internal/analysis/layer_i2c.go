// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"strconv"
	"strings"
)

// The I2C layers (REQ-SDS-243): a packet is one transfer, START (or a
// repeated START) to STOP, with its address and direction. A register device
// takes a write's first byte as its register pointer and writes the bytes
// after it from there; a read reads from the pointer; both move the pointer
// on. A write of the pointer alone, then a read, is a register read: the
// layers keep the pointer from packet to packet. Each chip's layer reads the
// addresses the chip answers on and leaves the rest of the bus without notes.

// regPtr is a register device's pointer, as the bus last set and moved it.
type regPtr struct {
	ptr   int
	known bool
}

// access is one packet's register access: the first register, its data,
// and whether it writes; ptrOnly is a write of the pointer alone.
type access struct {
	reg     int
	data    []byte
	write   bool
	ptrOnly bool
	known   bool // the register is known (a read with no pointer set before is not)
}

// take reads a packet as a register access with an n-byte pointer (most
// significant byte first) and moves the pointer past its data.
// TRLC-LINKS: REQ-SDS-243
func (r *regPtr) take(p Packet, n int) access {
	if !p.Read {
		if len(p.Bytes) < n {
			return access{write: true, data: p.Bytes}
		}
		a := access{reg: be(p.Bytes[:n]), data: p.Bytes[n:], write: true, known: true}
		a.ptrOnly = len(a.data) == 0
		r.ptr, r.known = a.reg+len(a.data), true
		return a
	}
	a := access{reg: r.ptr, data: p.Bytes, known: r.known}
	if r.known {
		r.ptr += len(p.Bytes)
	}
	return a
}

// bcd reads a BCD byte (masked) as a number; ok false for a nibble over 9.
// TRLC-LINKS: REQ-SDS-243
func bcd(v byte) (int, bool) {
	hi, lo := int(v>>4), int(v&0x0f)
	return hi*10 + lo, hi < 10 && lo < 10
}

// i2cDevs reports whether a packet goes to one of the addresses.
// TRLC-LINKS: REQ-SDS-243
func i2cDevs(p Packet, lo, hi int) bool { return p.HasAddr && p.Addr >= lo && p.Addr <= hi }

// rw names a transfer's direction.
// TRLC-LINKS: REQ-SDS-243
func rw(p Packet) string {
	if p.Read {
		return "read"
	}
	return "write"
}

// ---- 24xx EEPROM ----

// eepromWidths finds each device's word address width: a write of exactly
// one or two bytes followed by a read of the same device is a random read's
// address. One byte so set proves a one-byte part (a 24C32 and up never
// takes one); two prove two only without it (a small part's byte write
// before a current address read looks alike); 1 when none says.
// TRLC-LINKS: REQ-SDS-243
func eepromWidths(ps []Packet) map[int]int {
	w := map[int]int{}
	for i := 0; i+1 < len(ps); i++ {
		p, q := ps[i], ps[i+1]
		if !p.HasAddr || p.Read || !q.Read || q.Addr != p.Addr || p.Err != "" {
			continue
		}
		switch len(p.Bytes) {
		case 1:
			w[p.Addr] = 1
		case 2:
			if w[p.Addr] == 0 {
				w[p.Addr] = 2
			}
		}
	}
	return w
}

// eepromNotes reads 24xx EEPROM transfers at 0x50..0x57: a write of the
// word address and data (a byte or a page write), the word address alone
// (the read that follows reads from it), a read (from the address the device
// counts on to), and a transfer the device does not acknowledge (busy with a
// write cycle: an acknowledge poll).
// TRLC-LINKS: REQ-SDS-243
func eepromNotes(ps []Packet) {
	widths := eepromWidths(ps)
	ptrs := map[int]*regPtr{}
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x50, 0x57) {
			continue
		}
		pre := "EEPROM " + hx(p.Addr, 2)
		if p.Err == FaultNAK {
			p.Note = pre + " busy (no ACK)"
			continue
		}
		n := max(widths[p.Addr], 1)
		r := ptrs[p.Addr]
		if r == nil {
			r = &regPtr{}
			ptrs[p.Addr] = r
		}
		a := r.take(*p, n)
		at := "@0x" + hx(a.reg, 2*n)
		switch {
		case a.write && !a.known:
			p.Note, p.NoteErr = pre+" write (no address)", true
		case a.ptrOnly:
			p.Note = pre + " set address " + at
		case a.write && len(a.data) == 1:
			p.Note = pre + " byte write " + at + ": " + hx(int(a.data[0]), 2)
		case a.write:
			p.Note = pre + " page write " + at + ", " + plural(len(a.data), "byte") + ": " + hexList(a.data, 8)
		case !a.known:
			p.Note = pre + " read (current address), " + plural(len(a.data), "byte") + ": " + hexList(a.data, 8)
		default:
			p.Note = pre + " read " + at + ", " + plural(len(a.data), "byte") + ": " + hexList(a.data, 8)
		}
	}
}

// eepromDetect: every transfer to one device at 0x50..0x57, and one or more
// random reads among them (the word address written, then read from).
// TRLC-LINKS: REQ-SDS-247
func eepromDetect(ps []Packet) string {
	dev, reads := -1, 0
	for i, p := range ps {
		if !p.HasAddr || p.Addr < 0x50 || p.Addr > 0x57 || (dev >= 0 && p.Addr != dev) {
			return ""
		}
		dev = p.Addr
		if i+1 < len(ps) && !p.Read && (len(p.Bytes) == 1 || len(p.Bytes) == 2) && ps[i+1].Read && ps[i+1].Addr == p.Addr {
			reads++
		}
	}
	if reads == 0 {
		return ""
	}
	return fmt.Sprintf("EEPROM at 0x%s: %s from a word address written", hx(dev, 2), plural(reads, "random read"))
}

// eepromMatch: the device and its word address.
// TRLC-LINKS: REQ-SDS-248
func eepromMatch(p Packet) ([]string, int) {
	if p.Read || len(p.Bytes) == 0 {
		return nil, p.Addr
	}
	return hexToks(p.Bytes[:min(2, len(p.Bytes))]), p.Addr
}

// ---- EDID (VESA E-DDC at 0x50) ----

// edidHeader opens an EDID block.
var edidHeader = []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// edidState is the EDID layer's memory: the bytes read so far at their
// offsets (256: a base block and one extension), and which are known.
type edidState struct {
	mem  [256]byte
	have [256]bool
	ptr  regPtr
}

// edidNotes reads EDID reads at 0x50: the offset written, the bytes read
// placed at it; the read that completes the 128-byte base block names the
// display (manufacturer, product, its name, the preferred mode, the version
// and the checksum).
// TRLC-LINKS: REQ-SDS-243
func edidNotes(ps []Packet) {
	st := &edidState{}
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x50, 0x50) {
			continue
		}
		a := st.ptr.take(*p, 1)
		if a.write {
			p.Note = "EDID offset " + fmt.Sprint(a.reg)
			continue
		}
		if !a.known {
			// a read with no offset written starts at the header if it holds one
			if len(a.data) >= 8 && string(a.data[:8]) == string(edidHeader) {
				a.reg, a.known = 0, true
				st.ptr = regPtr{ptr: len(a.data), known: true}
			} else {
				p.Note = "EDID read (no offset), " + plural(len(a.data), "byte")
				continue
			}
		}
		had := st.complete()
		for k, v := range a.data {
			if o := a.reg + k; o < 256 {
				st.mem[o], st.have[o] = v, true
			}
		}
		if !had && st.complete() {
			p.Note, p.NoteErr = st.summary()
			continue
		}
		p.Note = fmt.Sprintf("EDID read @%d, %s", a.reg, plural(len(a.data), "byte"))
	}
}

// complete reports the base block read whole.
// TRLC-LINKS: REQ-SDS-243
func (st *edidState) complete() bool {
	for i := 0; i < 128; i++ {
		if !st.have[i] {
			return false
		}
	}
	return true
}

// summary names the display a base block describes.
// TRLC-LINKS: REQ-SDS-243
func (st *edidState) summary() (string, bool) {
	m := st.mem[:128]
	if string(m[:8]) != string(edidHeader) {
		return "EDID (no header)", true
	}
	id := []byte{64 + m[8]>>2&0x1f, 64 + (m[8]&3)<<3 | m[9]>>5, 64 + m[9]&0x1f}
	s := fmt.Sprintf("EDID %s %s", string(id), hx(int(m[11])<<8|int(m[10]), 4))
	for d := 54; d+18 <= 126; d += 18 {
		if m[d] == 0 && m[d+1] == 0 && m[d+3] == 0xfc {
			s += " '" + printable(strings.TrimRight(strings.SplitN(string(m[d+5:d+18]), "\n", 2)[0], " ")) + "'"
			break
		}
	}
	if pc := int(m[55])<<8 | int(m[54]); pc != 0 {
		h := int(m[58]&0xf0)<<4 | int(m[56])
		v := int(m[61]&0xf0)<<4 | int(m[59])
		hb := int(m[58]&0x0f)<<8 | int(m[57])
		vb := int(m[61]&0x0f)<<8 | int(m[60])
		s += fmt.Sprintf(" %dx%d", h, v)
		if tot := int64((h + hb) * (v + vb)); tot > 0 {
			s += "@" + fixed(int64(pc)*10000, tot, 0) + "Hz"
		}
	}
	s += fmt.Sprintf(" v%d.%d", m[18], m[19])
	sum := 0
	for _, v := range m {
		sum += int(v)
	}
	if sum%256 != 0 {
		return s + " checksum bad", true
	}
	return s + " checksum ok", false
}

// edidDetect: a read at 0x50 that holds the EDID header.
// TRLC-LINKS: REQ-SDS-247
func edidDetect(ps []Packet) string {
	for _, p := range ps {
		if !i2cDevs(p, 0x50, 0x50) || !p.Read {
			continue
		}
		for k := 0; k+8 <= len(p.Bytes); k++ {
			if string(p.Bytes[k:k+8]) == string(edidHeader) {
				return "EDID header 00 FF FF FF FF FF FF 00 read at 0x50"
			}
		}
	}
	return ""
}

// ---- real-time clocks: DS1307 (0x68) and Epson RTC-8564 (0x51) ----

// rtcSpec is where a clock keeps its time: the seconds' register and the
// order of the registers after it.
type rtcSpec struct {
	name                                   string
	sec, min, hour, wday, day, month, year int
	weekdayFrom                            int // the first weekday's number (DS1307 1, RTC-8564 0)
	regs                                   map[int]string
}

var ds1307Spec = rtcSpec{name: "RTC", sec: 0, min: 1, hour: 2, wday: 3, day: 4, month: 5, year: 6, weekdayFrom: 1,
	regs: map[int]string{0: "Seconds", 1: "Minutes", 2: "Hours", 3: "Day", 4: "Date", 5: "Month", 6: "Year", 7: "Control"}}

var rtc8564Spec = rtcSpec{name: "RTC", sec: 2, min: 3, hour: 4, wday: 6, day: 5, month: 7, year: 8, weekdayFrom: 0,
	regs: map[int]string{0: "Control1", 1: "Control2", 2: "Seconds", 3: "Minutes", 4: "Hours", 5: "Days", 6: "Weekdays",
		7: "Months", 8: "Years", 9: "Minute alarm", 10: "Hour alarm", 11: "Day alarm", 12: "Weekday alarm",
		13: "CLKOUT", 14: "Timer control", 15: "Timer"}}

// weekdays name the days of the week from Sunday.
var weekdays = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// clock reads the date and time out of a register image (r[k] the k-th
// register); ok false when a field is not BCD or out of its range.
// TRLC-LINKS: REQ-SDS-243
func (c rtcSpec) clock(r map[int]byte) (string, bool) {
	s, ok1 := bcd(r[c.sec] & 0x7f)
	m, ok2 := bcd(r[c.min] & 0x7f)
	hb := r[c.hour]
	var h int
	ok3 := true
	ampm := ""
	if c.weekdayFrom == 1 && hb&0x40 != 0 { // DS1307 12-hour mode
		h, ok3 = bcd(hb & 0x1f)
		ampm = map[bool]string{true: " PM", false: " AM"}[hb&0x20 != 0]
		ok3 = ok3 && h >= 1 && h <= 12
	} else {
		h, ok3 = bcd(hb & 0x3f)
		ok3 = ok3 && h < 24
	}
	d, ok4 := bcd(r[c.day] & 0x3f)
	mo, ok5 := bcd(r[c.month] & 0x1f)
	y, ok6 := bcd(r[c.year])
	wd := int(r[c.wday]&7) - c.weekdayFrom
	century := 2000
	if c.weekdayFrom == 0 && r[c.month]&0x80 != 0 {
		century = 1900 // RTC-8564's century bit
	}
	ok := ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && s < 60 && m < 60 && d >= 1 && d <= 31 && mo >= 1 && mo <= 12 && wd >= 0 && wd < 7
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d%s %s", century+y, mo, d, h, m, s, ampm, weekdays[wd]), true
}

// rtcNotes reads a clock's register accesses at its address: the date and
// time when an access covers all its registers, else the registers with
// their values.
// TRLC-LINKS: REQ-SDS-243
func rtcNotes(ps []Packet, c rtcSpec, addr int) {
	var r regPtr
	img := map[int]byte{}
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, addr, addr) {
			continue
		}
		a := r.take(*p, 1)
		if !a.known {
			p.Note = c.name + " " + rw(*p) + " (no register), " + plural(len(a.data), "byte")
			continue
		}
		if a.ptrOnly {
			p.Note = c.name + " set register " + regName(c.regs, a.reg)
			continue
		}
		for k, v := range a.data {
			img[a.reg+k] = v
		}
		lo, hi := min(c.sec, c.year), max(c.sec, c.year)
		if a.reg <= lo && a.reg+len(a.data) > hi {
			t, ok := c.clock(img)
			if ok {
				p.Note = c.name + " " + rw(*p) + " " + t
			} else {
				p.Note, p.NoteErr = c.name+" "+rw(*p)+" time not BCD: "+hexList(a.data, 8), true
			}
			continue
		}
		var parts []string
		for k, v := range a.data {
			if len(parts) == 4 {
				parts = append(parts, "..")
				break
			}
			name, ok := c.regs[a.reg+k]
			if !ok {
				name = "0x" + hx(a.reg+k, 2)
			}
			parts = append(parts, name+"="+hx(int(v), 2))
		}
		p.Note = c.name + " " + rw(*p) + " " + strings.Join(parts, " ")
	}
}

// rtcDetect: a read at the clock's address covering its date and time with
// every field BCD and in its range.
// TRLC-LINKS: REQ-SDS-247
func rtcDetect(ps []Packet, c rtcSpec, addr int) string {
	var r regPtr
	for _, p := range ps {
		if !i2cDevs(p, addr, addr) {
			continue
		}
		a := r.take(p, 1)
		lo, hi := min(c.sec, c.year), max(c.sec, c.year)
		if !a.write && a.known && a.reg <= lo && a.reg+len(a.data) > hi {
			img := map[int]byte{}
			for k, v := range a.data {
				img[a.reg+k] = v
			}
			if t, ok := c.clock(img); ok {
				return fmt.Sprintf("0x%s read as a valid date and time %s", hx(addr, 2), t)
			}
		}
	}
	return ""
}

// ---- LM75 temperature sensor (0x48..0x4F) ----

// lm75Regs name the LM75's registers.
var lm75Regs = map[int]string{0: "Temperature", 1: "Config", 2: "T_HYST", 3: "T_OS"}

// lm75Temp reads a temperature register: two's complement, the first byte
// whole degrees, bit 7 of the second a half degree (the LM75's nine bits).
// TRLC-LINKS: REQ-SDS-243
func lm75Temp(b []byte) string {
	v := int(int8(b[0]))*2 + int(b[1]>>7)
	return fixed(int64(v), 2, 1) + " C"
}

// lm75Notes reads LM75 accesses: the pointer (it stays where it is set, at
// the temperature after power-up), the configuration byte, and the
// temperature, hysteresis and overtemperature registers in degrees.
// TRLC-LINKS: REQ-SDS-243
func lm75Notes(ps []Packet) {
	ptrs := map[int]int{}
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x48, 0x4f) {
			continue
		}
		pre := "LM75 " + hx(p.Addr, 2) + " "
		data := p.Bytes
		if !p.Read && len(data) > 0 {
			ptrs[p.Addr] = int(data[0]) & 3
			data = data[1:]
			if len(data) == 0 {
				p.Note = pre + "pointer " + regName(lm75Regs, ptrs[p.Addr])
				continue
			}
		}
		reg := ptrs[p.Addr]
		switch {
		case reg == 1 && len(data) >= 1:
			c := data[0]
			p.Note = fmt.Sprintf("%s%s Config %s: %s %s", pre, rw(*p), hx(int(c), 2),
				map[bool]string{true: "shutdown", false: "normal"}[c&1 != 0],
				map[bool]string{true: "interrupt", false: "comparator"}[c&2 != 0])
		case reg != 1 && len(data) >= 2:
			p.Note = fmt.Sprintf("%s%s %s %s", pre, rw(*p), regName(lm75Regs, reg), lm75Temp(data))
		default:
			p.Note = fmt.Sprintf("%s%s %s, %s", pre, rw(*p), regName(lm75Regs, reg), plural(len(data), "byte"))
		}
	}
}

// ---- MLX90614 IR thermometer (SMBus, 0x5A) ----

// smbusPEC is SMBus's packet error code: CRC-8, polynomial x^8+x^2+x+1.
// TRLC-LINKS: REQ-SDS-243
func smbusPEC(b []byte) int {
	crc := 0
	for _, v := range b {
		crc ^= int(v)
		for k := 0; k < 8; k++ {
			if crc&0x80 != 0 {
				crc = (crc<<1 ^ 0x07) & 0xff
			} else {
				crc = crc << 1 & 0xff
			}
		}
	}
	return crc
}

// mlxRegs name the MLX90614's RAM and EEPROM words (EEPROM at 0x20+).
var mlxRegs = map[int]string{0x04: "Raw IR1", 0x05: "Raw IR2", 0x06: "Ta", 0x07: "Tobj1", 0x08: "Tobj2",
	0x20: "To max", 0x21: "To min", 0x22: "PWM ctrl", 0x23: "Ta range", 0x24: "Emissivity", 0x25: "Config1",
	0x2E: "SMBus address", 0x3C: "ID0", 0x3D: "ID1", 0x3E: "ID2", 0x3F: "ID3"}

// mlxNotes reads MLX90614 word reads (the command, then a read of the low
// byte, the high byte and the PEC): a temperature in kelvin times 50, shown
// in degrees Celsius; the PEC is checked.
// TRLC-LINKS: REQ-SDS-243
func mlxNotes(ps []Packet) {
	cmd := -1
	for i := range ps {
		p := &ps[i]
		if !p.HasAddr || (p.Addr != 0x5a && p.Addr != 0x00) {
			continue
		}
		if !p.Read {
			if len(p.Bytes) == 1 {
				cmd = int(p.Bytes[0])
				p.Note = "MLX90614 command " + regName(mlxRegs, cmd&0x3f)
			} else if len(p.Bytes) >= 3 {
				w := int(p.Bytes[2])<<8 | int(p.Bytes[1])
				p.Note = fmt.Sprintf("MLX90614 write %s = 0x%s", regName(mlxRegs, int(p.Bytes[0])&0x3f), hx(w, 4))
				cmd = -1
			}
			continue
		}
		if cmd < 0 || len(p.Bytes) < 3 {
			p.Note = "MLX90614 read, " + plural(len(p.Bytes), "byte")
			continue
		}
		w := int(p.Bytes[1])<<8 | int(p.Bytes[0])
		pec := smbusPEC([]byte{byte(p.Addr << 1), byte(cmd), byte(p.Addr<<1 | 1), p.Bytes[0], p.Bytes[1]})
		name := regName(mlxRegs, cmd&0x3f)
		var v string
		if c := cmd & 0x3f; cmd < 0x20 && c >= 6 && c <= 8 {
			v = fixed(int64(w*2-27315), 100, 2) + " C"
			if w&0x8000 != 0 {
				v = "error flag"
			}
		} else {
			v = "0x" + hx(w, 4)
		}
		p.Note = fmt.Sprintf("MLX90614 %s %s", name, v)
		if pec != int(p.Bytes[2]) {
			p.Note += " PEC bad"
			p.NoteErr = true
		}
		cmd = -1
	}
}

// mlxDetect: a word read at 0x5A whose PEC checks.
// TRLC-LINKS: REQ-SDS-247
func mlxDetect(ps []Packet) string {
	for i := 0; i+1 < len(ps); i++ {
		p, q := ps[i], ps[i+1]
		if !i2cDevs(p, 0x5a, 0x5a) || p.Read || len(p.Bytes) != 1 || !q.Read || q.Addr != 0x5a || len(q.Bytes) != 3 {
			continue
		}
		if smbusPEC([]byte{0xb4, p.Bytes[0], 0xb5, q.Bytes[0], q.Bytes[1]}) == int(q.Bytes[2]) {
			return "SMBus word read at 0x5A with a good PEC"
		}
	}
	return ""
}

// ---- ATSHA204A crypto authentication (0x64) ----

// atshaCRC is the ATSHA204A's CRC-16: polynomial 0x8005, each byte's bits
// least significant first, the result's low byte first on the bus.
// TRLC-LINKS: REQ-SDS-243
func atshaCRC(b []byte) int {
	crc := 0
	for _, v := range b {
		for k := 0; k < 8; k++ {
			bit := int(v>>k) & 1
			top := crc >> 15 & 1
			crc = crc << 1 & 0xffff
			if bit != top {
				crc ^= 0x8005
			}
		}
	}
	return crc
}

// atshaOps name the ATSHA204A's command opcodes.
var atshaOps = map[int]string{0x01: "Pause", 0x02: "Read", 0x08: "MAC", 0x11: "HMAC", 0x12: "Write", 0x15: "GenDig",
	0x16: "Nonce", 0x17: "Lock", 0x1b: "Random", 0x1c: "DeriveKey", 0x20: "UpdateExtra", 0x24: "Counter",
	0x28: "CheckMac", 0x30: "DevRev", 0x40: "GenKey", 0x41: "Sign", 0x43: "ECDH", 0x45: "Verify", 0x46: "PrivWrite",
	0x47: "SHA"}

// atshaStatus names a response's status byte.
var atshaStatus = map[int]string{0x00: "success", 0x01: "checkmac failure", 0x03: "parse error",
	0x0f: "execution error", 0x11: "ready", 0xff: "CRC error"}

// atshaCRCok checks a block's count, data and CRC (the count covers itself
// and the CRC).
// TRLC-LINKS: REQ-SDS-243
func atshaCRCok(b []byte) bool {
	if len(b) < 3 || int(b[0]) != len(b) {
		return false
	}
	c := atshaCRC(b[:len(b)-2])
	return int(b[len(b)-2]) == c&0xff && int(b[len(b)-1]) == c>>8
}

// atshaNotes reads ATSHA204A transfers: a write's word address (reset,
// sleep, idle, command) and a command block (count, opcode, param1, param2,
// data, CRC); a read's response block (count, status or data, CRC).
// TRLC-LINKS: REQ-SDS-243
func atshaNotes(ps []Packet) {
	for i := range ps {
		p := &ps[i]
		if !p.HasAddr || p.Addr != 0x64 {
			continue
		}
		b := p.Bytes
		if p.Read {
			switch {
			case len(b) == 0:
				p.Note = "ATSHA204A read"
			case len(b) == 4:
				st, ok := atshaStatus[int(b[1])]
				if !ok {
					st = "0x" + hx(int(b[1]), 2)
				}
				p.Note = "ATSHA204A status " + st
			default:
				p.Note = fmt.Sprintf("ATSHA204A response, %s", plural(max(len(b)-3, 0), "byte"))
			}
			if len(b) > 0 && !atshaCRCok(b) {
				p.Note += " CRC bad"
				p.NoteErr = true
			}
			continue
		}
		if len(b) == 0 {
			p.Note = "ATSHA204A wake"
			continue
		}
		switch b[0] {
		case 0:
			p.Note = "ATSHA204A reset"
		case 1:
			p.Note = "ATSHA204A sleep"
		case 2:
			p.Note = "ATSHA204A idle"
		case 3:
			c := b[1:]
			if len(c) < 7 {
				p.Note, p.NoteErr = "ATSHA204A command (cut)", true
				continue
			}
			op, ok := atshaOps[int(c[1])]
			if !ok {
				op = "0x" + hx(int(c[1]), 2)
			}
			p.Note = fmt.Sprintf("ATSHA204A %s param1 %s param2 %s", op, hx(int(c[2]), 2), hx(int(c[4])<<8|int(c[3]), 4))
			if n := len(c) - 7; n > 0 {
				p.Note += ", " + plural(n, "data byte")
			}
			if !atshaCRCok(c) {
				p.Note += " CRC bad"
				p.NoteErr = true
			}
		default:
			p.Note, p.NoteErr = "ATSHA204A word address 0x"+hx(int(b[0]), 2), true
		}
	}
}

// atshaDetect: a command block at 0x64 whose CRC checks.
// TRLC-LINKS: REQ-SDS-247
func atshaDetect(ps []Packet) string {
	for _, p := range ps {
		if i2cDevs(p, 0x64, 0x64) && !p.Read && len(p.Bytes) >= 8 && p.Bytes[0] == 3 && atshaCRCok(p.Bytes[1:]) {
			return "ATSHA204A command at 0x64 with a good CRC"
		}
	}
	return ""
}

// ---- HDCP (DDC at 0x3A) ----

// hdcpRegs name the HDCP receiver's register offsets.
var hdcpRegs = map[int]string{0x00: "Bksv", 0x08: "Ri'", 0x0a: "Pj'", 0x10: "Aksv", 0x15: "Ainfo", 0x18: "An",
	0x20: "V'H0", 0x24: "V'H1", 0x28: "V'H2", 0x2c: "V'H3", 0x30: "V'H4", 0x40: "Bcaps", 0x41: "Bstatus",
	0x43: "KSV FIFO", 0x50: "HDCP2Version", 0x60: "Write_Message", 0x70: "RxStatus", 0x80: "Read_Message"}

// hdcpMsgs name the HDCP 2.2 messages.
var hdcpMsgs = map[int]string{2: "AKE_Init", 3: "AKE_Send_Cert", 4: "AKE_No_stored_km", 5: "AKE_Stored_km",
	7: "AKE_Send_H_prime", 8: "AKE_Send_Pairing_Info", 9: "LC_Init", 10: "LC_Send_L_prime", 11: "SKE_Send_Eks",
	12: "RepeaterAuth_Send_ReceiverID_List", 15: "RepeaterAuth_Send_Ack", 16: "RepeaterAuth_Stream_Manage",
	17: "RepeaterAuth_Stream_Ready"}

// hdcpNotes reads HDCP transfers at 0x3A: the register written and read,
// Bstatus's device count, RxStatus's message length, and the HDCP 2
// messages written and read.
// TRLC-LINKS: REQ-SDS-243
func hdcpNotes(ps []Packet) {
	var r regPtr
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x3a, 0x3a) {
			continue
		}
		a := r.take(*p, 1)
		if !a.known {
			p.Note = "HDCP " + rw(*p) + ", " + plural(len(a.data), "byte")
			continue
		}
		name := regName(hdcpRegs, a.reg)
		if a.ptrOnly {
			p.Note = "HDCP offset " + name
			continue
		}
		d := a.data
		s := fmt.Sprintf("HDCP %s %s", rw(*p), name)
		switch {
		case (a.reg == 0x60 || a.reg == 0x80) && len(d) > 0:
			m, ok := hdcpMsgs[int(d[0])]
			if !ok {
				m = "message 0x" + hx(int(d[0]), 2)
			}
			s += " " + m
		case a.reg == 0x41 && len(d) >= 2:
			v := int(d[1])<<8 | int(d[0])
			s += fmt.Sprintf(" %d devices depth %d", v&0x7f, v>>8&7)
			if v&0x1000 != 0 {
				s += " HDMI"
			}
		case a.reg == 0x70 && len(d) >= 2:
			v := int(d[1])<<8 | int(d[0])
			s += fmt.Sprintf(" length %d", v&0x3ff)
			if v&0x400 != 0 {
				s += " ready"
			}
			if v&0x800 != 0 {
				s += " reauth"
			}
		case a.reg == 0x50 && len(d) >= 1:
			s += map[bool]string{true: " HDCP2", false: " not HDCP2"}[d[0]&4 != 0]
		default:
			s += ": " + hexList(d, 8)
		}
		p.Note = s
	}
}

// hdcpReads are the HDCP registers a transmitter reads and their lengths:
// Bksv, Bcaps, Bstatus, HDCP2Version.
var hdcpReads = map[int]int{0x00: 5, 0x40: 1, 0x41: 2, 0x50: 1}

// hdcpDetect: at 0x3A, one of the receiver's identity or capability
// registers set as the offset and read at its length, and no write to an
// offset HDCP does not have.
// TRLC-LINKS: REQ-SDS-247
func hdcpDetect(ps []Packet) string {
	found := ""
	for i, p := range ps {
		if !i2cDevs(p, 0x3a, 0x3a) || p.Read {
			continue
		}
		if len(p.Bytes) == 0 {
			return ""
		}
		off := int(p.Bytes[0])
		if _, ok := hdcpRegs[off]; !ok {
			return ""
		}
		if n, ok := hdcpReads[off]; ok && len(p.Bytes) == 1 && i+1 < len(ps) && i2cDevs(ps[i+1], 0x3a, 0x3a) &&
			ps[i+1].Read && len(ps[i+1].Bytes) == n {
			found = hdcpRegs[off]
		}
	}
	if found == "" {
		return ""
	}
	return "HDCP " + found + " read at 0x3A"
}

// ---- LTC2607/2617/2627 dual DAC ----

// ltcCmds and ltcDACs name the DAC's command and address nibbles.
var ltcCmds = map[int]string{0: "Write", 1: "Update", 3: "Write+Update", 4: "Power down", 15: "No op"}
var ltcDACs = map[int]string{0: "DAC A", 1: "DAC B", 15: "all DACs"}

// ltcNotes reads LTC26x7 writes: a command and address byte, then the
// 16-bit code (the LTC2607's; the 2617 and 2627 left-justify 14 and 12).
// TRLC-LINKS: REQ-SDS-243
func ltcNotes(ps []Packet) {
	for i := range ps {
		p := &ps[i]
		if !p.HasAddr || p.Read || len(p.Bytes) == 0 {
			continue
		}
		c := int(p.Bytes[0])
		cmd, ok1 := ltcCmds[c>>4]
		dac, ok2 := ltcDACs[c&0xf]
		if !ok1 || !ok2 {
			p.Note, p.NoteErr = "LTC26x7 ? 0x"+hx(c, 2), true
			continue
		}
		s := "LTC26x7 " + cmd + " " + dac
		if len(p.Bytes) >= 3 {
			v := be(p.Bytes[1:3])
			s += fmt.Sprintf(" 0x%s (%s Vref)", hx(v, 4), fixed(int64(v), 0xffff, 4))
		}
		p.Note = s
	}
}

// ---- port expanders: TCA6408A, PCA9571, MCP23008/17 ----

// tcaRegs name the TCA6408A's registers.
var tcaRegs = map[int]string{0: "Input", 1: "Output", 2: "Polarity", 3: "Config"}

// tcaNotes reads TCA6408A accesses at 0x20/0x21.
// TRLC-LINKS: REQ-SDS-243
func tcaNotes(ps []Packet) {
	regNotes(ps, 0x20, 0x21, "TCA6408A", func(int) map[int]string { return tcaRegs })
}

// pcaNotes reads PCA9571 transfers at 0x25: the output byte written, the
// port read back.
// TRLC-LINKS: REQ-SDS-243
func pcaNotes(ps []Packet) {
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x25, 0x25) || len(p.Bytes) == 0 {
			continue
		}
		if p.Read {
			p.Note = "PCA9571 read " + hexList(p.Bytes, 8)
		} else {
			p.Note = "PCA9571 outputs " + hexList(p.Bytes, 8)
		}
	}
}

// mcpNames name an MCP230xx's registers: the MCP23008's eleven, or the
// MCP23017's A and B pairs in bank 0 (IOCON.BANK 0, the reset state).
// TRLC-LINKS: REQ-SDS-243
func mcpNames(wide bool) map[int]string {
	base := []string{"IODIR", "IPOL", "GPINTEN", "DEFVAL", "INTCON", "IOCON", "GPPU", "INTF", "INTCAP", "GPIO", "OLAT"}
	m := map[int]string{}
	for i, n := range base {
		if !wide {
			m[i] = n
		} else if n == "IOCON" {
			m[2*i], m[2*i+1] = n, n
		} else {
			m[2*i], m[2*i+1] = n+"A", n+"B"
		}
	}
	return m
}

var mcp23008Regs = mcpNames(false)
var mcp23017Regs = mcpNames(true)

// regNotes reads a simple register device's accesses at lo..hi: the pointer
// written, then registers written or read from it, named by names(address).
// TRLC-LINKS: REQ-SDS-243
func regNotes(ps []Packet, lo, hi int, chip string, names func(int) map[int]string) {
	ptrs := map[int]*regPtr{}
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, lo, hi) {
			continue
		}
		r := ptrs[p.Addr]
		if r == nil {
			r = &regPtr{}
			ptrs[p.Addr] = r
		}
		a := r.take(*p, 1)
		t := names(p.Addr)
		pre := chip + " " + hx(p.Addr, 2) + " "
		switch {
		case !a.known:
			p.Note = pre + rw(*p) + " (no register), " + plural(len(a.data), "byte")
		case a.ptrOnly:
			p.Note = pre + "register " + regName(t, a.reg)
		default:
			var parts []string
			for k, v := range a.data {
				if len(parts) == 4 {
					parts = append(parts, "..")
					break
				}
				parts = append(parts, regName(t, a.reg+k)+"="+hx(int(v), 2))
			}
			p.Note = pre + rw(*p) + " " + strings.Join(parts, " ")
		}
	}
}

// ---- MXC6225XU accelerometer (0x15) ----

// mxcRegs name the MXC6225XU's registers.
var mxcRegs = map[int]string{0: "XOUT", 1: "YOUT", 2: "STATUS", 3: "DETECTION"}

// mxcNotes reads MXC6225XU accesses: the axes in counts (two's complement),
// the status and detection registers in hex.
// TRLC-LINKS: REQ-SDS-243
func mxcNotes(ps []Packet) {
	var r regPtr
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x15, 0x15) {
			continue
		}
		a := r.take(*p, 1)
		switch {
		case !a.known:
			p.Note = "MXC6225XU " + rw(*p) + ", " + plural(len(a.data), "byte")
		case a.ptrOnly:
			p.Note = "MXC6225XU register " + regName(mxcRegs, a.reg)
		default:
			var parts []string
			for k, v := range a.data {
				reg := a.reg + k
				if len(parts) == 4 {
					parts = append(parts, "..")
					break
				}
				if reg <= 1 {
					parts = append(parts, fmt.Sprintf("%s=%d", regName(mxcRegs, reg), int8(v)))
				} else {
					parts = append(parts, regName(mxcRegs, reg)+"="+hx(int(v), 2))
				}
			}
			p.Note = "MXC6225XU " + rw(*p) + " " + strings.Join(parts, " ")
		}
	}
}

// ---- Wii Nunchuk (0x52) ----

// nunchukNotes reads Nunchuk transfers: the init writes (40 00 on the
// original, F0 55 and FB 00 on the unencrypted protocol), the register
// pointer, and six-byte reports (stick X/Y, 10-bit accelerometer X/Y/Z, the
// Z and C buttons, pressed low).
// TRLC-LINKS: REQ-SDS-243
func nunchukNotes(ps []Packet) {
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x52, 0x52) {
			continue
		}
		b := p.Bytes
		if !p.Read {
			switch {
			case len(b) == 2 && b[0] == 0x40 && b[1] == 0x00:
				p.Note = "Nunchuk init"
			case len(b) == 2 && (b[0] == 0xf0 || b[0] == 0xfb):
				p.Note = "Nunchuk init (unencrypted) " + hexList(b, 2)
			case len(b) == 1 && b[0] == 0:
				p.Note = "Nunchuk request report"
			default:
				p.Note = "Nunchuk write " + hexList(b, 8)
			}
			continue
		}
		if len(b) < 6 {
			p.Note = "Nunchuk read, " + plural(len(b), "byte")
			continue
		}
		ax := int(b[2])<<2 | int(b[5]>>2&3)
		ay := int(b[3])<<2 | int(b[5]>>4&3)
		az := int(b[4])<<2 | int(b[5]>>6&3)
		s := fmt.Sprintf("Nunchuk stick %d/%d accel %d/%d/%d", b[0], b[1], ax, ay, az)
		if b[5]&1 == 0 {
			s += " Z"
		}
		if b[5]&2 == 0 {
			s += " C"
		}
		p.Note = s
	}
}

// nunchukDetect: an init write at 0x52 and a six-byte report.
// TRLC-LINKS: REQ-SDS-247
func nunchukDetect(ps []Packet) string {
	init, report := false, false
	for _, p := range ps {
		if !i2cDevs(p, 0x52, 0x52) {
			continue
		}
		b := p.Bytes
		if !p.Read && len(b) == 2 && ((b[0] == 0x40 && b[1] == 0) || (b[0] == 0xf0 && b[1] == 0x55) || (b[0] == 0xfb && b[1] == 0)) {
			init = true
		}
		if p.Read && len(b) == 6 {
			report = true
		}
	}
	if init && report {
		return "Nunchuk init and a six-byte report at 0x52"
	}
	return ""
}

// ---- XFP module (INF-8077 at 0x50) ----

// xfpState is the XFP layer's memory: the bytes read at their offsets
// (lower memory 0..127, table 1 at 128..255) and the table selected.
type xfpState struct {
	mem  [256]byte
	have [256]bool
	ptr  regPtr
}

// xfpText reads a space-padded text field.
// TRLC-LINKS: REQ-SDS-243
func xfpText(b []byte) string {
	return printable(strings.TrimRight(strings.TrimRight(string(b), "\x00"), " "))
}

// xfpNotes reads XFP memory reads at 0x50: the A/D readouts (temperature,
// TX bias, TX and RX power) when a read covers them, the identifier, vendor,
// part and serial numbers and the wavelength from table 1.
// TRLC-LINKS: REQ-SDS-243
func xfpNotes(ps []Packet) {
	st := &xfpState{}
	for i := range ps {
		p := &ps[i]
		if !i2cDevs(*p, 0x50, 0x50) {
			continue
		}
		a := st.ptr.take(*p, 1)
		if a.write {
			if a.ptrOnly {
				p.Note = fmt.Sprintf("XFP offset %d", a.reg)
			} else {
				p.Note = fmt.Sprintf("XFP write @%d: %s", a.reg, hexList(a.data, 8))
			}
			continue
		}
		if !a.known {
			p.Note = "XFP read (no offset), " + plural(len(a.data), "byte")
			continue
		}
		for k, v := range a.data {
			if o := a.reg + k; o < 256 {
				st.mem[o], st.have[o] = v, true
			}
		}
		covers := func(lo, hi int) bool { return a.reg <= lo && a.reg+len(a.data) > hi }
		var parts []string
		if covers(0, 0) {
			parts = append(parts, xfpModule(int(st.mem[0])))
		}
		if covers(96, 105) {
			m := st.mem[:]
			t := int(int16(uint16(m[96])<<8 | uint16(m[97])))
			bias := int(m[100])<<8 | int(m[101])
			tx := int(m[102])<<8 | int(m[103])
			rx := int(m[104])<<8 | int(m[105])
			parts = append(parts, fmt.Sprintf("%s C bias %s mA TX %s mW RX %s mW", fixed(int64(t), 256, 1),
				fixed(int64(bias)*2, 10000, 2), fixed(int64(tx), 10000, 3), fixed(int64(rx), 10000, 3)))
		}
		if covers(128, 128) {
			parts = append(parts, xfpModule(int(st.mem[128])))
		}
		if covers(148, 163) {
			parts = append(parts, "'"+xfpText(st.mem[148:164])+"'")
		}
		if covers(168, 183) {
			parts = append(parts, "PN '"+xfpText(st.mem[168:184])+"'")
		}
		if covers(186, 187) {
			parts = append(parts, fmt.Sprintf("%s nm", fixed(int64(int(st.mem[186])<<8|int(st.mem[187])), 20, 2)))
		}
		if covers(196, 211) {
			parts = append(parts, "SN '"+xfpText(st.mem[196:212])+"'")
		}
		if len(parts) == 0 {
			p.Note = fmt.Sprintf("XFP read @%d, %s", a.reg, plural(len(a.data), "byte"))
			continue
		}
		p.Note = "XFP " + strings.Join(parts, " ")
	}
}

// xfpModule names a module identifier (SFF-8024).
// TRLC-LINKS: REQ-SDS-243
func xfpModule(id int) string {
	names := map[int]string{1: "GBIC", 3: "SFP", 5: "XENPAK", 6: "XFP", 7: "XFF", 8: "XFP-E", 9: "XPAK", 10: "X2"}
	if n, ok := names[id]; ok {
		return n
	}
	return "module 0x" + hx(id, 2)
}

// xfpDetect: a read at 0x50 from offset 0 or 128 whose identifier byte is
// XFP's (06).
// TRLC-LINKS: REQ-SDS-247
func xfpDetect(ps []Packet) string {
	var r regPtr
	for _, p := range ps {
		if !i2cDevs(p, 0x50, 0x50) {
			continue
		}
		a := r.take(p, 1)
		if !a.write && a.known && len(a.data) > 0 && (a.reg == 0 || a.reg == 128) && a.data[0] == 0x06 {
			return fmt.Sprintf("XFP identifier 06 read at offset %d of 0x50", a.reg)
		}
	}
	return ""
}

// ---- address split and filter ----

// demuxNotes numbers the devices of a bus in the order they first appear
// and each device's transfers: "dev 2 (0x50) #3".
// TRLC-LINKS: REQ-SDS-243
func demuxNotes(ps []Packet) {
	devs := map[int]int{}
	count := map[int]int{}
	for i := range ps {
		p := &ps[i]
		if !p.HasAddr {
			continue
		}
		if _, ok := devs[p.Addr]; !ok {
			devs[p.Addr] = len(devs) + 1
		}
		count[p.Addr]++
		p.Note = "dev " + strconv.Itoa(devs[p.Addr]) + " (0x" + hx(p.Addr, 2) + ") #" + strconv.Itoa(count[p.Addr]) + " " + rw(*p) + ", " + plural(len(p.Bytes), "byte")
	}
}

// FilterI2C keeps the transfers to one address (-1 any) in one direction
// (0 both, 1 read, 2 write): the I2C filter layer's packet list.
// TRLC-LINKS: REQ-SDS-243
func FilterI2C(ps []Packet, addr, dir int) []Packet {
	var out []Packet
	for _, p := range ps {
		if !p.HasAddr || (addr >= 0 && p.Addr != addr) || (dir == 1 && !p.Read) || (dir == 2 && p.Read) {
			continue
		}
		p.Note = "0x" + hx(p.Addr, 2) + " " + rw(p) + ", " + plural(len(p.Bytes), "byte")
		out = append(out, p)
	}
	return out
}
