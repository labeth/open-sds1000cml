// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The clocked two-line protocols (PS/2, SWD, MDIO) read the data line at
// the clock line's edges; Wiegand's two lines each carry one bit value.
// Mirrored step for step by decode_clocked.js.

// ClockedCfg configures PS/2, SWD, MDIO and Wiegand: a, the clock (D0 for
// Wiegand), and b, the data (D1).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
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
	i   int // the edge's sample (edge.i)
}

// clockedLines slices both lines and lists the clock's edges with the data
// read at each; the clock's period (median between edges of one direction)
// in samples.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func clockedLines(a, b lineIn, cfg ClockedCfg) ([]clkEdge, float64, sliced, string) {
	E, per, C, _, why := clockedLinesD(a, b, cfg)
	return E, per, C, why
}

// clockedLinesD is clockedLines with the data line sliced as well.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func clockedLinesD(a, b lineIn, cfg ClockedCfg) ([]clkEdge, float64, sliced, sliced, string) {
	if b.none() {
		return nil, 0, sliced{}, sliced{}, "needs both lines (clock and data)"
	}
	C := a.slice(cfg.Threshold, cfg.HaveThr)
	if !C.ok {
		return nil, 0, C, sliced{}, C.reason
	}
	D := b.slice(cfg.ThresholdB, cfg.HaveThrB)
	if !D.ok {
		return nil, 0, C, D, D.reason
	}
	sc := a.scratch()
	out := scratchSlice[clkEdge](sc, len(C.edges))
	gaps := scratchSlice[float64](sc, len(C.edges)/2+1)
	lastFall := -1.0
	lv := levelCursor{s: &D}
	if fallUp, riseUp, ok := C.roundUps(); ok {
		// A clock sliced from its edges: each edge's sample is its own (no
		// rounding of its crossing), and the gaps between falls lie within
		// a hair of their samples (nthNear).
		gi := scratchSlice[int](sc, len(C.edges)/2+1)
		last := -1
		out = out[:len(C.edges)]
		// The data line's level at each edge's sample, its edges walked
		// along (the samples only grow: a rise's is its own, a fall's the
		// one before).
		dE, dn, dk := D.edges, D.n, 0
		walk := D.codes == nil
		for k := range C.edges {
			e := &C.edges[k]
			up := riseUp
			if e.dir < 0 {
				up = fallUp
			}
			at, d := e.i-1+up, -1
			switch {
			case !walk:
				d = lv.atSample(at)
			case at >= 0 && at < dn:
				for dk < len(dE) && dE[dk].i <= at {
					dk++
				}
				d = int(D.first) ^ dk&1
			}
			o := &out[k] // set field by field: no struct copied in
			o.x, o.dir, o.d, o.i = e.x, e.dir, d, e.i
			if e.dir < 0 {
				if last >= 0 {
					gaps = append(gaps, e.x-lastFall)
					gi = append(gi, e.i-last)
				}
				lastFall, last = e.x, e.i
			}
		}
		if len(gaps) < 4 {
			return nil, 0, C, D, "too few clock edges"
		}
		return out, nthNear(gaps, gi, len(gaps)/2, nearBufOf(sc)), C, D, ""
	}
	for _, e := range C.edges {
		out = append(out, clkEdge{e.x, e.dir, lv.at(e.x), e.i})
		if e.dir < 0 {
			if lastFall >= 0 {
				gaps = append(gaps, e.x-lastFall)
			}
			lastFall = e.x
		}
	}
	if len(gaps) < 4 {
		return nil, 0, C, D, "too few clock edges"
	}
	return out, nthValue(gaps, len(gaps)/2), C, D, ""
}

