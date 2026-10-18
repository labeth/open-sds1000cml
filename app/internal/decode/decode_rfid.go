// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"strings"
)

// 125 kHz RFID: an EM4100 tag's Manchester data on a demodulated line, and
// the commands a reader writes to EM4205/EM4305 and T55xx tags by stopping
// its field (gaps). The reader's line is read as the field itself (its
// carrier, edges every half field clock) or its envelope (high while the
// field is on). Mirrored step for step by decode_rfid.js.

// rfidCoilHz is the field's frequency unless a decode is given one (Baud).
const rfidCoilHz = 125000.0

// gap is a stop of the reader's field, start and end in samples.
type gap struct{ x0, x1 float64 }

// rfidGaps finds a reader line's gaps: on a carrier (the line's runs
// shorter than three quarters of a field clock) every run of three field
// clocks or more, on an envelope every low run. fc is the field clock in
// samples.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func rfidGaps(S sliced, fc float64) ([]gap, bool) {
	runs := levelRuns(S)
	var lens []float64
	for _, r := range runs {
		if !r.open {
			lens = append(lens, r.x1-r.x0)
		}
	}
	carrier := quantile(lens, 0.5) < 0.75*fc
	var out []gap
	for _, r := range runs {
		if r.open {
			continue
		}
		if (carrier && r.x1-r.x0 >= 3*fc) || (!carrier && r.lvl == 0) {
			out = append(out, gap{r.x0, r.x1})
		}
	}
	return out, carrier
}

// rfidOut collects a decode's spans, transcript and units.
type rfidOut struct {
	n     int
	spans []Span
	toks  []string
	units []int
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func (o *rfidOut) add(x0, x1 float64, text, kind string, val int, unit bool) {
	o.spans = append(o.spans, pspan(x0, x1, o.n, text, kind, val))
	if text != "" {
		o.toks = append(o.toks, text)
	}
	if unit {
		o.units = append(o.units, val)
	}
}

// rbit is one bit a reader wrote, with its extent.
type rbit struct {
	v      int
	x0, x1 float64
}

// bitsMSB is n bits from b[at] on, most significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func bitsMSB(b []rbit, at, n int) int {
	v := 0
	for i := 0; i < n; i++ {
		v = v<<1 | b[at+i].v
	}
	return v
}

// evenOK reports bits b[at..at+n-1] and the parity bit after them even.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func evenOK(b []rbit, at, n int) bool {
	p := 0
	for i := 0; i <= n; i++ {
		p ^= b[at+i].v
	}
	return p == 0
}

// em4305Cmds name the EM4305 commands.
var em4305Cmds = []string{"CMD0", "LOGIN", "WRITE", "CMD3", "READ", "DISABLE", "PROTECT", "CMD7"}

// DecodeEM4305 decodes a reader's commands to an EM4205/EM4305 tag (sigrok's
// em4305): a first field stop of 40 field clocks or more ("sof") opens
// them; then the field's on-times between gaps (of 12 field clocks or
// more) are bits: 15 to 27 field clocks a 0, longer ones a 1 each 32 field
// clocks and a 0 when the gap ending them lasts 15 to 27. Three hundred
// field clocks of field end the command. A 50-bit Login (0, command and
// parity, the password's four bytes each with its parity, column parity,
// stop 0) and a 57-bit Write (0, command, address and parity, the word's
// four bytes, column parity, stop) read as units: the command, the address
// ("A"), the bytes; a failed parity or stop bit flags its unit. Baud is the
// field's frequency (125 kHz unless given). OK needs a command whose checks
// hold.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func DecodeEM4305(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return rfidWrites("em4305", codes, colTimeS, cfg)
}

// DecodeT55xx decodes a reader's commands to a T55xx tag (sigrok's t55xx): a
// start gap of 20 field clocks or more ("sof") opens them; then the field's
// on-times between gaps (of 20 field clocks or more) are bits, 16 to 31
// field clocks a 0 and 48 to 63 a 1; 64 field clocks of field end the
// command. A 70-bit write with its password (opcode, password, lock bit,
// 32-bit word, block address), a 38-bit one without and a 2-bit opcode read
// as units: the opcode ("OP"), the password's and the word's bytes ("P",
// hex), the lock bit ("LK") and the block ("BL"). FrameBits is the bits. Baud
// is the field's frequency. OK needs a whole command.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func DecodeT55xx(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return rfidWrites("t55xx", codes, colTimeS, cfg)
}

