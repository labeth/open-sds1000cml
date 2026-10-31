// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The protocols framed on a UART character (LIN, DMX512, MIDI, Modbus RTU)
// read characters with uartChars: a start bit at each falling edge from idle,
// eight data bits least significant first, an optional parity bit and the
// stop bits, sampled mid-bit on the edge's interpolated crossing. A zero
// character whose line stays low for breakBits or more is a break, the frame
// start of LIN and DMX512. Mirrored step for step by decode_uartproto.js.

// uchar is one character read off the line.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
type uchar struct {
	x0, x1 float64 // the start bit's edge, the end of the last stop bit (a break: its rising edge)
	val    int
	kind   string  // data | parity-error | frame-error | break
	low    float64 // a break's low run, in bits
}

// uartChars reads the characters of a line idling high at spb samples a bit:
// parity "even", "odd" or "none", stops 1 or 2 (only the first is checked,
// as receivers do). A zero character with its stop bit low whose low run
// lasts breakBits or more is a break (breakBits 0: none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func uartChars(S sliced, spb float64, parity string, stops int, breakBits float64) []uchar {
	out := scratchSlice[uchar](S.sc, len(S.edges)/4+1) // a character takes two edges or more
	lv := levelCursor{s: &S}                           // logicAt, walking the edges forward
	pc := 0
	if parity != "none" {
		pc = 1
	}
	next := math.Inf(-1)
	for k := 0; k < len(S.edges); k++ {
		e := S.edges[k]
		if e.dir >= 0 || e.x < next {
			continue
		}
		x0 := e.x
		if lv.at(x0+0.5*spb) != 0 {
			continue // a glitch, no start bit
		}
		val, off := 0, false
		for c := 0; c < 8; c++ {
			b := lv.at(x0 + (1.5+float64(c))*spb)
			if b < 0 {
				off = true
				break
			}
			val |= b << c
		}
		if off {
			break
		}
		kind := "data"
		if pc == 1 {
			pb := lv.at(x0 + 9.5*spb)
			if pb < 0 {
				break
			}
			want := popcount(val) & 1
			if parity == "odd" {
				want = 1 - want
			}
			if pb != want {
				kind = "parity-error"
			}
		}
		sb := lv.at(x0 + (9.5+float64(pc))*spb)
		if sb < 0 {
			break
		}
		x1 := x0 + float64(9+pc+stops)*spb
		ch := uchar{x0: x0, x1: x1, val: val, kind: kind}
		if sb != 1 {
			ch.kind = "frame-error"
			if val == 0 && breakBits > 0 {
				rise := float64(S.n)
				if k+1 < len(S.edges) {
					rise = S.edges[k+1].x
				}
				if low := (rise - x0) / spb; low >= breakBits {
					if k+1 >= len(S.edges) {
						break // the record ends inside the break
					}
					ch.kind, ch.low, ch.x1 = "break", low, rise
				}
			}
		}
		out = append(out, ch)
		next = ch.x1 - 0.5*spb
		if ch.kind == "break" {
			next = ch.x1
		}
	}
	return out
}

// ui clamps a sample position into a record of n samples, rounded.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func ui(x float64, n int) int {
	i := int(math.Round(x))
	if i < 0 {
		return 0
	}
	if i > n-1 {
		return n - 1
	}
	return i
}

// LINCfg configures the LIN decode: Baud 0 reads the rate off the sync
// fields (snapped to a LIN rate within 2 %).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
type LINCfg struct {
	Baud      int
	Threshold float64
	HaveThr   bool
	Format    string
}

// linRates are the bit rates LIN buses run at.
var linRates = []float64{1200, 2400, 4800, 9600, 10417, 19200, 20000}

// linBreakBits is the shortest low run read as a LIN break: the standard's
// 13 bits less a master clock's tolerance.
const linBreakBits = 11.0

// linPIDParity is the protected identifier with its two parity bits for a
// 6-bit frame ID (P0 = ID0^ID1^ID2^ID4, P1 = !(ID1^ID3^ID4^ID5)).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func linPIDParity(id int) int {
	b := func(k int) int { return (id >> k) & 1 }
	p0 := b(0) ^ b(1) ^ b(2) ^ b(4)
	p1 := 1 - (b(1) ^ b(3) ^ b(4) ^ b(5))
	return id&0x3f | p0<<6 | p1<<7
}