// DecodePS2 decodes PS/2 keyboard and mouse traffic: the data line read
// at each clock fall, eleven bits a frame (start 0, eight data bits least
// significant first, odd parity, stop 1). A device's frame ends there; a
// host's request carries a twelfth clock, the device's acknowledge (data
// held low, "ack" ACK), and its byte reads "H" before the hex. A frame
// opens on a start bit after the clock idled for 1.4 periods (or right
// after the last frame). Parity and stop errors flag the byte. OK needs a
// frame that checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodePS2(clk, data []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodePS2(samplesIn(clk), samplesIn(data), colTimeS, cfg)
}

// DecodePS2Edges is DecodePS2 over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodePS2Edges(clk, data *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodePS2(edgesIn(clk), edgesIn(data), colTimeS, cfg)
}

// decodePS2 is DecodePS2 over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func decodePS2(clk, data lineIn, colTimeS float64, cfg ClockedCfg) Result {
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
	firstF, lastF := -1, -1 // the first frame's first fall, the last frame's last
	free := false           // the last frame just ended: the next may follow at once
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
		if firstF < 0 {
			firstF = i
		}
		lastF = last
		i = last
		free = true
	}
	if len(spans) == 0 {
		return Result{Proto: "ps2", Error: "no PS/2 frame"}
	}
	// The record's ends cut a frame as often as not: the falls before the
	// first frame and after the last, up to a frame's each, are its rest,
	// not clocks PS/2 leaves unexplained (a keyboard's two frames, the first
	// cut by the record's start, fell to half and the pair read as SPI).
	cut := min(firstF, 12) + min(len(F)-1-lastF, 12)
	res := Result{OK: good, Proto: "ps2", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: C.threshold, SPB: per, Cover: min(1, float64(explained)/float64(max(1, len(F)-cut)))}
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
	fi, ri int // the fall's sample and the rise's
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
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodeSWD(clk, dio []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeSWD(samplesIn(clk), samplesIn(dio), colTimeS, cfg)
}

// DecodeSWDEdges is DecodeSWD over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodeSWDEdges(clk, dio *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeSWD(edgesIn(clk), edgesIn(dio), colTimeS, cfg)
}

