// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"strings"
)

// Single-wire buses of the sigrok set beyond the first thirty: STM8 SWIM,
// TI SDQ, PJON's PJDL link, DSI lighting and the Qi charger's
// communication. Mirrored step for step by decode_wire1.js.

// wireOut collects a decode's spans, transcript and units.
type wireOut struct {
	n     int
	spans []Span
	toks  []string
	units []int
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func (o *wireOut) add(x0, x1 float64, text, kind string, val int, unit bool) {
	o.spans = append(o.spans, pspan(x0, x1, o.n, text, kind, val))
	if text != "" {
		o.toks = append(o.toks, text)
	}
	if unit {
		o.units = append(o.units, val)
	}
}

// fallsAndRises lists a line's falling edges with the rise after each (-1
// when the record ends low).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func fallsAndRises(S sliced) (falls, rises []float64) {
	E := S.edges
	falls, rises = make([]float64, 0, len(E)/2+1), make([]float64, 0, len(E)/2+1)
	for k, e := range E {
		if e.dir > 0 {
			continue
		}
		r := -1.0
		if k+1 < len(E) {
			r = E[k+1].x
		}
		falls, rises = append(falls, e.x), append(rises, r)
	}
	return falls, rises
}

// swimNames name the SWIM commands.
var swimNames = map[int]string{0: "SRST", 1: "ROTF", 2: "WOTF"}

// DecodeSWIM decodes the STM8 SWIM line (sigrok's swim): a bit is a low
// pulse and a high, a 0 low for most of it and a 1 low for a little; the
// bit time is the shortest falling-to-falling spacing's cluster. A frame is
// its bits a bit apart: the start bit (0 from the host, 1 from the
// device), a 3-bit command or 8 data bits, most significant first, their
// parity (even with them) and the receiver's acknowledge (1). A command
// opens a transaction ("sof"): SRST, ROTF and WOTF, the byte count ("N"),
// the 24-bit address ("@", three bytes) and the bytes ("W" written, "R"
// read). A frame not acknowledged or whose parity fails is flagged and does
// not advance the transaction; one short of its bits is flagged too, but
// marked cut ("?" gap) where an end of the record falls in it. Where a
// command is due, a frame running on past a command's bits without a gap is
// a byte of a transaction begun before the record (a trigger amid one). A low of 7.3 to 35.6 us (128 SWIM clocks) is
// a synchronization frame ("SYNC"). Baud is the bit rate. OK needs an
// acknowledged frame whose parity checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeSWIM(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return decodeSWIM(samplesIn(codes), colTimeS, cfg)
}

// DecodeSWIMEdges is DecodeSWIM over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeSWIMEdges(codes *EdgeLine, colTimeS float64, cfg LineCfg) Result {
	return decodeSWIM(edgesIn(codes), colTimeS, cfg)
}

