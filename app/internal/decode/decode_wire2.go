// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Two-wire buses of the sigrok set beyond the first thirty: an SD card's
// CMD line in SD mode, Atmel's PDI, IEEE 1149.7 compact JTAG, SEGA's Maple
// bus and digital calipers. Mirrored step for step by decode_wire2.js.

// risingBits is a data line read at each rising edge of a clock (line a);
// the clock's median rising-to-rising period.
type cbit struct {
	v int
	x float64
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func risingBits(a, b lineIn, cfg ClockedCfg) ([]cbit, float64, sliced, string) {
	E, _, C, why := clockedLines(a, b, cfg)
	if why != "" {
		return nil, 0, C, why
	}
	out := make([]cbit, 0, len(E)/2+1)
	per := make([]float64, 0, len(E)/2+1)
	for _, e := range E {
		if e.dir > 0 {
			if k := len(out); k > 0 {
				per = append(per, e.x-out[k-1].x)
			}
			out = append(out, cbit{e.d, e.x})
		}
	}
	return out, medianOf(per), C, ""
}

// crc7 is the SD card CRC-7 (x^7 + x^3 + 1) of bits, most significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func crc7(bits []int) int {
	c := 0
	for _, b := range bits {
		fb := (c>>6 ^ b) & 1
		c = c << 1 & 0x7f
		if fb != 0 {
			c ^= 0x09
		}
	}
	return c
}

// DecodeSDCard decodes an SD card's CMD line in SD mode (sigrok's
// sdcard_sd): read at the clock's (line a) rising edges, a token opens with
// a 0 ("sof") and a transmission bit, 1 from the host: a command (48 bits:
// the index, a 32-bit argument, CRC-7, end 1), or from the card a response,
// 136 bits (R2: the CID or CSD register and its CRC-7) after CMD2, CMD9 and
// CMD10 and else 48. A token's bytes are units, its first named ("CMD17",
// "ACMD41" after CMD55; the response "R1", "R2", "R3" after ACMD41, "R6"
// after CMD3 and CMD7, "R7" after CMD8); a CRC-7 or end bit that fails
// flags its last byte (R3 carries no CRC). Baud is the clock rate. OK needs
// a token whose CRC checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeSDCard(a, b []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeSDCard(samplesIn(a), samplesIn(b), colTimeS, cfg)
}

// DecodeSDCardEdges is DecodeSDCard over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeSDCardEdges(a, b *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeSDCard(edgesIn(a), edgesIn(b), colTimeS, cfg)
}

// decodeSDCard is DecodeSDCard over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func decodeSDCard(a, b lineIn, colTimeS float64, cfg ClockedCfg) Result {
	B, per, C, why := risingBits(a, b, cfg)
	if why != "" {
		return Result{Proto: "sdcard", Error: why}
	}
	o := &wireOut{n: C.n}
	good := false
	lastCmd, acmd, nextACMD := -1, false, false
	for i := 0; i < len(B); i++ {
		if B[i].v != 0 || i+1 >= len(B) {
			continue
		}
		host := B[i+1].v == 1
		n := 48
		if !host && !acmd && (lastCmd == 2 || lastCmd == 9 || lastCmd == 10) {
			n = 136
		}
		if i+n > len(B) {
			break
		}
		t := B[i : i+n]
		bits := make([]int, n)
		for k, x := range t {
			bits[k] = x.v
		}
		o.add(t[0].x, t[0].x, "", "sof", 0, false)
		idx := 0
		for k := 2; k < 8; k++ {
			idx = idx<<1 | bits[k]
		}
		name := ""
		ckOK := true
		switch {
		case host:
			acmd, nextACMD = nextACMD, idx == 55 && !nextACMD
			if acmd {
				name = "ACMD" + strconv.Itoa(idx)
			} else {
				name = "CMD" + strconv.Itoa(idx)
			}
			lastCmd = idx
			ckOK = crc7(bits[:40]) == bitsInt(bits[40:47])
		case n == 136:
			name = "R2"
			ckOK = crc7(bits[8:128]) == bitsInt(bits[128:135])
		case acmd && lastCmd == 41:
			name = "R3" // the OCR: no CRC
		default:
			name = "R1"
			switch {
			case !acmd && (lastCmd == 3 || lastCmd == 7):
				name = "R6"
			case !acmd && lastCmd == 8:
				name = "R7"
			}
			ckOK = crc7(bits[:40]) == bitsInt(bits[40:47])
		}
		ckOK = ckOK && bits[n-1] == 1
		for k := 0; k < n/8; k++ {
			v := bitsInt(bits[8*k : 8*k+8])
			txt, kind := hex2(v), "data"
			if k == 0 {
				txt = name
			}
			if k == n/8-1 && !ckOK {
				txt, kind = "!"+txt, "frame-error"
			}
			x1 := t[8*k].x + 8*per
			o.add(t[8*k].x, x1, txt, kind, v, true)
		}
		good = good || ckOK
		i += n - 1
	}
	if len(o.spans) == 0 {
		return Result{Proto: "sdcard", Error: "no SD token"}
	}
	res := Result{OK: good, Proto: "sdcard", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: C.threshold, SPB: per}
	if colTimeS > 0 && per > 0 {
		res.Baud = int(math.Round(1 / (per * colTimeS)))
	}
	if !good {
		res.Error = "no SD token whose CRC checks"
	}
	return res
}