// linChecksum is the inverted eight-bit sum with carry of the data bytes,
// with the protected identifier first for the enhanced checksum.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func linChecksum(pid int, data []int, enhanced bool) int {
	s := 0
	if enhanced {
		s = pid
	}
	for _, d := range data {
		s += d
		if s > 0xff {
			s -= 0xff
		}
	}
	return 0xff - s
}

// linSyncSPB finds the bit time of the sync fields that follow breaks: a
// break (a low run) then, after the delimiter, 0x55's ten evenly spaced
// edges. The median over the record's sync fields; 0 when none is seen.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func linSyncSPB(S sliced) float64 {
	var cands []float64
	E := S.edges
	for k := 0; k+11 < len(E); k++ {
		if E[k].dir >= 0 {
			continue
		}
		f, r9 := E[k+2].x, E[k+11].x
		spb := (r9 - f) / 9
		if !(spb > 0) || E[k+1].x-E[k].x < linBreakBits*spb || E[k+2].dir >= 0 {
			continue
		}
		ok := true
		for j := k + 2; j < k+11; j++ {
			g := E[j+1].x - E[j].x
			if g < 0.6*spb || g > 1.4*spb {
				ok = false
				break
			}
		}
		if ok {
			cands = append(cands, spb)
		}
	}
	if len(cands) == 0 {
		return 0
	}
	sort.Float64s(cands)
	return cands[len(cands)/2]
}

// snapSPB moves a bit time onto the nearest of rates within tol (a fraction).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func snapSPB(spb, colTimeS float64, rates []float64, tol float64) float64 {
	if !(spb > 0) || !(colTimeS > 0) {
		return spb
	}
	baud := 1 / (spb * colTimeS)
	for _, r := range rates {
		if math.Abs(baud-r) <= tol*r {
			return 1 / (r * colTimeS)
		}
	}
	return spb
}

// DecodeLIN decodes LIN frames: a break, the 0x55 sync field, the protected
// identifier (its two parity bits checked) and the response bytes up to the
// next break or two characters of idle, the last of them the checksum,
// which checks as enhanced (identifier included; not for the diagnostic IDs
// 0x3C and 0x3D) or classic. Spans: "sof" BREAK, "rw" SYNC, then the units:
// the PID ("ID3C"), the data bytes and the checksum ("CK5A"); a bad parity
// or checksum is that unit's error span. OK needs a frame whose sync and
// identifier read right and whose checksum checks (or no response).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func DecodeLIN(codes []uint8, colTimeS float64, cfg LINCfg) Result {
	return decodeLIN(samplesIn(codes), colTimeS, cfg)
}

// DecodeLINEdges is DecodeLIN over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func DecodeLINEdges(codes *EdgeLine, colTimeS float64, cfg LINCfg) Result {
	return decodeLIN(edgesIn(codes), colTimeS, cfg)
}