// rfidWrites is DecodeEM4305 and DecodeT55xx.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func rfidWrites(proto string, codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: proto, Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: proto, Error: "no sample time"}
	}
	coil := rfidCoilHz
	if cfg.Baud > 0 {
		coil = float64(cfg.Baud)
	}
	fc := 1 / (coil * colTimeS)
	gaps, carrier := rfidGaps(S, fc)
	em := proto == "em4305"
	start, wgap, end := 20.0, 20.0, 64.0
	if em {
		start, wgap, end = 40, 12, 300
	}
	o := &rfidOut{n: S.n}
	good, frameBits := false, 0
	var bits []rbit
	inCmd := false
	last := 0.0 // the end of the last gap that counted
	finish := func() {
		if !inCmd {
			return
		}
		inCmd = false
		ok := false
		if em {
			ok = em4305Fields(o, bits)
		} else {
			ok = t55xxFields(o, bits)
			if ok && frameBits == 0 {
				frameBits = len(bits)
			}
		}
		good = good || ok
		bits = nil
	}
	for _, g := range gaps {
		if inCmd && g.x0-last > end*fc {
			finish()
		}
		glen := g.x1 - g.x0
		if !inCmd {
			if glen > start*fc {
				inCmd, last = true, g.x1
				o.add(g.x0, g.x1, "", "sof", 0, false)
			}
			continue
		}
		if glen <= wgap*fc {
			continue
		}
		d := g.x0 - last
		if em {
			switch {
			case d > 15*fc && d < 27*fc:
				bits = append(bits, rbit{0, last, g.x1})
			case d > 32*fc && d < 300*fc:
				k := int(d / (32 * fc))
				for i := 0; i < k; i++ {
					x := last + float64(i)*32*fc
					bits = append(bits, rbit{1, x, x + 32*fc})
				}
				if glen > 15*fc && glen < 27*fc {
					bits = append(bits, rbit{0, last + float64(k)*32*fc, g.x1})
				}
			}
		} else {
			switch {
			case d > 16*fc && d < 31*fc:
				bits = append(bits, rbit{0, last, g.x0})
			case d > 48*fc && d < 63*fc:
				bits = append(bits, rbit{1, last, g.x0})
			}
		}
		last = g.x1
	}
	if inCmd && float64(S.n)-last > end*fc {
		finish()
	}
	if len(o.spans) == 0 {
		return Result{Proto: proto, Error: "no reader command"}
	}
	res := Result{OK: good, Proto: proto, Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, SPB: fc, Baud: int(math.Round(coil)), FrameBits: frameBits, Inverted: carrier}
	if !good {
		res.Error = "no whole reader command"
	}
	return res
}

// em4305Fields adds an EM4305 command's units: false unless it is a Login
// or a Write whose parities, column parity and stop bit check.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func em4305Fields(o *rfidOut, b []rbit) bool {
	if len(b) != 50 && len(b) != 57 {
		for _, x := range b {
			o.add(x.x0, x.x1, fmt.Sprint(x.v), "rw", x.v, false)
		}
		if len(b) > 0 {
			o.add(b[len(b)-1].x1, b[len(b)-1].x1, "!", "frame-error", 0, false)
		}
		return false
	}
	ok := true
	unit := func(at, n int, txt string, v int, good bool) {
		kind := "data"
		if !good {
			txt, kind = "!"+txt, "parity-error"
			ok = false
		}
		o.add(b[at].x0, b[at+n-1].x1, txt, kind, v, true)
	}
	cmd := bitsMSB(b, 1, 3)
	unit(1, 4, em4305Cmds[cmd], cmd, b[0].v == 0 && evenOK(b, 1, 3))
	at := 5
	if len(b) == 57 {
		a := b[5].v | b[6].v<<1 | b[7].v<<2 | b[8].v<<3
		unit(5, 7, fmt.Sprintf("A%d", a), a, evenOK(b, 5, 6))
		at = 12
	}
	col := 0
	for k := 0; k < 4; k++ {
		v := bitsMSB(b, at+9*k, 8)
		col ^= v
		unit(at+9*k, 9, hex2(v), v, evenOK(b, at+9*k, 8))
	}
	if bitsMSB(b, at+36, 8) != col || b[at+44].v != 0 {
		o.add(b[at+36].x0, b[at+44].x1, "!COL", "frame-error", 0, false)
		ok = false
	}
	return ok
}

// t55xxFields adds a T55xx command's units: false unless it has 70, 38 or 2
// bits.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func t55xxFields(o *rfidOut, b []rbit) bool {
	n := len(b)
	if n != 70 && n != 38 && n != 2 {
		for _, x := range b {
			o.add(x.x0, x.x1, fmt.Sprint(x.v), "rw", x.v, false)
		}
		if n > 0 {
			o.add(b[n-1].x1, b[n-1].x1, "!", "frame-error", 0, false)
		}
		return false
	}
	op := bitsMSB(b, 0, 2)
	o.add(b[0].x0, b[1].x1, fmt.Sprintf("OP%d%d", op>>1, op&1), "data", op, true)
	if n == 2 {
		return true
	}
	at := 2
	word := func(prefix string) {
		for k := 0; k < 4; k++ {
			v := bitsMSB(b, at+8*k, 8)
			o.add(b[at+8*k].x0, b[at+8*k+7].x1, prefix+hex2(v), "data", v, true)
		}
		at += 32
	}
	if n == 70 {
		word("P")
	}
	o.add(b[at].x0, b[at].x1, fmt.Sprintf("LK%d", b[at].v), "data", b[at].v, true)
	at++
	word("")
	blk := bitsMSB(b, at, 3)
	o.add(b[at].x0, b[at+2].x1, fmt.Sprintf("BL%d", blk), "data", blk, true)
	return true
}