// bitsInt is bits, most significant first, as a number.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func bitsInt(b []int) int {
	v := 0
	for _, x := range b {
		v = v<<1 | x
	}
	return v
}

// pdiOps name PDI's instructions by opcode.
var pdiOps = []string{"LDS", "LD", "STS", "ST", "LDCS", "REPEAT", "STCS", "KEY"}

// DecodePDI decodes Atmel's PDI (sigrok's avr_pdi): the data line read at
// the clock's (line a) rising edges as UART characters, a start 0, eight
// bits least significant first, even parity and a stop 1, after idle 1s;
// eleven 0s or more are a BREAK. An instruction's opcode ("sof", named:
// LDS, LD, STS, ST, LDCS, REPEAT, STCS, KEY) gives the operand bytes that
// follow it (addresses and data of 1 to 4 bytes, the KEY's 8, a REPEAT's
// count for the next LD or ST); every byte is a unit. A character whose
// parity or stop fails is flagged and ends the instruction. Baud is the
// clock rate. OK needs an instruction whose characters check.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodePDI(a, b []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodePDI(samplesIn(a), samplesIn(b), colTimeS, cfg)
}

// DecodePDIEdges is DecodePDI over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodePDIEdges(a, b *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodePDI(edgesIn(a), edgesIn(b), colTimeS, cfg)
}

// decodePDI is DecodePDI over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func decodePDI(a, b lineIn, colTimeS float64, cfg ClockedCfg) Result {
	B, per, C, why := risingBits(a, b, cfg)
	if why != "" {
		return Result{Proto: "pdi", Error: why}
	}
	o := &wireOut{n: C.n}
	good := false
	var groups []int // the operand groups (bytes each) still to come
	var cur []int    // the bytes of the group being read
	rep, isRep := 0, false
	for i := 0; i < len(B); i++ {
		if B[i].v == 1 {
			continue
		}
		// a BREAK: eleven 0s or more
		run := 0
		for i+run < len(B) && B[i+run].v == 0 {
			run++
		}
		if run >= 11 {
			o.add(B[i].x, B[i+run-1].x+per, "BRK", "rw", 0, false)
			i += run - 1
			groups, cur, rep = nil, nil, 0
			continue
		}
		if i+11 > len(B) {
			break
		}
		c := B[i : i+11]
		v, p := 0, 0
		for k := 0; k < 8; k++ {
			v |= c[1+k].v << k
			p ^= c[1+k].v
		}
		i += 10
		x0, x1 := c[0].x, c[10].x+per
		if p != c[9].v || c[10].v != 1 {
			o.add(x0, x1, "!"+hex2(v), "parity-error", v, true)
			groups, cur = nil, nil
			continue
		}
		if len(groups) == 0 {
			// an opcode: the operand groups it takes
			op := v >> 5
			na, nd := (v>>2&3)+1, (v&3)+1
			switch op {
			case 0, 2: // LDS, STS: an address, then the data
				groups = []int{na, nd}
			case 1, 3: // LD, ST: the data, again for each repeat
				groups = []int{nd}
				for ; rep > 0; rep-- {
					groups = append(groups, nd)
				}
			case 4, 6: // LDCS, STCS
				groups = []int{1}
			case 5: // REPEAT: its count
				groups = []int{nd}
			case 7: // KEY
				groups = []int{8}
			}
			isRep = op == 5
			o.add(x0, x0, "", "sof", 0, false)
			o.add(x0, x1, pdiOps[op], "data", v, true)
			good = true
			continue
		}
		o.add(x0, x1, hex2(v), "data", v, true)
		cur = append(cur, v)
		if len(cur) == groups[0] {
			if isRep {
				// the count, least significant byte first (at most 4096:
				// a record holds no more)
				rep = 0
				for k := len(cur) - 1; k >= 0; k-- {
					rep = min(rep*256+cur[k], 4096)
				}
			}
			groups, cur = groups[1:], nil
		}
	}
	if len(o.spans) == 0 {
		return Result{Proto: "pdi", Error: "no PDI character"}
	}
	res := Result{OK: good, Proto: "pdi", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: C.threshold, SPB: per}
	if colTimeS > 0 && per > 0 {
		res.Baud = int(math.Round(1 / (per * colTimeS)))
	}
	if !good {
		res.Error = "no PDI instruction"
	}
	return res
}