// decodeLIN is DecodeLIN over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func decodeLIN(codes lineIn, colTimeS float64, cfg LINCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "lin", Error: S.reason}
	}
	var spb float64
	if cfg.Baud > 0 && colTimeS > 0 {
		spb = 1 / (float64(cfg.Baud) * colTimeS)
	} else {
		spb = snapSPB(linSyncSPB(S), colTimeS, linRates, 0.02)
	}
	if !(spb >= 3) {
		return Result{Proto: "lin", Error: "no LIN break and sync field"}
	}
	chars := uartChars(S, spb, "none", 1, linBreakBits)
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	unit := func(c uchar, v int, txt, kind string) {
		if kind != "data" {
			txt = "!" + txt
		}
		spans = append(spans, Span{ui(c.x0, n), ui(c.x1, n), txt, kind, v})
		toks = append(toks, txt)
		units = append(units, v)
	}
	for k := 0; k < len(chars); k++ {
		if chars[k].kind != "break" {
			continue
		}
		b := chars[k]
		spans = append(spans, Span{ui(b.x0, n), ui(b.x1, n), "BRK", "sof", 0})
		toks = append(toks, "BRK")
		if k+1 >= len(chars) || chars[k+1].kind == "break" {
			continue
		}
		s := chars[k+1]
		if s.kind != "data" || s.val != 0x55 {
			spans = append(spans, Span{ui(s.x0, n), ui(s.x1, n), "!SYNC", "frame-error", s.val})
			toks = append(toks, "!SYNC")
			continue
		}
		spans = append(spans, Span{ui(s.x0, n), ui(s.x1, n), "SYNC", "rw", 0x55})
		toks = append(toks, "SYNC")
		k++
		if k+1 >= len(chars) || chars[k+1].kind == "break" || chars[k+1].x0-s.x1 > 20*spb {
			continue
		}
		k++
		p := chars[k]
		pidKind := p.kind
		if pidKind == "data" && linPIDParity(p.val&0x3f) != p.val {
			pidKind = "parity-error"
		}
		unit(p, p.val, fmt.Sprintf("ID%02X", p.val&0x3f), pidKind)
		// the response: characters each within two characters of the last
		var resp []uchar
		last := p.x1
		for k+1 < len(chars) && chars[k+1].kind != "break" && chars[k+1].x0-last <= 20*spb {
			k++
			resp = append(resp, chars[k])
			last = chars[k].x1
		}
		if len(resp) == 0 {
			good = good || pidKind == "data" // a header no node answered
			continue
		}
		var data []int
		for _, c := range resp[:len(resp)-1] {
			unit(c, c.val, FmtByte(c.val, cfg.Format), c.kind)
			data = append(data, c.val)
		}
		ck := resp[len(resp)-1]
		kind := ck.kind
		if kind == "data" {
			id := p.val & 0x3f
			enh := id != 0x3c && id != 0x3d && linChecksum(p.val, data, true) == ck.val
			if !enh && linChecksum(p.val, data, false) != ck.val {
				kind = "frame-error"
			}
		}
		unit(ck, ck.val, "CK"+hex2(ck.val), kind)
		good = good || pidKind == "data" && kind == "data"
	}
	if len(spans) == 0 {
		return Result{Proto: "lin", Error: "no LIN break"}
	}
	res := Result{OK: good, Proto: "lin", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Baud: int(math.Round(1 / (spb * colTimeS))), SPB: spb, Thr: S.threshold}
	if !good {
		res.Error = "no LIN frame with a valid identifier"
	}
	return res
}

// DMXCfg configures the DMX512 decode; Baud 0 is the standard 250 kbit/s.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
type DMXCfg struct {
	Baud      int
	Threshold float64
	HaveThr   bool
	Format    string
}

// dmxBreakBits is DMX512's shortest break, 88 us at 250 kbit/s.
const dmxBreakBits = 22.0

// DecodeDMX512 decodes DMX512 packets: a break of 88 us or more, the mark
// after break, the start code (slot 0, "SC00") and the slots, 8N2 at
// 250 kbit/s. Spans: "sof" BRK, "rw" MAB, then a unit per slot. OK needs a
// packet with a start code and a slot after it, all framed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func DecodeDMX512(codes []uint8, colTimeS float64, cfg DMXCfg) Result {
	return decodeDMX512(samplesIn(codes), colTimeS, cfg)
}

// DecodeDMX512Edges is DecodeDMX512 over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func DecodeDMX512Edges(codes *EdgeLine, colTimeS float64, cfg DMXCfg) Result {
	return decodeDMX512(edgesIn(codes), colTimeS, cfg)
}