// manchesterStream reads a line's runs as Manchester bits of two halves of
// hp samples, a 1 falling at its middle (of the record's first run, whose
// length is unknown, only its last half): a run of other than one or two halves, or two
// halves of one level where a middle should be, breaks the stream, which
// marks it with a bit of -1 and takes up again a half later.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func manchesterStream(runs []lrun, hp float64) []rbit {
	var lv []int
	var xs []float64
	for k := 0; k < len(runs); k++ {
		r := runs[k]
		q := int(math.Round((r.x1 - r.x0) / hp))
		if r.open && q > 2 && k > 0 {
			q = 2
		}
		if k == 0 {
			q = 3 // the record's first run: its last half may open a bit
		}
		if q < 1 || q > 2 {
			// an idle or a glitch; a long run's last half may open a bit
			lv, xs = append(lv, -1), append(xs, r.x0)
			if q > 2 {
				lv, xs = append(lv, r.lvl), append(xs, r.x1-hp)
			}
			continue
		}
		for j := 0; j < q; j++ {
			lv = append(lv, r.lvl)
			xs = append(xs, r.x0+float64(j)*(r.x1-r.x0)/float64(q))
		}
	}
	var bits []rbit
	for j := 0; j+1 < len(lv); {
		if lv[j] < 0 || lv[j+1] < 0 || lv[j] == lv[j+1] {
			if len(bits) > 0 && bits[len(bits)-1].v >= 0 {
				bits = append(bits, rbit{-1, xs[j], xs[j]})
			}
			j++
			continue
		}
		bits = append(bits, rbit{lv[j], xs[j], xs[j] + 2*hp})
		j += 2
	}
	return bits
}

// DecodeEM4100 decodes an EM4100 tag's 64-bit frames on a demodulated line
// (sigrok's em4100): Manchester bits (a 1 falls at its middle) of two
// shortest runs; nine 1s after a 0 or an idle open a frame ("sof"), then ten rows of
// four bits and their even parity, four column parities and a stop 0. The
// rows make five units: the version or customer byte and four data bytes,
// each flagged when a row's parity fails; a failed column parity or stop
// bit is flagged after them. Baud is the bit rate. OK needs a frame whose
// checks hold.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func DecodeEM4100(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "em4100", Error: S.reason}
	}
	runs := levelRuns(S)
	hp := shortestRunCluster(runs)
	if cfg.Baud > 0 && colTimeS > 0 {
		hp = 1 / (2 * float64(cfg.Baud) * colTimeS)
	}
	if !(hp > 0) {
		return Result{Proto: "em4100", Error: "no bits"}
	}
	bits := manchesterStream(runs, hp)
	o := &rfidOut{n: S.n}
	good := false
	for i := 0; i+64 <= len(bits); i++ {
		// a header: nine 1s after a 0 (the last frame's stop bit) or an idle
		if i > 0 && bits[i-1].v == 1 {
			continue
		}
		hdr := true
		for k := 0; k < 9; k++ {
			hdr = hdr && bits[i+k].v == 1
		}
		if !hdr {
			continue
		}
		f := bits[i : i+64]
		whole := true
		for _, x := range f {
			whole = whole && x.v >= 0
		}
		if !whole {
			continue
		}
		o.add(f[0].x0, f[0].x0, "", "sof", 0, false)
		ok := true
		col := 0
		for by := 0; by < 5; by++ {
			r0, r1 := 9+10*by, 14+10*by
			v := bitsMSB(f, r0, 4)<<4 | bitsMSB(f, r1, 4)
			col ^= bitsMSB(f, r0, 4) ^ bitsMSB(f, r1, 4)
			txt, kind := hex2(v), "data"
			if !evenOK(f, r0, 4) || !evenOK(f, r1, 4) {
				txt, kind, ok = "!"+txt, "parity-error", false
			}
			o.add(f[r0].x0, f[r1+4].x1, txt, kind, v, true)
		}
		if bitsMSB(f, 59, 4) != col || f[63].v != 0 {
			o.add(f[59].x0, f[63].x1, "!COL", "frame-error", 0, false)
			ok = false
		}
		good = good || ok
		i += 63
	}
	if len(o.spans) == 0 {
		return Result{Proto: "em4100", Error: "no EM4100 frame"}
	}
	res := Result{OK: good, Proto: "em4100", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, SPB: 2 * hp}
	if colTimeS > 0 {
		res.Baud = int(math.Round(1 / (2 * hp * colTimeS)))
	}
	if !good {
		res.Error = "no EM4100 frame whose parities check"
	}
	return res
}