// jtagNext is the TAP controller's state after state s with TMS tms (the
// states numbered as jtagNames).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func jtagNext(s, tms int) int {
	// 0 reset, 1 idle, 2 sel-DR, 3 cap-DR, 4 shift-DR, 5 exit1-DR, 6 pause-DR,
	// 7 exit2-DR, 8 update-DR, 9 sel-IR, 10 cap-IR, 11 shift-IR, 12 exit1-IR,
	// 13 pause-IR, 14 exit2-IR, 15 update-IR
	next := [16][2]int{{1, 0}, {1, 2}, {3, 9}, {4, 5}, {4, 5}, {6, 8}, {6, 7}, {4, 8}, {1, 2},
		{10, 0}, {11, 12}, {11, 12}, {13, 15}, {13, 14}, {11, 15}, {1, 2}}
	return next[s][tms&1]
}

// DecodeCJTAG decodes IEEE 1149.7 compact JTAG on TCKC (line a) and TMSC
// (sigrok's cjtag): TMSC's edges while TCKC is high are an escape, six of
// them ("sof" ESC6) the selection that, after its online activation code
// (12 bits, or 36), enters OScan1, eight or more ("sof" ESC8) back to four-wire mode. In
// OScan1 three TCKC rises make a TCK: TMSC carries nTDI, TMS and TDO; the
// TAP controller follows TMS, and each scan's bits shifted in SHIFT-IR or
// SHIFT-DR, first bit least significant, are a unit at UPDATE ("IR" or
// "DR", the TDI value), its TDO beside it. Baud is TCKC's rate, Cover the
// share of its periods within a fifth of their median (a clock's). OK needs
// a scan, or an escape and its whole activation code (a short record of a
// slow probe holds no scan).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeCJTAG(a, b []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeCJTAG(samplesIn(a), samplesIn(b), colTimeS, cfg)
}

// DecodeCJTAGEdges is DecodeCJTAG over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeCJTAGEdges(a, b *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeCJTAG(edgesIn(a), edgesIn(b), colTimeS, cfg)
}

