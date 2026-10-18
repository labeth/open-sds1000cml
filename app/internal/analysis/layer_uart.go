// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"strings"
)

// The UART layers (REQ-SDS-244): a packet is a burst of characters the
// line's idle of 1.5 character times ends. S.BUS sends a 25-byte frame per
// burst; the trace streams (ITM, TPIU, ETM) and the text protocols read the
// bytes of a burst in order, a packet's note naming what it holds.

// ---- Futaba S.BUS (100 kbaud, 8E2, inverted) ----

// sbusFrame reads one S.BUS frame: 0x0F, 16 channels of 11 bits least
// significant bit first, a flag byte (channels 17 and 18, frame lost,
// failsafe) and the end byte (0x00; S.BUS2 sends 04, 14, 24 or 34).
// TRLC-LINKS: REQ-SDS-244
func sbusFrame(b []byte) (string, bool) {
	var ch [16]int
	bit := 0
	for k := range ch {
		v := 0
		for j := 0; j < 11; j++ {
			byteI, bitI := 1+(bit>>3), bit&7
			v |= int(b[byteI]>>bitI&1) << j
			bit++
		}
		ch[k] = v
	}
	var sb strings.Builder
	sb.WriteString("S.BUS")
	for _, v := range ch {
		sb.WriteString(fmt.Sprintf(" %d", v))
	}
	f := b[23]
	if f&1 != 0 {
		sb.WriteString(" ch17")
	}
	if f&2 != 0 {
		sb.WriteString(" ch18")
	}
	if f&4 != 0 {
		sb.WriteString(" lost")
	}
	if f&8 != 0 {
		sb.WriteString(" failsafe")
	}
	end := b[24]
	ok := end == 0x00 || end&0x0f == 0x04
	if !ok {
		sb.WriteString(" end " + hx(int(end), 2))
	}
	return sb.String(), !ok
}

// sbusNote reads the S.BUS frames of a burst.
// TRLC-LINKS: REQ-SDS-244
func sbusNote(p Packet) (string, bool) {
	b := p.Bytes
	if len(b) != 25 || b[0] != 0x0f {
		return fmt.Sprintf("S.BUS? %s (25 from 0x0F)", plural(len(b), "byte")), true
	}
	s, bad := sbusFrame(b)
	if anyBad(p) {
		return s + " (parity)", true
	}
	return s, bad
}

// sbusNotes writes each burst's S.BUS frame.
// TRLC-LINKS: REQ-SDS-244
func sbusNotes(ps []Packet) {
	for i := range ps {
		ps[i].Note, ps[i].NoteErr = sbusNote(ps[i])
	}
}

// sbusDetect: two or more bursts of 25 bytes opening with 0x0F and ending
// with an end byte, no character with a parity or framing error.
// TRLC-LINKS: REQ-SDS-247
func sbusDetect(ps []Packet) string {
	n := 0
	for _, p := range ps {
		if !p.Complete {
			continue
		}
		if _, bad := sbusNote(p); bad {
			return ""
		}
		n++
	}
	if n < 2 {
		return ""
	}
	return fmt.Sprintf("%d S.BUS frames (25 bytes from 0x0F)", n)
}

// sbusMatch: the frame's header.
// TRLC-LINKS: REQ-SDS-248
func sbusMatch(p Packet) ([]string, int) { return []string{"0F"}, -1 }

// ---- ARM ITM (instrumentation trace over SWO) ----

// itmExceptions name the Cortex-M exception numbers.
var itmExceptions = map[int]string{0: "Thread", 1: "Reset", 2: "NMI", 3: "HardFault", 4: "MemManage", 5: "BusFault",
	6: "UsageFault", 11: "SVCall", 12: "DebugMon", 14: "PendSV", 15: "SysTick"}

// itmText reports a character an ITM stimulus write joins to a string.
// TRLC-LINKS: REQ-SDS-244
func itmText(v byte) bool { return (v >= 0x20 && v < 0x7f) || (v >= 9 && v <= 13) }