// decodeSWIM is DecodeSWIM over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func decodeSWIM(codes lineIn, colTimeS float64, cfg LineCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "swim", Error: S.reason}
	}
	falls, rises := fallsAndRises(S)
	sp := make([]float64, 0, len(falls))
	for k := 1; k < len(falls); k++ {
		sp = append(sp, falls[k]-falls[k-1])
	}
	if len(sp) < 4 {
		return Result{Proto: "swim", Error: "too few bits"}
	}
	lo := quantile(sp, 0)
	sum, cnt := 0.0, 0
	for _, v := range sp {
		if v <= 1.3*lo {
			sum += v
			cnt++
		}
	}
	T := sum / float64(cnt)
	if cfg.Baud > 0 && colTimeS > 0 {
		T = 1 / (float64(cfg.Baud) * colTimeS)
	}
	o := &wireOut{n: S.n}
	state := 0 // 0 a command, 1 the count, 2..4 the address, 5 the bytes
	left := 0
	good := false
	type sbit struct {
		v      int
		x0, x1 float64
	}
	var fr []sbit
	frame := func() {
		if len(fr) == 0 {
			return
		}
		defer func() { fr = nil }()
		nb := 8
		cmd := state == 0 && len(fr) <= 6 // a command, not a byte met amid a transaction
		if cmd {
			nb = 3
		}
		if len(fr) != nb+3 {
			if fr[0].x0 <= 1.5*T || fr[len(fr)-1].x1+1.5*T >= float64(S.n) {
				o.add(fr[0].x0, fr[len(fr)-1].x1, "?", "gap", 0, false)
			} else {
				o.add(fr[0].x0, fr[len(fr)-1].x1, "!", "frame-error", 0, false)
			}
			return
		}
		v, p := 0, 0
		for i := 1; i <= nb; i++ {
			v = v<<1 | fr[i].v
			p ^= fr[i].v
		}
		ok := p == fr[nb+1].v && fr[nb+2].v == 1
		if cmd {
			o.add(fr[0].x0, fr[0].x0, "", "sof", 0, false)
		}
		txt := ""
		switch {
		case cmd:
			txt = swimNames[v]
			if txt == "" {
				txt = fmt.Sprintf("CMD%d", v)
			}
		case state == 1:
			txt = "N" + hex2(v)
		case state >= 2 && state <= 4:
			txt = "@" + hex2(v)
		default:
			txt = "W" + hex2(v)
			if fr[0].v == 1 {
				txt = "R" + hex2(v)
			}
		}
		if !ok {
			o.add(fr[1].x0, fr[nb].x1, "!"+txt, "frame-error", v, true)
			return
		}
		good = true
		o.add(fr[1].x0, fr[nb].x1, txt, "data", v, true)
		switch {
		case cmd && (v == 1 || v == 2):
			state = 1
		case state == 1:
			state, left = 2, v
		case state >= 2 && state < 5:
			state++
		case state == 5:
			if left--; left <= 0 {
				state = 0
			}
		}
	}
	for k, f := range falls {
		r := rises[k]
		if r < 0 {
			break
		}
		low := r - f
		if colTimeS > 0 {
			// a synchronization frame: 128 SWIM clocks (64 of 8.8 MHz to
			// 128 of 3.6 MHz); it resets the transaction
			if us := low * colTimeS * 1e6; us >= 64/8.8 && us <= 128/3.6 && low > 1.5*T {
				frame()
				o.add(f, r, "SYNC", "rw", 0, false)
				state = 0
				continue
			}
		}
		if low > 1.5*T {
			frame()
			continue
		}
		end := f + T // a frame's last bit: a bit time
		if k+1 < len(falls) && falls[k+1]-f <= 1.5*T {
			end = falls[k+1]
		}
		b := 1
		if low >= end-r {
			b = 0
		}
		if len(fr) > 0 && f-fr[len(fr)-1].x0 > 1.5*T {
			frame()
		}
		fr = append(fr, sbit{b, f, end})
		next := k+1 < len(falls) && falls[k+1]-f <= 1.5*T // the frame runs on
		if (state != 0 || len(fr) != 6 || !next) && (len(fr) == 11 || (state == 0 && len(fr) == 6)) {
			frame()
		}
	}
	frame()
	if len(o.spans) == 0 {
		return Result{Proto: "swim", Error: "no SWIM frame"}
	}
	res := Result{OK: good, Proto: "swim", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, SPB: T}
	if colTimeS > 0 {
		res.Baud = int(math.Round(1 / (T * colTimeS)))
	}
	if !good {
		res.Error = "no acknowledged SWIM frame"
	}
	return res
}

// DecodeSDQ decodes TI's SDQ line (sigrok's sdq): each bit a low pulse at
// the bit time (Baud, 98425 bit/s unless given), under half a bit a 1,
// longer a 0, over 1.2 bits a break ("sof"); eight bits a byte, least
// significant first. A bit closer than 0.7 bits to the last is flagged and
// drops the byte. OK needs two bytes after a break.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeSDQ(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return decodeSDQ(samplesIn(codes), colTimeS, cfg)
}