// decodeCJTAG is DecodeCJTAG over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func decodeCJTAG(a, b lineIn, colTimeS float64, cfg ClockedCfg) Result {
	if b.none() {
		return Result{Proto: "cjtag", Error: "needs both lines (TCKC and TMSC)"}
	}
	K := a.slice(cfg.Threshold, cfg.HaveThr)
	if !K.ok {
		return Result{Proto: "cjtag", Error: K.reason}
	}
	M := b.slice(cfg.ThresholdB, cfg.HaveThrB)
	if !M.ok {
		return Result{Proto: "cjtag", Error: M.reason}
	}
	o := &wireOut{n: K.n}
	var rises []float64
	for _, e := range K.edges {
		if e.dir > 0 {
			rises = append(rises, e.x)
		}
	}
	// TMSC's edges while TCKC is high, counted per high phase: the phase
	// after rise i ends at the fall after it
	esc := make([]int, len(rises)+1)
	ke, mi := 0, 0
	for i, r := range rises {
		for ke < len(K.edges) && K.edges[ke].x <= r {
			ke++
		}
		fall := float64(K.n)
		if ke < len(K.edges) {
			fall = K.edges[ke].x
		}
		for mi < len(M.edges) && M.edges[mi].x <= r {
			mi++
		}
		for mi < len(M.edges) && M.edges[mi].x <= fall {
			esc[i+1]++
			mi++
		}
	}
	const fourWire, oac, oscan = 0, 1, 2
	mode := fourWire
	oacp, oaclen, cyc := 0, 12, 0
	state := 1 // run-test/idle
	tdi := 0
	var ti, to, nb int
	var x0 float64
	good := false
	var per []float64
	for i, r := range rises {
		if i > 0 {
			per = append(per, r-rises[i-1])
		}
		switch e := esc[i]; {
		case e >= 8:
			mode = fourWire
			o.add(r, r, "", "sof", 0, false)
			o.add(r, r, "ESC"+strconv.Itoa(e), "rw", e, false)
		case e == 6:
			mode, oacp, oaclen = oac, 0, 12
			o.add(r, r, "", "sof", 0, false)
			o.add(r, r, "ESC6", "rw", 6, false)
		}
		tmsc := logicAt(&M, r)
		if mode == oac {
			// the online activation code: twelve bits (36 when the eighth
			// is 0: the extended form), then OScan1
			if oacp++; oacp == 8 && tmsc == 0 {
				oaclen = 36
			}
			if oacp > oaclen {
				mode, cyc, state = oscan, 1, 0
				good = true
			}
			continue
		}
		if mode != oscan {
			continue
		}
		switch cyc {
		case 0:
			tdi = 1 - tmsc
			cyc = 1
			continue
		case 1:
			cyc = 2
			continue
		}
		cyc = 0
		tms := logicAt(&M, rises[i-1])
		tdo := tmsc
		if state == 4 || state == 11 {
			if nb == 0 {
				x0 = rises[i-2]
			}
			if nb < 31 {
				ti |= tdi << nb
				to |= tdo << nb
			}
			nb++
		}
		state = jtagNext(state, tms)
		if state == 8 || state == 15 {
			reg := "DR"
			if state == 15 {
				reg = "IR"
			}
			txt := fmt.Sprintf("%s%X", reg, ti)
			o.add(x0, r, txt, "data", ti, true)
			o.add(x0, r, fmt.Sprintf("TDO%X", to), "rw", to, false)
			good = good || nb > 0
			ti, to, nb = 0, 0, 0
		}
	}
	if len(o.spans) == 0 {
		return Result{Proto: "cjtag", Error: "no cJTAG escape"}
	}
	T := medianOf(per)
	regular := 0 // TCKC's periods within a fifth of their median
	for _, p := range per {
		if p >= 0.8*T && p <= 1.2*T {
			regular++
		}
	}
	res := Result{OK: good, Proto: "cjtag", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: K.threshold, SPB: T}
	if len(per) > 0 {
		res.Cover = float64(regular) / float64(len(per))
	}
	if colTimeS > 0 && T > 0 {
		res.Baud = int(math.Round(1 / (T * colTimeS)))
	}
	if !good {
		res.Error = "no cJTAG scan"
	}
	return res
}

// mapleNames prefix a Maple frame's header bytes.
var mapleNames = []string{"L", "S", "D", "C"}

// DecodeMaple decodes SEGA's Maple bus (sigrok's maple_bus) on SDCKA (line
// a) and SDCKB: a start pattern (SDCKA low while SDCKB pulses four times,
// "sof") opens a frame whose bits alternate lines: at SDCKA's falls SDCKB
// is the bit, at SDCKB's falls SDCKA, most significant first. Its bytes are
// units: the length ("L", 4-byte words after the header), the source and
// destination ("S", "D"), the command ("C"), the data and the checksum,
// the exclusive-or of all before it (flagged when it fails, as is a frame
// whose length disagrees). The end pattern closes it. OK needs a frame
// whose checksum checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeMaple(a, b []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeMaple(samplesIn(a), samplesIn(b), colTimeS, cfg)
}