// itmParse reads ITM packets: synchronization, overflow, local timestamps,
// extension packets, software stimulus writes (channel and 1, 2 or 4
// bytes; single printable characters on a channel join into a string) and
// hardware (DWT) packets: event counters, exception trace, PC samples, data
// trace. The items are what the bytes hold, in order.
// TRLC-LINKS: REQ-SDS-244
func itmParse(b []byte) (items []string, bad bool) {
	textCh, text := -1, []byte{}
	flush := func() {
		if textCh >= 0 {
			items = append(items, fmt.Sprintf("ch%d %s", textCh, quoted(text, 24)))
			textCh, text = -1, text[:0]
		}
	}
	for i := 0; i < len(b); {
		h := b[i]
		switch {
		case h == 0x00: // synchronization: zeros, then 0x80
			j := i
			for j < len(b) && b[j] == 0 {
				j++
			}
			flush()
			if j < len(b) && b[j] == 0x80 && j-i >= 5 {
				items = append(items, "SYNC")
				i = j + 1
				continue
			}
			items = append(items, "(zeros)")
			i = j
			continue
		case h == 0x70:
			flush()
			items = append(items, "OVERFLOW")
			i++
			continue
		case h&0x0f == 0x00: // local timestamp
			flush()
			if h&0x80 == 0 {
				items = append(items, fmt.Sprintf("TS +%d", h>>4))
				i++
				continue
			}
			ts, j := 0, i+1
			for k := 0; j < len(b) && k < 4; k++ {
				ts |= int(b[j]&0x7f) << (7 * k)
				j++
				if b[j-1]&0x80 == 0 {
					break
				}
			}
			if b[j-1]&0x80 != 0 {
				items = append(items, "TS (cut)")
				return items, true
			}
			items = append(items, fmt.Sprintf("TS +%d", ts))
			i = j
			continue
		case h&0x0b == 0x08: // extension: continuation bytes
			flush()
			j := i + 1
			for h&0x80 != 0 && j < len(b) && b[j-1]&0x80 != 0 {
				j++
			}
			items = append(items, "EXT")
			i = j
			continue
		case h&0x0f == 0x04:
			flush()
			items = append(items, "? 0x"+hx(int(h), 2))
			bad = true
			i++
			continue
		}
		n := []int{0, 1, 2, 4}[h&3]
		if i+1+n > len(b) {
			flush()
			items = append(items, "(cut)")
			return items, true
		}
		d := b[i+1 : i+1+n]
		id := int(h >> 3)
		i += 1 + n
		if h&0x04 == 0 { // software stimulus
			if n == 1 && itmText(d[0]) {
				if textCh != id {
					flush()
					textCh = id
				}
				text = append(text, d[0])
				continue
			}
			flush()
			items = append(items, fmt.Sprintf("ch%d 0x%s", id, hx(le64(d), 2*n)))
			continue
		}
		flush()
		switch {
		case id == 0 && n == 1:
			items = append(items, "DWT events 0x"+hx(int(d[0]), 2))
		case id == 1 && n == 2:
			exc := int(d[1]&1)<<8 | int(d[0])
			name, ok := itmExceptions[exc]
			if !ok {
				name = fmt.Sprintf("IRQ %d", exc-16)
			}
			ev := map[int]string{1: "enter", 2: "exit", 3: "resume"}[int(d[1]>>4)]
			items = append(items, strings.TrimSpace(ev+" "+name))
		case id == 2:
			if n == 4 {
				items = append(items, "PC 0x"+hx(le64(d), 8))
			} else {
				items = append(items, "PC sleep")
			}
		case h&0xc4 == 0x84:
			wr := map[bool]string{true: "write", false: "read"}[h&0x08 != 0]
			items = append(items, fmt.Sprintf("WP%d %s 0x%s", h>>4&3, wr, hx(le64(d), 2*n)))
		case h&0xcf == 0x47:
			items = append(items, fmt.Sprintf("WP%d PC 0x%s", h>>4&3, hx(le64(d), 8)))
		case h&0xcf == 0x4e:
			items = append(items, fmt.Sprintf("WP%d addr 0x%s", h>>4&3, hx(le64(d), 4)))
		default:
			items = append(items, fmt.Sprintf("HW%d 0x%s", id, hx(le64(d), 2*n)))
		}
	}
	flush()
	return items, bad
}

// itmNotes writes each burst's ITM packets.
// TRLC-LINKS: REQ-SDS-244
func itmNotes(ps []Packet) {
	eachNote(ps, func(b []byte) (string, bool) {
		items, bad := itmParse(b)
		return "ITM " + strings.Join(items, " "), bad
	})
}

