// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"sort"
	"strings"
)

// The clocked two-line protocols (PS/2, SWD, MDIO) read the data line at
// the clock line's edges; Wiegand's two lines each carry one bit value.
// Mirrored step for step by decode_clocked.js.

// ClockedCfg configures PS/2, SWD, MDIO and Wiegand: a, the clock (D0 for
// Wiegand), and b, the data (D1).
// TRLC-LINKS: REQ-SDS-018
type ClockedCfg struct {
	Threshold, ThresholdB float64
	HaveThr, HaveThrB     bool
	Format                string
}

// clkEdge is one clock edge with the data line's level there.
type clkEdge struct {
	x   float64
	dir int
	d   int
}

// clockedLines slices both lines and lists the clock's edges with the data
// read at each; the clock's period (median between edges of one direction)
// in samples.
// TRLC-LINKS: REQ-SDS-018
func clockedLines(a, b []uint8, cfg ClockedCfg) ([]clkEdge, float64, sliced, string) {
	if b == nil {
		return nil, 0, sliced{}, "needs both lines (clock and data)"
	}
	C := sliceChannel(a, cfg.Threshold, cfg.HaveThr)
	if !C.ok {
		return nil, 0, C, C.reason
	}
	D := sliceChannel(b, cfg.ThresholdB, cfg.HaveThrB)
	if !D.ok {
		return nil, 0, C, D.reason
	}
	var out []clkEdge
	var gaps []float64
	lastFall := -1.0
	for _, e := range C.edges {
		out = append(out, clkEdge{e.x, e.dir, logicAt(D, e.x)})
		if e.dir < 0 {
			if lastFall >= 0 {
				gaps = append(gaps, e.x-lastFall)
			}
			lastFall = e.x
		}
	}
	if len(gaps) < 4 {
		return nil, 0, C, "too few clock edges"
	}
	sort.Float64s(gaps)
	return out, gaps[len(gaps)/2], C, ""
}

// DecodePS2 decodes PS/2 keyboard and mouse traffic: the data line read
// at each clock fall, eleven bits a frame (start 0, eight data bits least
// significant first, odd parity, stop 1). A device's frame ends there; a
// host's request carries a twelfth clock, the device's acknowledge (data
// held low, "ack" ACK), and its byte reads "H" before the hex. A frame
// opens on a start bit after the clock idled for 1.4 periods (or right
// after the last frame). Parity and stop errors flag the byte. OK needs a
// frame that checks.
// TRLC-LINKS: REQ-SDS-018
func DecodePS2(clk, data []uint8, colTimeS float64, cfg ClockedCfg) Result {
	E, per, C, why := clockedLines(clk, data, cfg)
	if why != "" {
		return Result{Proto: "ps2", Error: why}
	}
	var F []clkEdge
	for _, e := range E {
		if e.dir < 0 {
			F = append(F, e)
		}
	}
	n := C.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	explained := 0
	free := false // the last frame just ended: the next may follow at once
	for i := 0; i < len(F); i++ {
		quiet := (i == 0 && F[0].x >= 1.4*per) || (i > 0 && F[i].x-F[i-1].x >= 1.4*per)
		if F[i].d != 0 || !(quiet || free) || i+10 >= len(F) {
			free = false
			continue
		}
		// the frame's clocks come a period apart
		ok := true
		for k := 1; k <= 10; k++ {
			if F[i+k].x-F[i+k-1].x > 1.4*per {
				ok = false
			}
		}
		if !ok {
			free = false
			continue
		}
		v, ones := 0, 0
		for k := 0; k < 8; k++ {
			v |= F[i+1+k].d << k
			ones += F[i+1+k].d
		}
		par, stop := F[i+9].d, F[i+10].d
		host := i+11 < len(F) && F[i+11].x-F[i+10].x <= 1.4*per && F[i+11].d == 0
		spans = append(spans, pspan(F[i].x, F[i].x, n, "", "sof", 0))
		txt := FmtByte(v, cfg.Format)
		if host {
			txt = "H" + hex2(v)
		}
		kind := "data"
		switch {
		case (ones+par)%2 != 1:
			kind = "parity-error"
		case stop != 1:
			kind = "frame-error"
		}
		if kind != "data" {
			txt = "!" + txt
		} else {
			good = true
		}
		spans = append(spans, pspan(F[i+1].x, F[i+9].x, n, txt, kind, v))
		toks = append(toks, txt)
		units = append(units, v)
		explained += 11
		last := i + 10
		if host {
			explained++
			spans = append(spans, pspan(F[i+11].x, F[i+11].x+per, n, "ACK", "ack", 0))
			toks = append(toks, "ACK")
			last++
		}
		i = last
		free = true
	}
	if len(spans) == 0 {
		return Result{Proto: "ps2", Error: "no PS/2 frame"}
	}
	res := Result{OK: good, Proto: "ps2", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: C.threshold, SPB: per, Cover: float64(explained) / float64(len(F))}
	if colTimeS > 0 {
		res.Baud = int(1/(per*colTimeS) + 0.5)
	}
	if !good {
		res.Error = "no PS/2 frame whose parity checks"
	}
	return res
}