// DecodeMapleEdges is DecodeMaple over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeMapleEdges(a, b *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeMaple(edgesIn(a), edgesIn(b), colTimeS, cfg)
}

// decodeMaple is DecodeMaple over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func decodeMaple(a, b lineIn, colTimeS float64, cfg ClockedCfg) Result {
	if b.none() {
		return Result{Proto: "maple", Error: "needs both lines (SDCKA and SDCKB)"}
	}
	A := a.slice(cfg.Threshold, cfg.HaveThr)
	if !A.ok {
		return Result{Proto: "maple", Error: A.reason}
	}
	Bs := b.slice(cfg.ThresholdB, cfg.HaveThrB)
	if !Bs.ok {
		return Result{Proto: "maple", Error: Bs.reason}
	}
	// both lines' edges in time order, with both lines' levels after each
	type ev struct {
		x      float64
		line   int // 0 SDCKA, 1 SDCKB
		fall   bool
		la, lb int
	}
	evs := make([]ev, 0, len(A.edges)+len(Bs.edges))
	la, lb := int(A.first), int(Bs.first)
	ia, ib := 0, 0
	for ia < len(A.edges) || ib < len(Bs.edges) {
		if ib >= len(Bs.edges) || (ia < len(A.edges) && A.edges[ia].x <= Bs.edges[ib].x) {
			e := A.edges[ia]
			ia++
			la = (e.dir + 1) / 2
			evs = append(evs, ev{e.x, 0, e.dir < 0, la, lb})
		} else {
			e := Bs.edges[ib]
			ib++
			lb = (e.dir + 1) / 2
			evs = append(evs, ev{e.x, 1, e.dir < 0, la, lb})
		}
	}
	o := &wireOut{n: A.n}
	good := false
	var bitT runningMedian
	for k := 0; k < len(evs); k++ {
		// a start: SDCKA falls with SDCKB high, then SDCKB falls four
		// times (six: with a CRC) before SDCKA rises
		if !(evs[k].line == 0 && evs[k].fall && evs[k].lb == 1) {
			continue
		}
		cnt, j := 0, k+1
		for ; j < len(evs) && !(evs[j].line == 0 && !evs[j].fall); j++ {
			if evs[j].line == 1 && evs[j].fall {
				cnt++
			}
		}
		if j >= len(evs) || evs[j].lb != 1 || (cnt != 4 && cnt != 6) {
			continue
		}
		o.add(evs[k].x, evs[j].x, "", "sof", 0, false)
		var bytes []int
		var bx []float64
		ended := false
		var endX0, endX1 float64
		// the bytes: at SDCKA's falls SDCKB is the bit, at SDCKB's SDCKA
		for byteDone := true; byteDone; {
			byteDone = false
			ca, cb, v := 0, 0, 0
			initial := true
			x0 := 0.0
			for j++; j < len(evs) && cb < 4; j++ {
				e := evs[j]
				if !e.fall {
					continue
				}
				if e.line == 0 {
					if ca == cb {
						if ca == 0 {
							x0 = e.x
						}
						v = v<<1 | e.lb
						ca++
						continue
					}
					if ca == 1 && cb == 0 && v == 0 && e.lb == 0 {
						// the end pattern: both lines high before another fall
						for q := j + 1; q < len(evs); q++ {
							if evs[q].fall {
								break
							}
							if evs[q].la == 1 && evs[q].lb == 1 {
								ended, endX0, endX1 = true, e.x, evs[q].x
								j = q
								break
							}
						}
					}
					break
				}
				if ca == cb+1 {
					v = v<<1 | e.la
					cb++
					continue
				}
				if ca == 0 && cb == 0 && e.la == 1 && initial {
					initial = false
					continue
				}
				break
			}
			if cb == 4 {
				bytes, bx = append(bytes, v), append(bx, x0)
				bitT.add((evs[j-1].x - x0) / 7)
				byteDone = true
				j--
			}
		}
		want := 0
		if len(bytes) > 0 {
			want = 4*(bytes[0]+1) + 1
		}
		ck := 0
		T := bitT.value()
		for i, x := range bytes {
			txt, kind := hex2(x), "data"
			switch {
			case i < 4:
				txt = mapleNames[i] + txt
			case i == len(bytes)-1:
				txt = "K" + txt
				if x != ck || len(bytes) != want || !ended {
					txt, kind = "!"+txt, "frame-error"
				} else {
					good = true
				}
			}
			ck ^= x
			o.add(bx[i], bx[i]+8*T, txt, kind, x, true)
		}
		if ended {
			o.add(endX0, endX1, "END", "stop", 0, false)
		}
		k = j
	}
	if len(o.spans) == 0 {
		return Result{Proto: "maple", Error: "no Maple frame"}
	}
	T := bitT.value()
	res := Result{OK: good, Proto: "maple", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: A.threshold, SPB: T}
	if colTimeS > 0 && T > 0 {
		res.Baud = int(math.Round(1 / (T * colTimeS)))
	}
	if !good {
		res.Error = "no Maple frame whose checksum checks"
	}
	return res
}