// itmDetect: an ITM synchronization packet (five or more zeros then 0x80)
// and the bytes after it all ITM packets.
// TRLC-LINKS: REQ-SDS-247
func itmDetect(ps []Packet) string {
	for _, p := range ps {
		b := p.Bytes
		for k := 0; k+6 <= len(b); k++ {
			if b[k] == 0 && b[k+1] == 0 && b[k+2] == 0 && b[k+3] == 0 && b[k+4] == 0 && b[k+5] == 0x80 {
				items, bad := itmParse(b[k:])
				if !bad && len(items) >= 2 {
					return "ITM synchronization packet (00 00 00 00 00 80) and stimulus packets after it"
				}
			}
		}
	}
	return ""
}

// itmMatch: the ITM packet header that opens the burst (its channel).
// TRLC-LINKS: REQ-SDS-248
func itmMatch(p Packet) ([]string, int) {
	if len(p.Bytes) == 0 {
		return nil, -1
	}
	return hexToks(p.Bytes[:1]), -1
}

// ---- ARM TPIU (formatted trace port) ----

// tpiuDemux reads TPIU frames of 16 bytes (after a synchronization FF FF FF
// 7F, else from the burst's first byte): byte 15 holds the low bits of the
// even bytes; an even byte with bit 0 set is a stream ID (taking effect
// after the next byte when its low bit says so), else data; odd bytes are
// data. It returns the bytes of each stream in order.
// TRLC-LINKS: REQ-SDS-244
func tpiuDemux(b []byte) (ids []int, data [][]byte, cut bool) {
	start := 0
	for k := 0; k+4 <= len(b); k++ {
		if b[k] == 0xff && b[k+1] == 0xff && b[k+2] == 0xff && b[k+3] == 0x7f {
			start = k + 4
		}
	}
	cur := -1
	emit := func(v byte) {
		if cur < 0 {
			return
		}
		if len(ids) == 0 || ids[len(ids)-1] != cur {
			ids = append(ids, cur)
			data = append(data, nil)
		}
		data[len(data)-1] = append(data[len(data)-1], v)
	}
	i := start
	for ; i+16 <= len(b); i += 16 {
		f := b[i : i+16]
		low := f[15]
		for k := 0; k < 15; k += 2 {
			lb := low >> (k / 2) & 1
			delayed := -1
			if f[k]&1 != 0 {
				if lb != 0 {
					delayed = int(f[k] >> 1)
				} else {
					cur = int(f[k] >> 1)
				}
			} else {
				emit(f[k] | lb)
			}
			if k < 14 {
				emit(f[k+1])
			}
			if delayed >= 0 {
				cur = delayed
			}
		}
	}
	return ids, data, i < len(b)
}

// tpiuNotes writes each burst's TPIU streams: stream 1 read as ITM (the
// usual ATB ID of a Cortex-M's ITM), the others as bytes.
// TRLC-LINKS: REQ-SDS-244
func tpiuNotes(ps []Packet) {
	eachNote(ps, func(b []byte) (string, bool) {
		ids, data, cut := tpiuDemux(b)
		var parts []string
		bad := cut
		for k, id := range ids {
			if id == 1 {
				items, ib := itmParse(data[k])
				bad = bad || ib
				parts = append(parts, "ITM "+strings.Join(items, " "))
				continue
			}
			parts = append(parts, fmt.Sprintf("id%d %s", id, hexList(data[k], 8)))
		}
		if len(parts) == 0 {
			parts = append(parts, "(no stream)")
		}
		s := "TPIU " + strings.Join(parts, "; ")
		if cut {
			s += " (frame cut)"
		}
		return s, bad
	})
}

// tpiuDetect: a TPIU synchronization (FF FF FF 7F) and a whole frame after.
// TRLC-LINKS: REQ-SDS-247
func tpiuDetect(ps []Packet) string {
	for _, p := range ps {
		b := p.Bytes
		for k := 0; k+20 <= len(b); k++ {
			if b[k] == 0xff && b[k+1] == 0xff && b[k+2] == 0xff && b[k+3] == 0x7f {
				return "TPIU frame synchronization FF FF FF 7F"
			}
		}
	}
	return ""
}