// DecodeSDQEdges is DecodeSDQ over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeSDQEdges(codes *EdgeLine, colTimeS float64, cfg LineCfg) Result {
	return decodeSDQ(edgesIn(codes), colTimeS, cfg)
}

// decodeSDQ is DecodeSDQ over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func decodeSDQ(codes lineIn, colTimeS float64, cfg LineCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "sdq", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "sdq", Error: "no sample time"}
	}
	baud := 98425.0
	if cfg.Baud > 0 {
		baud = float64(cfg.Baud)
	}
	T := 1 / (baud * colTimeS)
	falls, rises := fallsAndRises(S)
	o := &wireOut{n: S.n}
	v, nb := 0, 0
	x0, prev := 0.0, -1.0
	bytesAfter, good := -1, false
	for k, f := range falls {
		r := rises[k]
		if r < 0 {
			break
		}
		d := r - f
		if d > 1.2*T {
			o.add(f, r, "", "sof", 0, false)
			v, nb, bytesAfter, prev = 0, 0, 0, -1
			continue
		}
		if nb > 0 && f-prev < 0.7*T {
			// bits closer than the bit time: no SDQ at this rate
			o.add(prev, f, "!", "frame-error", 0, false)
			v, nb, bytesAfter = 0, 0, -1
		}
		prev = f
		if nb == 0 {
			x0 = f
		}
		if d <= T/2 {
			v |= 1 << nb
		}
		nb++
		if nb == 8 {
			o.add(x0, f+T, FmtByte(v, cfg.Format), "data", v, true)
			v, nb = 0, 0
			if bytesAfter >= 0 {
				bytesAfter++
				good = good || bytesAfter >= 2
			}
		}
	}
	if len(o.spans) == 0 {
		return Result{Proto: "sdq", Error: "no SDQ bits"}
	}
	res := Result{OK: good, Proto: "sdq", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, SPB: T, Baud: int(math.Round(baud))}
	if !good {
		res.Error = "no SDQ bytes after a break"
	}
	return res
}

// pjdlModes are PJDL's data bit and pad times (us) of modes 1 to 4.
var pjdlModes = [5][2]float64{{}, {44, 116}, {40, 92}, {28, 88}, {26, 60}}

// pjdlIn reports a time (us) within PJDL's tolerance of want: ten percent
// or 1.5 us, whichever is wider.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func pjdlIn(us, want float64) bool {
	lo := math.Min(want*0.9, want-1.5)
	hi := math.Max(want*1.1, want+1.5)
	return us >= lo && us <= hi
}

// crc8PJON is PJON's CRC-8 (reflected polynomial 0x97, from 0).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func crc8PJON(b []int) int {
	c := 0
	for _, v := range b {
		c ^= v & 0xff
		for i := 0; i < 8; i++ {
			if c&1 != 0 {
				c = c>>1 ^ 0x97
			} else {
				c >>= 1
			}
		}
	}
	return c
}

// DecodePJDL decodes PJON over PJDL (sigrok's pjdl and pjon): each byte a
// sync pad (high for the pad time, then a data time low) and eight data
// bits of the mode's data time, least significant first; three sync pads
// in a row open a frame ("sof"). A frame's bytes are PJON's: receiver ID,
// header (bit 6 a 16-bit length, bit 5 a CRC-32), length, the CRC-8 of
// those, and at its end the CRC-8 (or CRC-32) of all bytes before it; a
// failed CRC flags its byte. Variant picks the mode (1..4), else the one
// whose pads the line holds. OK needs a frame whose header CRC checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodePJDL(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return decodePJDL(samplesIn(codes), colTimeS, cfg)
}

// DecodePJDLEdges is DecodePJDL over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodePJDLEdges(codes *EdgeLine, colTimeS float64, cfg LineCfg) Result {
	return decodePJDL(edgesIn(codes), colTimeS, cfg)
}