// DecodeCaliper decodes a digital caliper's clock (line a) and data lines
// (sigrok's caliper): 24 bits read at the clock's rising edges, least
// significant first, a frame after 10 ms of quiet ("sof"): a 16-bit count
// (two units, the low byte first) and the flags (bit 4 negative, bit 7
// inches; a unit). The reading is hundredths of a millimetre or two
// thousandths of an inch ("rw", e.g. "-12.34mm"). OK needs a frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeCaliper(a, b []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeCaliper(samplesIn(a), samplesIn(b), colTimeS, cfg)
}

// DecodeCaliperEdges is DecodeCaliper over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func DecodeCaliperEdges(a, b *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeCaliper(edgesIn(a), edgesIn(b), colTimeS, cfg)
}

// decodeCaliper is DecodeCaliper over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func decodeCaliper(a, b lineIn, colTimeS float64, cfg ClockedCfg) Result {
	B, per, C, why := risingBits(a, b, cfg)
	if why != "" {
		return Result{Proto: "caliper", Error: why}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "caliper", Error: "no sample time"}
	}
	quiet := 10e-3 / colTimeS
	o := &wireOut{n: C.n}
	good := false
	for i := 0; i+24 <= len(B); {
		if i > 0 && B[i].x-B[i-1].x < quiet {
			i++
			continue
		}
		f := B[i : i+24]
		whole := true
		for k := 1; k < 24; k++ {
			whole = whole && f[k].x-f[k-1].x < quiet
		}
		if !whole {
			i++
			continue
		}
		o.add(f[0].x, f[0].x, "", "sof", 0, false)
		num, flags := 0, 0
		for k := 0; k < 16; k++ {
			num |= f[k].v << k
		}
		for k := 0; k < 8; k++ {
			flags |= f[16+k].v << k
		}
		o.add(f[0].x, f[7].x+per, hex2(num&0xff), "data", num&0xff, true)
		o.add(f[8].x, f[15].x+per, hex2(num>>8), "data", num>>8, true)
		o.add(f[16].x, f[23].x+per, hex2(flags), "data", flags, true)
		v := float64(num)
		if flags>>4&1 == 1 && num != 0 {
			v = -v
		}
		txt := ""
		if flags>>7&1 == 1 {
			txt = strconv.FormatFloat(v/2000, 'f', -1, 64) + "in"
		} else {
			txt = strconv.FormatFloat(v/100, 'f', -1, 64) + "mm"
		}
		o.add(f[0].x, f[23].x+per, txt, "rw", num, false)
		good = true
		i += 24
	}
	if len(o.spans) == 0 {
		return Result{Proto: "caliper", Error: "no caliper frame"}
	}
	res := Result{OK: good, Proto: "caliper", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: C.threshold, SPB: per}
	if per > 0 {
		res.Baud = int(math.Round(1 / (per * colTimeS)))
	}
	return res
}