// decodeSWD is DecodeSWD over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func decodeSWD(clk, dio lineIn, colTimeS float64, cfg ClockedCfg) Result {
	E, per, C, D, why := clockedLinesD(clk, dio, cfg)
	if why != "" {
		return Result{Proto: "swd", Error: why}
	}
	// cycles: a fall and the rise after it
	cy := scratchSlice[swdCycle](clk.scratch(), len(E)/2+1)
	for k := 0; k+1 < len(E); k++ {
		if f, r := &E[k], &E[k+1]; f.dir < 0 && r.dir > 0 {
			cy = cy[:len(cy)+1]
			y := &cy[len(cy)-1] // set field by field: no struct copied in
			y.xf, y.xr, y.df, y.dr, y.fi, y.ri = f.x, r.x, f.d, r.d, f.i, r.i
		}
	}
	// The target's bits are read at the falls (it drives them from a rise);
	// a target that moves them at the fall, as the host does its own, reads
	// at the rises instead: the edge whose reading acknowledges more
	// requests OK.
	atRise := swdTargetAtRise(cy)
	tb := func(k int) int {
		if atRise {
			return cy[k].dr
		}
		return cy[k].df
	}
	o := newWireOut(C.n, C.sc)
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
			o.add(cy[resets[ri][0]].xr, cy[resets[ri][1]].xr, "LINERESET", "rw", 0, false)
		}
	}
	// cycles explained: the transfers', the resets' and idle ones (low)
	explained := 0
	for _, rs := range resets {
		explained += rs[1] - rs[0]
	}
	for c := range cy {
		if cy[c].dr == 0 && cy[c].df == 0 {
			explained++
		}
	}
	dj := 0                   // swdRequestRaces' place in D's edges
	_, _, byI := C.roundUps() // both lines sliced from their edges: each edge within a sample of its own
	byI = byI && D.codes == nil
	next := -1                  // where the transfer just read lets the next request start
	firstC, lastC := len(cy), 0 // the first transfer's or reset's cycle, and the cycle after the last
	for _, rs := range resets {
		firstC, lastC = min(firstC, rs[0]), max(lastC, rs[1])
	}
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
		ack := tb(c+9) | tb(c+10)<<1 | tb(c+11)<<2
		if !parOK || (ack != 1 && ack != 2 && ack != 4) {
			continue // not a request a target answered
		}
		if swdRequestRaces(&D, cy[c:c+8], per, &dj, byI) {
			continue // its bits read where they change: no host's
		}
		o.add(cy[c].xr, cy[c].xr, "", "sof", 0, false)
		o.add(cy[c].xf, cy[c+7].xr+per/2, swdRequestText[rnw][apdp][a2|a3<<1], "data", req, true)
		o.add(cy[c+9].xf, cy[c+11].xr+per/2, swdAckText(ack), "data", ack, true)
		firstC = min(firstC, c)
		if ack != 1 {
			explained += swdUnexplained(cy[c : c+12])
			c += 11 // the turnaround after the acknowledge ends with the next rise
			next = c + 1
			lastC = max(lastC, next)
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
			b := tb(first + k)
			if rnw == 0 {
				b = cy[first+k].dr
			}
			v |= b << k
			cnt += b
		}
		p := tb(first + 32)
		if rnw == 0 {
			p = cy[first+32].dr
		}
		dtxt, kind := hexText("", uint64(uint32(v)), 8), "data"
		if cnt%2 != p {
			dtxt, kind = hexText("!", uint64(uint32(v)), 8), "parity-error"
		} else {
			good = true
		}
		o.add(cy[first].xf, cy[first+31].xr+per/2, dtxt, kind, v, true)
		explained += swdUnexplained(cy[c : first+33])
		c = first + 32 // a read's turnaround ends with its parity cycle's rise
		next = c + 1
		lastC = max(lastC, next)
	}
	flushResets(len(cy))
	if len(o.spans) == 0 {
		return Result{Proto: "swd", Error: "no SWD request"}
	}
	// The record's ends cut a transfer as often as not: the busy cycles
	// before the first transfer and after the last, up to a transfer's
	// length each, are its rest, not cycles SWD leaves unexplained (a record
	// cut through a transfer at both ends fell to 0.78 and the pair read as
	// SPI now and then).
	cut := func(cs []swdCycle) int {
		busy := 0
		for _, y := range cs {
			if y.dr != 0 || y.df != 0 {
				busy++
			}
		}
		return min(busy, swdTransferCycles)
	}
	edges := 0
	if firstC < len(cy) {
		edges = cut(cy[:firstC]) + cut(cy[min(lastC, len(cy)):])
	}
	cover := min(1, float64(explained)/float64(max(1, len(cy)-edges)))
	// data an SPI bus clocks holds request-like bits now and then; SWD's
	// transfers, idle and resets explain most of its clock, which runs at
	// one rate while it runs
	_, _, near := C.roundUps()
	good = good && cover >= 0.5 && swdSteady(cy, per, near)
	res := Result{OK: good, Proto: "swd", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: C.threshold, SPB: per, Cover: cover}
	if colTimeS > 0 {
		res.Baud = int(1/(per*colTimeS) + 0.5)
	}
	if !good {
		res.Error = "no SWD transfer acknowledged OK whose parity checks"
	}
	return res
}

// swdRequestText names a request by RnW, APnDP and A[3:2]: "RDP0", "WAP4".
var swdRequestText = func() (t [2][2][4]string) {
	for rnw, rw := range []string{"W", "R"} {
		for apdp, ap := range []string{"DP", "AP"} {
			for a := range 4 {
				t[rnw][apdp][a] = rw + ap + hexText("", uint64(a<<2), 0)
			}
		}
	}
	return t
}()

// swdAckText names an acknowledge ("" none of the three).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func swdAckText(ack int) string {
	switch ack {
	case 1:
		return "OK"
	case 2:
		return "WAIT"
	case 4:
		return "FAULT"
	}
	return ""
}