// decodeDMX512 is DecodeDMX512 over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func decodeDMX512(codes lineIn, colTimeS float64, cfg DMXCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "dmx512", Error: S.reason}
	}
	baud := cfg.Baud
	if baud <= 0 {
		baud = 250000
	}
	if !(colTimeS > 0) {
		return Result{Proto: "dmx512", Error: "no sample time"}
	}
	spb := 1 / (float64(baud) * colTimeS)
	if !(spb >= 3) {
		return Result{Proto: "dmx512", Error: fmt.Sprintf("%.1f samples/bit; need >= 3", spb)}
	}
	chars := uartChars(S, spb, "none", 2, dmxBreakBits)
	n := S.n
	// Sized for the characters: a DMX512 packet is hundreds of them.
	spans := make([]Span, 0, len(chars)+len(chars)/8+2)
	toks := make([]string, 0, cap(spans))
	units := make([]int, 0, len(chars))
	good := false
	for k := 0; k < len(chars); k++ {
		if chars[k].kind != "break" {
			continue
		}
		b := chars[k]
		spans = append(spans, Span{ui(b.x0, n), ui(b.x1, n), "BRK", "sof", 0})
		toks = append(toks, "BRK")
		if k+1 >= len(chars) || chars[k+1].kind == "break" {
			continue
		}
		if mab := chars[k+1].x0 - b.x1; mab >= 1.5*spb {
			spans = append(spans, Span{ui(b.x1, n), ui(chars[k+1].x0, n), "MAB", "rw", 0})
			toks = append(toks, "MAB")
		}
		slot, clean := 0, true
		last := b.x1
		for k+1 < len(chars) && chars[k+1].kind != "break" && chars[k+1].x0-last <= 44*spb && slot <= 512 {
			k++
			c := chars[k]
			var txt string
			switch {
			case slot == 0 && c.kind != "data":
				txt, clean = hexBangSC[c.val&0xff], false
			case slot == 0:
				txt = hexSC[c.val&0xff]
			case c.kind != "data":
				txt, clean = "!"+FmtByte(c.val, cfg.Format), false
			default:
				txt = FmtByte(c.val, cfg.Format)
			}
			spans = append(spans, Span{ui(c.x0, n), ui(c.x1, n), txt, c.kind, c.val})
			toks = append(toks, txt)
			units = append(units, c.val)
			last = c.x1
			slot++
		}
		if slot >= 2 && clean {
			good = true
		}
	}
	if len(spans) == 0 {
		return Result{Proto: "dmx512", Error: "no DMX512 break"}
	}
	if len(units) == 0 {
		units = nil // as a decode with no slot has always read
	}
	res := Result{OK: good, Proto: "dmx512", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Baud: baud, SPB: spb, Thr: S.threshold}
	if !good {
		res.Error = "no complete DMX512 packet"
	}
	return res
}

// MIDICfg configures the MIDI decode; Baud 0 is the standard 31250 Bd.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
type MIDICfg struct {
	Baud      int
	Threshold float64
	HaveThr   bool
	Format    string
}

// midiDataLen is the data bytes a status byte takes (-1: a system exclusive
// message, up to its end; real-time bytes take none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func midiDataLen(st int) int {
	switch st >> 4 {
	case 0x8, 0x9, 0xA, 0xB, 0xE:
		return 2
	case 0xC, 0xD:
		return 1
	}
	switch st {
	case 0xF0:
		return -1
	case 0xF1, 0xF3:
		return 1
	case 0xF2:
		return 2
	}
	return 0
}

// midiName names a status byte, a channel message with its channel 1..16.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func midiName(st int) string {
	ch := []string{"OFF", "ON", "PAT", "CC", "PC", "CAT", "PB"}
	if st < 0xF0 {
		return fmt.Sprintf("%s%d", ch[(st>>4)-8], st&0xf+1)
	}
	sys := map[int]string{0xF0: "SYX", 0xF1: "MTC", 0xF2: "SPP", 0xF3: "SNG", 0xF6: "TUN", 0xF7: "EOX",
		0xF8: "CLK", 0xFA: "START", 0xFB: "CONT", 0xFC: "STOP", 0xFE: "SENS", 0xFF: "RST"}
	if s, ok := sys[st]; ok {
		return s
	}
	return "F" + fmt.Sprintf("%X", st&0xf)
}