// decodePJDL is DecodePJDL over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func decodePJDL(codes lineIn, colTimeS float64, cfg LineCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "pjdl", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "pjdl", Error: "no sample time"}
	}
	runs := levelRuns(S)
	us := func(d float64) float64 { return d * colTimeS * 1e6 }
	mode := cfg.Variant
	if mode < 1 || mode > 4 {
		best := 0
		for m := 1; m <= 4; m++ {
			c := 0
			for _, r := range runs {
				if r.lvl == 1 && !r.open && pjdlIn(us(r.x1-r.x0), pjdlModes[m][1]) {
					c++
				}
			}
			if c > best {
				best, mode = c, m
			}
		}
		if best == 0 {
			return Result{Proto: "pjdl", Error: "no PJDL sync pads"}
		}
	}
	data, pad := pjdlModes[mode][0], pjdlModes[mode][1]
	dt := data / us(1) // samples
	o := &wireOut{n: S.n}
	good := false
	var frame []int
	var fx []float64
	inFrame := false
	flush := func() {
		if !inFrame || len(frame) == 0 {
			inFrame = false
			frame, fx = nil, nil
			return
		}
		good = pjonFields(o, frame, fx, dt) || good
		inFrame = false
		frame, fx = nil, nil
	}
	lastEnd := 0.0
	idle := (pad + 9*data + 4) / us(1) // a byte's time and a little: the bus is free
	// highFrom is the high stretch at or after x: from x when the line is
	// high there (a byte's last 1 bits run on into the next pad), else from
	// the next rise; to the fall after it. The x asked for never goes back:
	// the search resumes where the last one stopped.
	ri := 0
	highFrom := func(x float64) (float64, float64, bool) {
		for ri < len(runs) && (runs[ri].x1 <= x || runs[ri].lvl != 1 || runs[ri].open && runs[ri].x0 == 0) {
			ri++
		}
		if ri >= len(runs) || runs[ri].open {
			return 0, 0, false
		}
		return math.Max(runs[ri].x0, x), runs[ri].x1, true
	}
	for x := 0.0; ; {
		a, b, ok := highFrom(x)
		if !ok {
			break
		}
		if !pjdlIn(us(b-a), pad) {
			x = b
			continue
		}
		if inFrame && a-lastEnd > idle {
			flush()
		}
		// a pad: three in a row (each with one data time of low after it)
		// open a frame; the data bits follow the last pad's zero
		pads, e := 1, b
		for pads < 3 {
			c, d, ok := highFrom(e)
			if !ok || !pjdlIn(us(c-e), data) || !pjdlIn(us(d-c), pad) {
				break
			}
			pads, e = pads+1, d
		}
		if pads == 3 {
			flush()
			inFrame = true
			o.add(a, e+dt, "", "sof", 0, false)
			x, lastEnd = e, e+dt
			continue
		}
		v := 0
		for k := 0; k < 8; k++ {
			if logicAt(&S, e+dt*(1.5+float64(k))) == 1 {
				v |= 1 << k
			}
		}
		if inFrame {
			frame = append(frame, v)
			fx = append(fx, e+dt)
		} else {
			o.add(e+dt, e+9*dt, hex2(v), "data", v, true)
		}
		x, lastEnd = e+9*dt, e+9*dt
	}
	flush()
	if len(o.spans) == 0 {
		return Result{Proto: "pjdl", Error: "no PJDL frame"}
	}
	res := Result{OK: good, Proto: "pjdl", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, SPB: dt, Baud: int(math.Round(1e6 / data)), FrameBits: mode}
	if !good {
		res.Error = "no PJON frame whose CRC checks"
	}
	return res
}