// swdTargetAtRise reports whether more of the requests on a clock
// (start 1, stop 0, park 1, parity checking) read an OK acknowledge at the
// rises than at the falls; where as many do, whether more of the reads'
// data words check their parity at the rises. An acknowledge's three bits
// read alike at either edge more often than not; a target that moves SWDIO
// at the fall has its data read there now and then a bit early or late.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func swdTargetAtRise(cy []swdCycle) bool {
	fall, rise := 0, 0
	pf, pr := 0, 0 // read data words whose parity checks at the falls, the rises
	for c := 0; c+12 <= len(cy); c++ {
		w := cy[c : c+12 : c+12] // the request's cycles
		if w[0].dr != 1 || w[6].dr != 0 || w[7].dr != 1 || (w[1].dr+w[2].dr+w[3].dr+w[4].dr)%2 != w[5].dr {
			continue
		}
		okF := w[9].df == 1 && w[10].df == 0 && w[11].df == 0
		okR := w[9].dr == 1 && w[10].dr == 0 && w[11].dr == 0
		if okF {
			fall++
		}
		if okR {
			rise++
		}
		if w[2].dr == 1 && c+45 <= len(cy) { // a read: data on cycles c+12 .. c+44
			nf, nr := 0, 0
			for k := c + 12; k < c+45; k++ {
				nf += cy[k].df
				nr += cy[k].dr
			}
			if okF && nf%2 == 0 {
				pf++
			}
			if okR && nr%2 == 0 {
				pr++
			}
		}
	}
	if rise != fall {
		return rise > fall
	}
	return pr > pf
}

// swdSteady reports a clock whose cycles while it runs (gaps under four
// periods) are mostly a period long. near: the falls are a digital line's
// edges (slice), each gap within a hair of its samples, compared as those
// where they lie clear of a bound.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func swdSteady(cy []swdCycle, per float64, near bool) bool {
	run, even := 0, 0
	gap, lo, hi := 4*per, 0.75*per, 1.25*per
	gapB, loB, hiB := nearBoundOf(gap), nearBoundOf(lo), nearBoundOf(hi)
	for k := 1; k < len(cy); k++ {
		if gi := cy[k].fi - cy[k-1].fi; near && (gi >= gapB.hi || gi <= gapB.lo) {
			if gi >= gapB.hi {
				continue // over four periods
			}
			run++
			switch {
			case gi >= loB.hi && gi <= hiB.lo:
				even++ // within the period's quarter
			case gi <= loB.lo || gi >= hiB.hi:
				// outside it
			default:
				if g := cy[k].xf - cy[k-1].xf; g >= lo && g <= hi {
					even++
				}
			}
			continue
		}
		g := cy[k].xf - cy[k-1].xf
		if g > gap {
			continue
		}
		run++
		if g >= lo && g <= hi {
			even++
		}
	}
	return run > 0 && float64(even) >= 0.8*float64(run)
}

// nearBound is where integers lie clear of a bound t for values within a
// hair (a millionth) of them: an integer up to lo lies below t, one from hi
// above it; between the two the value itself decides.
type nearBound struct{ lo, hi int }

// nearBoundOf is t's nearBound (none for a t out of the integers' range).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func nearBoundOf(t float64) nearBound {
	if !(math.Abs(t) < 1<<30) {
		return nearBound{math.MinInt, math.MaxInt}
	}
	return nearBound{int(math.Ceil(t-1e-6)) - 1, int(math.Floor(t+1e-6)) + 1}
}

// swdTransferCycles is the longest SWD transfer: an 8-bit request, the
// turnaround, a 3-bit acknowledge, a turnaround, 32 data bits and parity.
const swdTransferCycles = 46