// DecodeMIDI decodes MIDI messages (31250 Bd 8N1): each status byte opens
// a message ("sof", then the status unit named "ON1", "CC10", "SYX"...),
// data bytes follow (running status: a channel message's data repeat with no
// status), real-time bytes may come between. A data byte with no message to
// belong to is an error. OK needs a complete message.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func DecodeMIDI(codes []uint8, colTimeS float64, cfg MIDICfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "midi", Error: S.reason}
	}
	baud := cfg.Baud
	if baud <= 0 {
		baud = 31250
	}
	if !(colTimeS > 0) {
		return Result{Proto: "midi", Error: "no sample time"}
	}
	spb := 1 / (float64(baud) * colTimeS)
	if !(spb >= 3) {
		return Result{Proto: "midi", Error: fmt.Sprintf("%.1f samples/bit; need >= 3", spb)}
	}
	chars := uartChars(S, spb, "none", 1, 0)
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	run, need, got := 0, 0, 0 // running status, its data bytes, those read
	complete := 0
	for _, c := range chars {
		i0, i1 := ui(c.x0, n), ui(c.x1, n)
		if c.kind != "data" {
			spans = append(spans, Span{i0, i1, "!" + hex2(c.val), "frame-error", c.val})
			toks = append(toks, "!"+hex2(c.val))
			units = append(units, c.val)
			run, need, got = 0, 0, 0
			continue
		}
		v := c.val
		switch {
		case v >= 0xF8: // real time: between any bytes
			spans = append(spans, Span{i0, i1, midiName(v), "data", v})
			toks = append(toks, midiName(v))
			units = append(units, v)
			complete++
			continue
		case v >= 0x80:
			wasSyx := run == 0xF0
			spans = append(spans, Span{i0, i0, "", "sof", 0})
			spans = append(spans, Span{i0, i1, midiName(v), "data", v})
			toks = append(toks, midiName(v))
			units = append(units, v)
			need, got = midiDataLen(v), 0
			switch {
			case v < 0xF0 || v == 0xF0 || need > 0:
				run = v
			default: // system common without data ends running status
				if v != 0xF7 || wasSyx {
					complete++
				}
				run = 0
			}
			continue
		}
		units = append(units, v)
		if run == 0 || (need == 0 && run != 0xF0) {
			spans = append(spans, Span{i0, i1, "!" + hex2(v), "frame-error", v})
			toks = append(toks, "!"+hex2(v))
			continue
		}
		txt := FmtByte(v, cfg.Format)
		spans = append(spans, Span{i0, i1, txt, "data", v})
		toks = append(toks, txt)
		if need > 0 {
			got++
			if got == need {
				complete++
				got = 0
				if run >= 0xF0 {
					run, need = 0, 0
				}
			}
		}
	}
	if len(spans) == 0 {
		return Result{Proto: "midi", Error: "no MIDI bytes"}
	}
	res := Result{OK: complete > 0, Proto: "midi", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Baud: baud, SPB: spb, Thr: S.threshold}
	if complete == 0 {
		res.Error = "no complete MIDI message"
	}
	return res
}

// ModbusCfg configures the Modbus RTU decode: Baud 0 infers the rate,
// Parity "" tries even (the RTU default), none with two stop bits, odd and
// none with one, keeping the framing with the most CRC-valid frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
type ModbusCfg struct {
	Baud      int
	Parity    string
	Threshold float64
	HaveThr   bool
	Format    string
}

// modbusCRC is CRC-16/MODBUS: the reflected polynomial 0xA001 from all
// ones, sent low byte first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func modbusCRC(b []int) int {
	c := 0xFFFF
	for _, v := range b {
		c ^= v & 0xff
		for i := 0; i < 8; i++ {
			if c&1 != 0 {
				c = c>>1 ^ 0xA001
			} else {
				c >>= 1
			}
		}
	}
	return c
}