// swdCycle is one SWD clock cycle: the data line at its fall (the target's
// bits) and at its rise (the host's).
type swdCycle struct {
	xf, xr float64
	df, dr int
}

// DecodeSWD decodes ARM Serial Wire Debug: the host's request read at the
// clock's rises (start 1, APnDP, RnW, A[3:2], even parity, stop 0, park 1;
// a unit "RDP0", "WAP4"), a turnaround, the target's three-bit acknowledge
// read at the falls (a unit: OK, WAIT, FAULT), then 32 data bits least
// significant first and their parity, read at the falls for a read and at
// the rises (after another turnaround) for a write: the target drives a bit
// from a rise, the host reads it at the next fall. Fifty or more rises with
// SWDIO high are a line reset ("rw" LINERESET). OK needs a transfer
// acknowledged OK whose parities check.
// TRLC-LINKS: REQ-SDS-018
func DecodeSWD(clk, dio []uint8, colTimeS float64, cfg ClockedCfg) Result {
	E, per, C, why := clockedLines(clk, dio, cfg)
	if why != "" {
		return Result{Proto: "swd", Error: why}
	}
	// cycles: a fall and the rise after it
	var cy []swdCycle
	for k := 0; k+1 < len(E); k++ {
		if E[k].dir < 0 && E[k+1].dir > 0 {
			cy = append(cy, swdCycle{E[k].x, E[k+1].x, E[k].d, E[k+1].d})
		}
	}
	n := C.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	// line resets: fifty or more rises with SWDIO high, ended by a low one
	var resets [][2]int
	ones := 0
	for c := 0; c < len(cy); c++ {
		if cy[c].dr == 1 {
			ones++
			continue
		}
		if ones >= 50 {
			resets = append(resets, [2]int{c - ones, c})
		}
		ones = 0
	}
	ri := 0
	flushResets := func(upTo int) {
		for ; ri < len(resets) && resets[ri][1] <= upTo; ri++ {
			spans = append(spans, pspan(cy[resets[ri][0]].xr, cy[resets[ri][1]].xr, n, "LINERESET", "rw", 0))
			toks = append(toks, "LINERESET")
		}
	}
	// cycles explained: the transfers', the resets' and idle ones (low)
	explained := 0
	for _, rs := range resets {
		explained += rs[1] - rs[0]
	}
	for _, y := range cy {
		if y.dr == 0 && y.df == 0 {
			explained++
		}
	}
	next := -1 // where the transfer just read lets the next request start
	for c := 0; c+12 <= len(cy); c++ {
		flushResets(c)
		r := func(k int) int { return cy[c+k].dr }
		// a request: start 1, stop 0, park 1, after an idle (low) bit or
		// right after the last transfer
		if r(0) != 1 || r(6) != 0 || r(7) != 1 || (c > 0 && cy[c-1].dr != 0 && c != next) {
			continue
		}
		apdp, rnw, a2, a3, par := r(1), r(2), r(3), r(4), r(5)
		req := 1 | apdp<<1 | rnw<<2 | a2<<3 | a3<<4 | par<<5 | 1<<7
		parOK := (apdp+rnw+a2+a3)%2 == par
		ack := cy[c+9].df | cy[c+10].df<<1 | cy[c+11].df<<2
		if !parOK || (ack != 1 && ack != 2 && ack != 4) {
			continue // not a request a target answered
		}
		rw := map[int]string{0: "W", 1: "R"}[rnw]
		ap := map[int]string{0: "DP", 1: "AP"}[apdp]
		spans = append(spans, pspan(cy[c].xr, cy[c].xr, n, "", "sof", 0))
		txt := fmt.Sprintf("%s%s%X", rw, ap, (a2|a3<<1)<<2)
		spans = append(spans, pspan(cy[c].xf, cy[c+7].xr+per/2, n, txt, "data", req))
		toks = append(toks, txt)
		units = append(units, req)
		ackTxt := map[int]string{1: "OK", 2: "WAIT", 4: "FAULT"}[ack]
		spans = append(spans, pspan(cy[c+9].xf, cy[c+11].xr+per/2, n, ackTxt, "data", ack))
		toks = append(toks, ackTxt)
		units = append(units, ack)
		if ack != 1 {
			explained += swdUnexplained(cy[c : c+12])
			c += 11 // the turnaround after the acknowledge ends with the next rise
			next = c + 1
			continue
		}
		first := c + 12 // read: the falls of cycles c+12 .. c+44
		if rnw == 0 {
			first = c + 13 // write: after the turnaround, the rises
		}
		if first+33 > len(cy) {
			break
		}
		v, cnt := 0, 0
		for k := 0; k < 32; k++ {
			b := cy[first+k].df
			if rnw == 0 {
				b = cy[first+k].dr
			}
			v |= b << k
			cnt += b
		}
		p := cy[first+32].df
		if rnw == 0 {
			p = cy[first+32].dr
		}
		dtxt, kind := fmt.Sprintf("%08X", uint32(v)), "data"
		if cnt%2 != p {
			dtxt, kind = "!"+dtxt, "parity-error"
		} else {
			good = true
		}
		spans = append(spans, pspan(cy[first].xf, cy[first+31].xr+per/2, n, dtxt, kind, v))
		toks = append(toks, dtxt)
		units = append(units, v)
		explained += swdUnexplained(cy[c : first+33])
		c = first + 32 // a read's turnaround ends with its parity cycle's rise
		next = c + 1
	}
	flushResets(len(cy))
	if len(spans) == 0 {
		return Result{Proto: "swd", Error: "no SWD request"}
	}
	cover := min(1, float64(explained)/float64(max(1, len(cy))))
	// data an SPI bus clocks holds request-like bits now and then; SWD's
	// transfers, idle and resets explain most of its clock, which runs at
	// one rate while it runs
	good = good && cover >= 0.5 && swdSteady(cy, per)
	res := Result{OK: good, Proto: "swd", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: C.threshold, SPB: per, Cover: cover}
	if colTimeS > 0 {
		res.Baud = int(1/(per*colTimeS) + 0.5)
	}
	if !good {
		res.Error = "no SWD transfer acknowledged OK whose parity checks"
	}
	return res
}