// swdRequestRaces reports a request whose SWDIO changes within an eighth of
// a period of a rise it is read at (cy its eight cycles, per the period). A
// host moves SWDIO at the fall, half a period from the rise; data that moves
// at the rise is a bus launching it there (SPI with its clock at 80% duty),
// read now as one bit and now as the other, and its "requests" come and go
// with a millivolt of noise. at keeps its place in D's edges from request
// to request; byI: both lines' edges lie within a sample of their own,
// and an edge and a rise samples apart lie that much apart less one.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func swdRequestRaces(D *sliced, cy []swdCycle, per float64, at *int, byI bool) bool {
	if !D.ok || len(cy) == 0 || !(per > 0) {
		return false
	}
	near := per / 8
	lo, hi := cy[0].xf, cy[len(cy)-1].xr+near
	// the first of D's edges at or past lo: on from the last request's
	// (requests come in order), or searched for when it lies before
	j := *at
	if j > len(D.edges) || (j > 0 && D.edges[j-1].x >= lo) {
		j = sort.Search(len(D.edges), func(k int) bool { return D.edges[k].x >= lo })
	}
	for j < len(D.edges) && D.edges[j].x < lo {
		j++
	}
	*at = j
	far := math.MaxInt // samples apart that put an edge and a rise over near apart
	if byI && near < 1<<30 {
		far = int(math.Ceil(near)) + 2
	}
	for ; j < len(D.edges) && D.edges[j].x <= hi; j++ {
		x, i := D.edges[j].x, D.edges[j].i
		for c := range cy {
			if di := i - cy[c].ri; di >= far || -di >= far {
				continue
			}
			if math.Abs(x-cy[c].xr) < near {
				return true
			}
		}
	}
	return false
}

// swdUnexplained counts a transfer's cycles not counted as idle already.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func swdUnexplained(cy []swdCycle) int {
	n := 0
	for c := range cy {
		if cy[c].dr != 0 || cy[c].df != 0 {
			n++
		}
	}
	return n
}

// mdioCut is the rises a record cuts off MDIO frames at its ends: before the
// first frame's preamble and after the last frame, up to a preamble and a
// frame each (a PHY's frame cut by the record's start fell to 0.61 of the
// clock explained and the pair read as SPI).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func mdioCut(firstR, lastR, n int) int {
	if firstR < 0 {
		return 0
	}
	return min(firstR, 64) + min(n-1-lastR, 64)
}

// DecodeMDIO decodes MDIO management frames (IEEE 802.3 clause 22 and 45):
// the data line read at the clock's rises, a preamble of 16 or more ones,
// the start (01 clause 22, 00 clause 45), the opcode, the 5-bit PHY (port)
// and register (device) addresses, the turnaround and 16 data bits most
// significant first. Units: the header (start, opcode and addresses as 14
// bits; "RD 01.02", "WR45 01.03") and the data ("ABCD"); a turnaround whose
// second bit is not driven low (no PHY answered) flags the data. OK needs a
// frame whose turnaround is good.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodeMDIO(mdc, mdio []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeMDIO(samplesIn(mdc), samplesIn(mdio), colTimeS, cfg)
}

// DecodeMDIOEdges is DecodeMDIO over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodeMDIOEdges(mdc, mdio *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeMDIO(edgesIn(mdc), edgesIn(mdio), colTimeS, cfg)
}