// ---- ARM ETMv3 (instruction trace) ----

// etmState is the ETM layer's memory: the last branch address and the
// processor state, which shorter branch addresses build on.
type etmState struct {
	addr  int64
	thumb bool
}

// etmVarint reads a value seven bits a byte, bit 7 continuing.
// TRLC-LINKS: REQ-SDS-244
func etmVarint(b []byte, max int) (v int64, n int, ok bool) {
	for n < len(b) && n < max {
		v |= int64(b[n]&0x7f) << (7 * n)
		n++
		if b[n-1]&0x80 == 0 {
			return v, n, true
		}
	}
	return v, n, false
}

// parse reads ETMv3 packets (the alternative branch encoding, no context
// ID, no cycle accuracy): A-sync, I-sync with its address and reason,
// branch addresses (compressed against the last), P-header atoms, trigger,
// exception entry and exit, timestamps and the ones it skips.
// TRLC-LINKS: REQ-SDS-244
func (st *etmState) parse(b []byte) (items []string, bad bool) {
	for i := 0; i < len(b); {
		h := b[i]
		switch {
		case h == 0x00:
			j := i
			for j < len(b) && b[j] == 0 {
				j++
			}
			if j < len(b) && b[j] == 0x80 && j-i >= 4 {
				items = append(items, "A-SYNC")
				i = j + 1
			} else {
				items = append(items, "(zeros)")
				i = j
			}
		case h&1 == 1: // branch
			a, n, ok := etmVarint(b[i:], 5)
			if !ok {
				items = append(items, "B (cut)")
				return items, true
			}
			bits := 7*n - 1
			exc := n >= 2 && a>>bits&1 != 0
			if exc {
				a &^= 1 << bits
			}
			if n == 5 {
				st.thumb = b[i+4]&0xb0 == 0x10
			}
			full := bits
			if st.thumb {
				a &^= 1
			} else {
				a = (a &^ 1) << 1
				full++
			}
			if n < 5 {
				a |= st.addr &^ (1<<full - 1)
			}
			a &= 0xffffffff
			st.addr = a
			s := "B 0x" + hx(a, 8)
			i += n
			if exc {
				e, m, ok := etmVarint(b[i:], 3)
				if !ok {
					items = append(items, s+" (cut)")
					return items, true
				}
				s += fmt.Sprintf(" exc %d", (e>>1&0x0f)|(e>>7&0x1f0))
				i += m
			}
			items = append(items, s)
		case h == 0x08: // I-sync
			if i+6 > len(b) {
				items = append(items, "I-SYNC (cut)")
				return items, true
			}
			info := b[i+1]
			a := le64(b[i+2 : i+6])
			st.thumb = a&1 == 1
			st.addr = a &^ 1
			reason := []string{"periodic", "trace on", "overflow", "debug exit"}[info>>5&3]
			items = append(items, fmt.Sprintf("I-SYNC 0x%s %s %s", hx(st.addr, 8),
				map[bool]string{true: "thumb", false: "arm"}[st.thumb], reason))
			i += 6
		case h == 0x0c:
			items = append(items, "TRIGGER")
			i++
		case h == 0x76:
			items = append(items, "EXC EXIT")
			i++
		case h == 0x7e:
			items = append(items, "EXC ENTRY")
			i++
		case h == 0x66:
			i++
		case h&0x83 == 0x80: // P-header: E executed atoms, N not
			e, nn := int(h>>2&15), int(h>>6&1)
			s := fmt.Sprintf("%dE", e)
			if nn > 0 {
				s += fmt.Sprintf(" %dN", nn)
			}
			items = append(items, s)
			i++
		case h&0xf3 == 0x82:
			items = append(items, "2 atoms "+map[bool]string{true: "N", false: "E"}[h&8 != 0]+map[bool]string{true: "N", false: "E"}[h&4 != 0])
			i++
		case h&0xfb == 0x42: // timestamp
			_, n, ok := etmVarint(b[i+1:], 9)
			if !ok {
				items = append(items, "TS (cut)")
				return items, true
			}
			items = append(items, "TS")
			i += 1 + n
		case h == 0x04: // cycle count
			_, n, ok := etmVarint(b[i+1:], 5)
			if !ok {
				items = append(items, "CYC (cut)")
				return items, true
			}
			items = append(items, "CYC")
			i += 1 + n
		default:
			items = append(items, "? 0x"+hx(int(h), 2))
			return items, true
		}
	}
	return items, bad
}