// modbusFrames groups characters into RTU frames, split where the line idles
// more than two character times (the standard's 3.5 between frames, at most
// 1.5 within one).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func modbusFrames(chars []uchar, spb float64) [][]uchar {
	var out [][]uchar
	var cur []uchar
	for _, c := range chars {
		if len(cur) > 0 && c.x0-cur[len(cur)-1].x1 > 22*spb {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// modbusGood counts a framing's CRC-valid frames and its character errors.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func modbusGood(frames [][]uchar) (good, bad int) {
	for _, f := range frames {
		ok := len(f) >= 4
		vals := make([]int, len(f))
		for i, c := range f {
			vals[i] = c.val
			if c.kind != "data" {
				bad++
				ok = false
			}
		}
		if ok && modbusCRC(vals[:len(f)-2]) == vals[len(f)-2]|vals[len(f)-1]<<8 {
			good++
		}
	}
	return good, bad
}

// DecodeModbus decodes Modbus RTU frames: the address ("A01"), the function
// ("F03", an exception response "F83!"), the data bytes and the CRC (two
// bytes, low first; "!" on both when it does not check). Each frame opens
// with a "sof". OK needs a CRC-valid frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func DecodeModbus(codes []uint8, colTimeS float64, cfg ModbusCfg) Result {
	return decodeModbus(samplesIn(codes), colTimeS, cfg)
}

// DecodeModbusEdges is DecodeModbus over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func DecodeModbusEdges(codes *EdgeLine, colTimeS float64, cfg ModbusCfg) Result {
	return decodeModbus(edgesIn(codes), colTimeS, cfg)
}

// decodeModbus is DecodeModbus over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func decodeModbus(codes lineIn, colTimeS float64, cfg ModbusCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "modbus", Error: S.reason}
	}
	var spb float64
	if cfg.Baud > 0 && colTimeS > 0 {
		spb = 1 / (float64(cfg.Baud) * colTimeS)
	} else {
		var reason string
		if spb, reason = inferUARTspb(S); reason != "" {
			return Result{Proto: "modbus", Error: reason}
		}
		spb = snapStandardBaud(spb, colTimeS)
	}
	if !(spb >= 3) {
		return Result{Proto: "modbus", Error: fmt.Sprintf("%.1f samples/bit; need >= 3", spb)}
	}
	type framing struct {
		parity string
		stops  int
	}
	tries := []framing{{"even", 1}, {"none", 2}, {"odd", 1}, {"none", 1}}
	switch cfg.Parity {
	case "even", "odd":
		tries = []framing{{cfg.Parity, 1}}
	case "none":
		tries = []framing{{"none", 2}}
	}
	var frames [][]uchar
	parity, bestGood, bestBad := "", -1, 0
	for _, fr := range tries {
		f := modbusFrames(uartChars(S, spb, fr.parity, fr.stops, 0), spb)
		g, b := modbusGood(f)
		if g > bestGood || (g == bestGood && b < bestBad) {
			frames, parity, bestGood, bestBad = f, fr.parity, g, b
		}
	}
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	for _, f := range frames {
		spans = append(spans, Span{ui(f[0].x0, n), ui(f[0].x0, n), "", "sof", 0})
		vals := make([]int, len(f))
		clean := len(f) >= 4
		for i, c := range f {
			vals[i] = c.val
			clean = clean && c.kind == "data"
		}
		crcOK := clean && modbusCRC(vals[:len(f)-2]) == vals[len(f)-2]|vals[len(f)-1]<<8
		for i, c := range f {
			txt, kind := FmtByte(c.val, cfg.Format), c.kind
			switch {
			case i == 0:
				txt = "A" + hex2(c.val)
			case i == 1:
				txt = "F" + hex2(c.val)
				if c.val >= 0x80 {
					txt += "!"
				}
			case i >= len(f)-2 && len(f) >= 4:
				txt = hex2(c.val)
				if kind == "data" && !crcOK {
					kind = "frame-error"
				}
			}
			if kind != "data" {
				txt = "!" + txt
			}
			spans = append(spans, Span{ui(c.x0, n), ui(c.x1, n), txt, kind, c.val})
			toks = append(toks, txt)
			units = append(units, c.val)
		}
	}
	if len(spans) == 0 {
		return Result{Proto: "modbus", Error: "no characters"}
	}
	res := Result{OK: bestGood > 0, Proto: "modbus", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Baud: int(math.Round(1 / (spb * colTimeS))), SPB: spb, Thr: S.threshold, Parity: parity}
	if bestGood <= 0 {
		res.Error = "no CRC-valid Modbus RTU frame"
	}
	return res
}