// decodeMDIO is DecodeMDIO over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func decodeMDIO(mdc, mdio lineIn, colTimeS float64, cfg ClockedCfg) Result {
	E, per, C, why := clockedLines(mdc, mdio, cfg)
	if why != "" {
		return Result{Proto: "mdio", Error: why}
	}
	// the rises: where each lies and the data there
	type rise struct {
		x float64
		d int
	}
	R := scratchSlice[rise](mdc.scratch(), len(E)/2+1)
	for k := range E {
		if e := &E[k]; e.dir > 0 {
			R = R[:len(R)+1]
			r := &R[len(R)-1] // set field by field: no struct copied in
			r.x, r.d = e.x, e.d
		}
	}
	o := newWireOut(C.n, C.sc)
	good := false
	ones, explained := 0, 0
	firstR, lastR := -1, -1 // the first frame's preamble's first rise, the last frame's last
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
		name := mdioOpName(st, op)
		if name == "" {
			continue
		}
		head := st<<12 | op<<10 | phy<<5 | reg
		o.add(R[i].x, R[i].x, "", "sof", 0, false)
		o.add(R[i].x, R[i+14].x, name+" "+hex2(phy)+"."+hex2(reg), "data", head, true)
		dtxt, kind := hexText("", uint64(data), 4), "data"
		if ta&1 != 0 {
			dtxt, kind = hexText("!", uint64(data), 4), "frame-error"
		} else {
			good = true
		}
		o.add(R[i+16].x, R[i+31].x+per, dtxt, kind, data, true)
		explained += 32
		if firstR < 0 {
			firstR = i - pre
		}
		lastR = i + 31
		i += 31
	}
	explained += ones // the idle after the last frame
	if len(o.spans) == 0 {
		return Result{Proto: "mdio", Error: "no MDIO frame"}
	}
	res := Result{OK: good, Proto: "mdio", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: C.threshold, SPB: per, Cover: min(1, float64(explained)/float64(max(1, len(R)-mdioCut(firstR, lastR, len(R)))))}
	if colTimeS > 0 {
		res.Baud = int(1/(per*colTimeS) + 0.5)
	}
	if !good {
		res.Error = "no MDIO frame a PHY answered"
	}
	return res
}

// mdioOpName names an MDIO frame's opcode op after its start st (01
// clause 22, 00 clause 45); "" a frame no opcode names.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func mdioOpName(st, op int) string {
	if st == 1 {
		switch op {
		case 1:
			return "WR"
		case 2:
			return "RD"
		}
		return ""
	}
	switch op {
	case 0:
		return "ADDR"
	case 1:
		return "WR45"
	case 2:
		return "RDI45"
	case 3:
		return "RD45"
	}
	return ""
}

// DecodeWiegand decodes Wiegand card readers: a low pulse on D0 (a, the
// first line) is a 0 bit, on D1 (b) a 1; a frame ends where the lines rest
// longer than three bit intervals (and 3 ms). A 26-bit frame holds even
// parity, an 8-bit facility code, a 16-bit card number and odd parity, a
// 34-bit one a 16-bit facility code; each field is a unit ("F12", "C4660",
// decimal as readers print them; flagged when its parity fails). Other
// lengths are their bits in bytes, most significant first. OK needs a 26-
// or 34-bit frame whose parities check.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodeWiegand(d0, d1 []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeWiegand(samplesIn(d0), samplesIn(d1), colTimeS, cfg)
}

// DecodeWiegandEdges is DecodeWiegand over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func DecodeWiegandEdges(d0, d1 *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeWiegand(edgesIn(d0), edgesIn(d1), colTimeS, cfg)
}

// decodeWiegand is DecodeWiegand over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func decodeWiegand(d0, d1 lineIn, colTimeS float64, cfg ClockedCfg) Result {
	if d1.none() {
		return Result{Proto: "wiegand", Error: "needs both lines (D0 and D1)"}
	}
	A := d0.slice(cfg.Threshold, cfg.HaveThr)
	B := d1.slice(cfg.ThresholdB, cfg.HaveThrB)
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
	frameBits := 0
	emit := func(fr []pulse) {
		nb := len(fr)
		if frameBits == 0 || (frameBits != 26 && frameBits != 34 && (nb == 26 || nb == 34)) {
			frameBits = nb
		}
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
		Thr: thr, SPB: interval, FrameBits: frameBits}
	if interval > 0 {
		res.Baud = int(1/(interval*colTimeS) + 0.5)
	}
	if !good {
		res.Error = "no 26- or 34-bit Wiegand frame whose parities check"
	}
	return res
}