// etmNotes writes each burst's ETM packets, the branch state carried on.
// TRLC-LINKS: REQ-SDS-244
func etmNotes(ps []Packet) {
	st := &etmState{}
	eachNote(ps, func(b []byte) (string, bool) {
		items, bad := st.parse(b)
		return "ETM " + strings.Join(items, " "), bad
	})
}

// ---- Amulet LCD ASCII protocol ----

// amuletCmds name the Amulet commands and replies and say what follows the
// command byte: 'r' a two-character address, 'b' address and a byte value
// (2 characters), 'w' address and a word (4), 's' address and a string to
// NUL, 'p' a page change (02, the index, a checksum), 'c' coordinates (four
// 16-bit values, 16 characters), 'a' address and a list to NUL, '-' nothing.
var amuletCmds = map[int][2]string{0xa0: {"PAGE", "p"}, 0xd0: {"GBV", "r"}, 0xd1: {"GWV", "r"}, 0xd2: {"GSV", "r"},
	0xd3: {"GLV", "r"}, 0xd4: {"GRPC", "r"}, 0xd5: {"SBV", "b"}, 0xd6: {"SWV", "w"}, 0xd7: {"SSV", "s"}, 0xd8: {"RPC", "r"},
	0xd9: {"LINE", "c"}, 0xda: {"RECT", "c"}, 0xdb: {"FRECT", "c"}, 0xdc: {"PIXEL", "c"}, 0xdd: {"GBVA", "r"},
	0xde: {"GWVA", "r"}, 0xdf: {"SBVA", "a"}, 0xe0: {"GBVR", "b"}, 0xe1: {"GWVR", "w"}, 0xe2: {"GSVR", "s"},
	0xe3: {"GLVR", "s"}, 0xe4: {"GRPCR", "r"}, 0xe5: {"SBVR", "b"}, 0xe6: {"SWVR", "w"}, 0xe7: {"SSVR", "s"},
	0xe8: {"RPCR", "r"}, 0xe9: {"LINER", "c"}, 0xea: {"RECTR", "c"}, 0xeb: {"FRECTR", "c"}, 0xec: {"PIXELR", "c"},
	0xed: {"GBVAR", "a"}, 0xee: {"GWVAR", "a"}, 0xef: {"SBVAR", "a"}, 0xf0: {"ACK", "-"}, 0xf1: {"NACK", "-"},
	0xf2: {"SWVA", "a"}, 0xf3: {"SWVAR", "a"}, 0xf4: {"GCV", "r"}, 0xf5: {"GCVR", "w"}, 0xf6: {"SCV", "w"},
	0xf7: {"SCVR", "w"}}