// swdSteady reports a clock whose cycles while it runs (gaps under four
// periods) are mostly a period long.
// TRLC-LINKS: REQ-SDS-018
func swdSteady(cy []swdCycle, per float64) bool {
	run, even := 0, 0
	for k := 1; k < len(cy); k++ {
		g := cy[k].xf - cy[k-1].xf
		if g > 4*per {
			continue
		}
		run++
		if g >= 0.75*per && g <= 1.25*per {
			even++
		}
	}
	return run > 0 && float64(even) >= 0.8*float64(run)
}

// swdUnexplained counts a transfer's cycles not counted as idle already.
// TRLC-LINKS: REQ-SDS-018
func swdUnexplained(cy []swdCycle) int {
	n := 0
	for _, y := range cy {
		if y.dr != 0 || y.df != 0 {
			n++
		}
	}
	return n
}

// DecodeMDIO decodes MDIO management frames (IEEE 802.3 clause 22 and 45):
// the data line read at the clock's rises, a preamble of 16 or more ones,
// the start (01 clause 22, 00 clause 45), the opcode, the 5-bit PHY (port)
// and register (device) addresses, the turnaround and 16 data bits most
// significant first. Units: the header (start, opcode and addresses as 14
// bits; "RD 01.02", "WR45 01.03") and the data ("ABCD"); a turnaround whose
// second bit is not driven low (no PHY answered) flags the data. OK needs a
// frame whose turnaround is good.
// TRLC-LINKS: REQ-SDS-018
func DecodeMDIO(mdc, mdio []uint8, colTimeS float64, cfg ClockedCfg) Result {
	E, per, C, why := clockedLines(mdc, mdio, cfg)
	if why != "" {
		return Result{Proto: "mdio", Error: why}
	}
	var R []clkEdge
	for _, e := range E {
		if e.dir > 0 {
			R = append(R, e)
		}
	}
	n := C.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	ones, explained := 0, 0
	for i := 0; i < len(R); i++ {
		if R[i].d == 1 {
			ones++
			continue
		}
		pre := ones
		ones = 0
		if pre >= 16 {
			explained += pre // a preamble (or idle) before a frame
		}
		if pre < 16 || i+31 >= len(R) {
			continue
		}
		bit := func(k int) int { return R[i+k].d }
		bits := func(from, cnt int) int {
			v := 0
			for k := 0; k < cnt; k++ {
				v = v<<1 | bit(from+k)
			}
			return v
		}
		st := bits(0, 2)
		if st != 1 && st != 0 {
			continue
		}
		op, phy, reg := bits(2, 2), bits(4, 5), bits(9, 5)
		ta := bits(14, 2)
		data := bits(16, 16)
		var name string
		if st == 1 {
			name = map[int]string{1: "WR", 2: "RD"}[op]
		} else {
			name = map[int]string{0: "ADDR", 1: "WR45", 2: "RDI45", 3: "RD45"}[op]
		}
		if name == "" {
			continue
		}
		head := st<<12 | op<<10 | phy<<5 | reg
		spans = append(spans, pspan(R[i].x, R[i].x, n, "", "sof", 0))
		htxt := fmt.Sprintf("%s %02X.%02X", name, phy, reg)
		spans = append(spans, pspan(R[i].x, R[i+14].x, n, htxt, "data", head))
		toks = append(toks, htxt)
		units = append(units, head)
		dtxt, kind := fmt.Sprintf("%04X", data), "data"
		if ta&1 != 0 {
			dtxt, kind = "!"+dtxt, "frame-error"
		} else {
			good = true
		}
		spans = append(spans, pspan(R[i+16].x, R[i+31].x+per, n, dtxt, kind, data))
		toks = append(toks, dtxt)
		units = append(units, data)
		explained += 32
		i += 31
	}
	explained += ones // the idle after the last frame
	if len(spans) == 0 {
		return Result{Proto: "mdio", Error: "no MDIO frame"}
	}
	res := Result{OK: good, Proto: "mdio", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: C.threshold, SPB: per, Cover: min(1, float64(explained)/float64(max(1, len(R))))}
	if colTimeS > 0 {
		res.Baud = int(1/(per*colTimeS) + 0.5)
	}
	if !good {
		res.Error = "no MDIO frame a PHY answered"
	}
	return res
}