// pjonFields adds a PJON frame's bytes (starting at fx, each nine data
// times): true when its header CRC checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func pjonFields(o *wireOut, b []int, fx []float64, dt float64) bool {
	put := func(i int, txt string, bad bool) {
		kind := "data"
		if bad {
			txt, kind = "!"+txt, "frame-error"
		}
		o.add(fx[i], fx[i]+9*dt, txt, kind, b[i], true)
	}
	if len(b) < 4 {
		for i := range b {
			put(i, hex2(b[i]), i == len(b)-1)
		}
		return false
	}
	cfgB := b[1]
	head := 4 // rx id, header, length, meta CRC (8-bit length)
	length := b[2]
	if cfgB&0x40 != 0 {
		head = 5
		if len(b) >= 5 {
			length = b[2]<<8 | b[3]
		}
	}
	metaOK := crc8PJON(b[:head-1]) == b[head-1]
	crc32 := cfgB&0x20 != 0
	endLen := 1
	if crc32 {
		endLen = 4
	}
	complete := length == len(b) && length > head+endLen
	endOK := false
	if complete {
		if crc32 {
			var v uint32
			for _, x := range b[length-4:] {
				v = v<<8 | uint32(x)
			}
			endOK = crc32IEEE(b[:length-4]) == v
		} else {
			endOK = crc8PJON(b[:length-1]) == b[length-1]
		}
	}
	for i := range b {
		bad := false
		switch {
		case i == head-1:
			bad = !metaOK
		case i == len(b)-1:
			bad = !complete || !endOK
		}
		txt := hex2(b[i])
		if i == 0 {
			txt = "RX" + txt
		}
		put(i, txt, bad)
	}
	return metaOK
}

// DecodeDSI decodes DSI (Digital Serial Interface) lighting frames (sigrok's
// dsi): Manchester bits of 1/600 s after an idle of two bits or more, a 1
// with its first half at the marks' level (the level the idle is not): a
// start bit 0, whose first half is the idle's, and eight data bits, most
// significant first, the dimming level (a unit). OK needs a whole frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeDSI(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return decodeDSI(samplesIn(codes), colTimeS, cfg)
}

// DecodeDSIEdges is DecodeDSI over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeDSIEdges(codes *EdgeLine, colTimeS float64, cfg LineCfg) Result {
	return decodeDSI(edgesIn(codes), colTimeS, cfg)
}

// decodeDSI is DecodeDSI over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func decodeDSI(codes lineIn, colTimeS float64, cfg LineCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "dsi", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "dsi", Error: "no sample time"}
	}
	marks, high := irMarks(S, colTimeS)
	n := S.n
	us := func(d float64) float64 { return d * colTimeS * 1e6 }
	const half = 833.33
	hx := half / us(1)
	o := &wireOut{n: n}
	good := false
	for i := 0; i < len(marks); i++ {
		if (i > 0 && us(marks[i].x0-marks[i-1].x1) < 3200) || (i == 0 && us(marks[0].x0) < 1700) {
			continue
		}
		lv, xs := irHalves(marks, i, half, 2, us)
		if len(lv) == 0 {
			continue
		}
		// the start bit's first half is the idle's level
		lv = append([]int{0}, lv...)
		xs = append([]float64{marks[i].x0 - hx}, xs...)
		if len(lv)%2 != 0 {
			lv = append(lv, 0)
			xs = append(xs, xs[len(xs)-1]+hx)
		}
		var bits []int
		ok := true
		for b := 0; 2*b+1 < len(lv); b++ {
			if lv[2*b] == lv[2*b+1] {
				ok = false
				break
			}
			bits = append(bits, lv[2*b])
		}
		o.add(xs[0], xs[0]+2*hx, "START", "sof", 0, false)
		if !ok || len(bits) != 9 || bits[0] != 0 {
			o.add(xs[0], xs[len(xs)-1]+hx, fmt.Sprintf("!%dbits", len(bits)), "frame-error", 0, false)
			continue
		}
		v := 0
		for k := 1; k <= 8; k++ {
			v = v<<1 | bits[k]
		}
		o.add(xs[2], xs[2]+16*hx, FmtByte(v, cfg.Format), "data", v, true)
		good = true
	}
	if len(o.spans) == 0 {
		return Result{Proto: "dsi", Error: "no DSI frame"}
	}
	res := Result{OK: good, Proto: "dsi", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, Inverted: high, SPB: 2 * hx, Baud: 600}
	if !good {
		res.Error = "no whole DSI frame"
	}
	return res
}