// amuletHex reads n ASCII hex characters.
// TRLC-LINKS: REQ-SDS-244
func amuletHex(b []byte) (int, bool) {
	v := 0
	for _, c := range b {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		default:
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

// amuletNote reads a burst's Amulet commands and replies.
// TRLC-LINKS: REQ-SDS-244
func amuletNote(b []byte) (string, bool) {
	var parts []string
	for i := 0; i < len(b); {
		c, ok := amuletCmds[int(b[i])]
		if !ok {
			parts = append(parts, "? 0x"+hx(int(b[i]), 2))
			return joinNotes(parts), true
		}
		name, form := c[0], c[1]
		i++
		need := map[string]int{"r": 2, "b": 4, "w": 6, "c": 16, "-": 0, "p": 4, "s": 2, "a": 2}[form]
		if i+need > len(b) {
			parts = append(parts, name+" (cut)")
			return joinNotes(parts), true
		}
		f := b[i : i+need]
		addr, aok := amuletHex(f[:min(2, len(f))])
		s := name
		switch form {
		case "r":
			s += " 0x" + hx(addr, 2)
		case "b", "w":
			v, vok := amuletHex(f[2:])
			aok = aok && vok
			s += fmt.Sprintf(" 0x%s = 0x%s", hx(addr, 2), hx(v, len(f)-2))
		case "c":
			var xy []string
			for k := 0; k < 4; k++ {
				v, vok := amuletHex(f[4*k : 4*k+4])
				aok = aok && vok
				xy = append(xy, fmt.Sprint(v))
			}
			s += " " + strings.Join(xy, ",")
		case "p":
			sum := (0xa0 + int(f[0]) + int(f[1]) + int(f[2]) + int(f[3])) & 0xff
			page, pok := amuletHex(f[1:3])
			aok = f[0] == 0x02 && sum == 0 && pok
			s += " 0x" + hx(page, 2)
		case "s", "a":
			j := i + 2
			for j < len(b) && b[j] != 0 {
				j++
			}
			if j >= len(b) {
				parts = append(parts, name+" (no NUL)")
				return joinNotes(parts), true
			}
			s += " 0x" + hx(addr, 2) + " " + quoted(b[i+2:j], 24)
			need = j + 1 - i
		}
		if form == "-" {
			aok = true
		}
		if !aok {
			parts = append(parts, s+" (bad)")
			return joinNotes(parts), true
		}
		parts = append(parts, s)
		i += need
	}
	return joinNotes(parts), false
}

// ---- Panasonic PAN1321 (Bluetooth SPP module AT commands) ----

// panState is the PAN1321 layer's memory: the line read so far (commands
// and replies end with CR LF and may span bursts).
type panState struct{ line []byte }

// note reads the lines a burst ends: the host's AT+J commands (name,
// security, auto-accept, data to send, reset, production mode) and the
// module's replies (ROK, OK, ERR=).
// TRLC-LINKS: REQ-SDS-244
func (st *panState) note(b []byte) (string, bool) {
	var parts []string
	bad := false
	for _, v := range b {
		st.line = append(st.line, v)
		n := len(st.line)
		if n < 2 || st.line[n-2] != '\r' || v != '\n' {
			continue
		}
		s := string(st.line[:n-2])
		st.line = st.line[:0]
		switch {
		case strings.HasPrefix(s, "AT+JSLN"):
			name := s
			if k := strings.IndexByte(s, ','); k >= 0 {
				name = s[k+1:]
			}
			parts = append(parts, "name '"+printable(name)+"'")
		case strings.HasPrefix(s, "AT+JSEC"):
			pin := s
			if len(s) >= 4 {
				pin = s[len(s)-4:]
			}
			parts = append(parts, "PIN "+printable(pin))
		case strings.HasPrefix(s, "AT+JAAC="):
			parts = append(parts, map[bool]string{true: "auto-accept on", false: "auto-accept off"}[s[8:] == "1"])
			bad = bad || (s[8:] != "0" && s[8:] != "1")
		case strings.HasPrefix(s, "AT+JPRO="):
			parts = append(parts, "production mode "+printable(s[8:]))
		case s == "AT+JRES":
			parts = append(parts, "reset")
		case strings.HasPrefix(s, "AT+JSDA="):
			l, d, _ := strings.Cut(s[8:], ",")
			parts = append(parts, fmt.Sprintf("send %s: %s", printable(l), quoted([]byte(d), 16)))
			bad = bad || fmt.Sprint(len(d)) != l
		case s == "ROK":
			parts = append(parts, "ready")
		case s == "OK":
			parts = append(parts, "OK")
		case strings.HasPrefix(s, "ERR"):
			parts = append(parts, "ERR "+printable(strings.TrimPrefix(strings.TrimPrefix(s, "ERR"), "=")))
			bad = true
		default:
			parts = append(parts, quoted([]byte(s), 24))
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return "PAN1321 " + joinNotes(parts), bad
}

// panNotes reads a record's PAN1321 lines in order.
// TRLC-LINKS: REQ-SDS-244
func panNotes(ps []Packet) {
	st := &panState{}
	eachNote(ps, st.note)
}

// panDetect: a PAN1321 command (AT+J...) ending in CR LF.
// TRLC-LINKS: REQ-SDS-247
func panDetect(ps []Packet) string {
	for _, p := range ps {
		s := string(p.Bytes)
		if k := strings.Index(s, "AT+J"); k >= 0 && strings.Contains(s[k:], "\r\n") {
			return "PAN1321 command AT+J ending in CR LF"
		}
	}
	return ""
}