// DecodeWiegand decodes Wiegand card readers: a low pulse on D0 (a, the
// first line) is a 0 bit, on D1 (b) a 1; a frame ends where the lines rest
// longer than three bit intervals (and 3 ms). A 26-bit frame holds even
// parity, an 8-bit facility code, a 16-bit card number and odd parity, a
// 34-bit one a 16-bit facility code; each field is a unit ("F12", "C4660",
// decimal as readers print them; flagged when its parity fails). Other
// lengths are their bits in bytes, most significant first. OK needs a 26-
// or 34-bit frame whose parities check.
// TRLC-LINKS: REQ-SDS-018
func DecodeWiegand(d0, d1 []uint8, colTimeS float64, cfg ClockedCfg) Result {
	if d1 == nil {
		return Result{Proto: "wiegand", Error: "needs both lines (D0 and D1)"}
	}
	A := sliceChannel(d0, cfg.Threshold, cfg.HaveThr)
	B := sliceChannel(d1, cfg.ThresholdB, cfg.HaveThrB)
	if !A.ok && !B.ok {
		return Result{Proto: "wiegand", Error: A.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "wiegand", Error: "no sample time"}
	}
	type pulse struct {
		x0, x1 float64
		v      int
	}
	var ps []pulse
	for v, S := range []sliced{A, B} {
		if !S.ok {
			continue
		}
		for _, r := range levelRuns(S) {
			if r.lvl == 0 && !r.open {
				if w := r.us(colTimeS); w >= 10 && w <= 500 {
					ps = append(ps, pulse{r.x0, r.x1, v})
				}
			}
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].x0 < ps[j].x0 || ps[i].x0 == ps[j].x0 && ps[i].v < ps[j].v })
	if len(ps) < 4 {
		return Result{Proto: "wiegand", Error: "too few Wiegand pulses"}
	}
	var gaps []float64
	for k := 1; k < len(ps); k++ {
		gaps = append(gaps, ps[k].x0-ps[k-1].x0)
	}
	sort.Float64s(gaps)
	split := max(3*gaps[len(gaps)/2], 3e-3/colTimeS)
	n := max(A.n, B.n)
	var spans []Span
	var toks []string
	var units []int
	good := false
	var interval float64
	emit := func(fr []pulse) {
		nb := len(fr)
		spans = append(spans, pspan(fr[0].x0, fr[0].x0, n, "", "sof", 0))
		lbl := fmt.Sprintf("%db", nb)
		spans = append(spans, pspan(fr[0].x0, fr[nb-1].x1, n, lbl, "rw", nb))
		toks = append(toks, lbl)
		bits := func(from, cnt int) (v, ones int) {
			for k := 0; k < cnt; k++ {
				v = v<<1 | fr[from+k].v
				ones += fr[from+k].v
			}
			return v, ones
		}
		field := func(from, cnt int, pfx string, bad bool) {
			v, _ := bits(from, cnt)
			txt, kind := fmt.Sprintf("%s%d", pfx, v), "data"
			if bad {
				txt, kind = "!"+txt, "parity-error"
			}
			spans = append(spans, pspan(fr[from].x0, fr[from+cnt-1].x1, n, txt, kind, v))
			toks = append(toks, txt)
			units = append(units, v)
		}
		if nb == 26 || nb == 34 {
			h := nb / 2
			_, e := bits(0, h)
			_, o := bits(h, h)
			fc := 8
			if nb == 34 {
				fc = 16
			}
			field(1, fc, "F", e%2 != 0)
			field(1+fc, 16, "C", o%2 != 1)
			good = good || (e%2 == 0 && o%2 == 1)
			return
		}
		for k := 0; k < nb; k += 8 {
			cnt := min(8, nb-k)
			v, _ := bits(k, cnt)
			txt := hex2(v)
			spans = append(spans, pspan(fr[k].x0, fr[k+cnt-1].x1, n, txt, "data", v))
			toks = append(toks, txt)
			units = append(units, v)
		}
	}
	start := 0
	for k := 1; k <= len(ps); k++ {
		if k == len(ps) || ps[k].x0-ps[k-1].x0 > split {
			if k-start >= 4 {
				emit(ps[start:k])
				if interval == 0 {
					interval = (ps[k-1].x0 - ps[start].x0) / float64(k-1-start)
				}
			}
			start = k
		}
	}
	if len(spans) == 0 {
		return Result{Proto: "wiegand", Error: "no Wiegand frame"}
	}
	thr := A.threshold
	if !A.ok {
		thr = B.threshold
	}
	res := Result{OK: good, Proto: "wiegand", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: thr, SPB: interval}
	if interval > 0 {
		res.Baud = int(1/(interval*colTimeS) + 0.5)
	}
	if !good {
		res.Error = "no 26- or 34-bit Wiegand frame whose parities check"
	}
	return res
}