// qiLen is a Qi packet's message bytes from its header.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func qiLen(h int) int {
	switch {
	case h <= 0x1f:
		return 1 + h/32
	case h <= 0x7f:
		return 2 + (h-32)/16
	case h <= 0xdf:
		return 8 + (h-128)/8
	}
	return 20 + (h-224)/4
}

// DecodeQi decodes a Qi receiver's packets on the demodulated line (sigrok's
// qi): biphase-mark bits of 2 kbit/s (a 1 has a transition at its middle);
// after a preamble of four 1s or more, bytes of a start 0, eight data bits
// least significant first, odd parity and a stop 1: a header, its message
// (the header gives its length) and a checksum, the exclusive-or of the
// rest ("sof" at each header). A byte whose parity or stop fails, and a
// checksum that does not check, are flagged. OK needs a packet whose
// checksum checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeQi(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return decodeQi(samplesIn(codes), colTimeS, cfg)
}

// DecodeQiEdges is DecodeQi over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func DecodeQiEdges(codes *EdgeLine, colTimeS float64, cfg LineCfg) Result {
	return decodeQi(edgesIn(codes), colTimeS, cfg)
}

// decodeQi is DecodeQi over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func decodeQi(codes lineIn, colTimeS float64, cfg LineCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "qi", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "qi", Error: "no sample time"}
	}
	bit := 500e-6 / colTimeS
	o := &wireOut{n: S.n}
	good := false
	for _, burst := range bmcBits(S, bit) {
		ones := 0
		k := 0
		for ; k < len(burst); k++ {
			if burst[k].v == 1 {
				ones++
				continue
			}
			if ones >= 4 {
				break
			}
			ones = 0
		}
		if r := len(burst) - k; r%11 == 10 && r > 0 {
			// the last stop bit's second half runs into the idle: no edge
			// ends it
			last := burst[len(burst)-1].x0
			burst = append(burst, bmcBit{1, last + bit})
		}
		var pkt []int
		var px []float64
		bad := false
		for ; k+11 <= len(burst); k += 11 {
			c := burst[k : k+11]
			if c[0].v != 0 {
				break
			}
			v, p := 0, 1
			for i := 0; i < 8; i++ {
				v |= c[1+i].v << i
				p ^= c[1+i].v
			}
			if c[9].v != p || c[10].v != 1 {
				bad = true
				o.add(c[0].x0, c[10].x0+bit, "!"+hex2(v), "parity-error", v, true)
				break
			}
			if len(pkt) == 0 {
				o.add(c[0].x0, c[0].x0, "", "sof", 0, false)
			}
			pkt, px = append(pkt, v), append(px, c[0].x0)
			if len(pkt) == qiLen(pkt[0])+2 {
				x := 0
				for _, b := range pkt[:len(pkt)-1] {
					x ^= b
				}
				for i, b := range pkt {
					txt, kind := hex2(b), "data"
					if i == len(pkt)-1 && x != b {
						txt, kind = "!"+txt, "frame-error"
					}
					o.add(px[i], px[i]+11*bit, txt, kind, b, true)
				}
				good = good || x == pkt[len(pkt)-1]
				pkt, px = nil, nil
			}
		}
		if len(pkt) > 0 && !bad {
			for i, b := range pkt {
				o.add(px[i], px[i]+11*bit, hex2(b), "data", b, true)
			}
			o.add(px[len(px)-1]+11*bit, px[len(px)-1]+11*bit, "!", "frame-error", 0, false)
		}
	}
	if len(o.spans) == 0 {
		return Result{Proto: "qi", Error: "no Qi packet"}
	}
	res := Result{OK: good, Proto: "qi", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, SPB: bit, Baud: 2000}
	if !good {
		res.Error = "no Qi packet whose checksum checks"
	}
	return res
}
