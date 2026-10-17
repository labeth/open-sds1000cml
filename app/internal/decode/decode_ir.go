// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"strings"
)

// The infrared remote protocols (NEC, Sony SIRC, RC-5, RC-6) read a line as
// marks (the carrier on) and spaces. The idle level is the level of the
// line's longest run, so a demodulating receiver's active-low output and an
// emitter's active-high drive read alike; a mark still carrying its carrier
// (bursts at 36-40 kHz) is one mark, its gaps under irMergeUs merged.
// Mirrored step for step by decode_ir.js.

// irMergeUs is the longest gap inside a modulated mark: a carrier's half
// period is 12-14 us, the shortest space of these protocols 444 us.
const irMergeUs = 150.0

// irMark is one mark, start to end (samples).
type irMark struct{ x0, x1 float64 }

// IRCfg configures the infrared decoders.
// TRLC-LINKS: REQ-SDS-018
type IRCfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
}

// irMarks finds the line's marks and whether they are its high level.
// TRLC-LINKS: REQ-SDS-018
func irMarks(S sliced, colTimeS float64) ([]irMark, bool) {
	runs := levelRuns(S)
	longest, idle := -1.0, 1
	for _, r := range runs {
		if d := r.x1 - r.x0; d > longest {
			longest, idle = d, r.lvl
		}
	}
	active := 1 - idle
	var marks []irMark
	merge := irMergeUs * 1e-6 / colTimeS
	for _, r := range runs {
		if r.lvl != active || r.open {
			continue
		}
		if k := len(marks); k > 0 && r.x0-marks[k-1].x1 < merge {
			marks[k-1].x1 = r.x1
			continue
		}
		marks = append(marks, irMark{r.x0, r.x1})
	}
	return marks, active == 1
}

// irDecode runs the line through irMarks and a protocol's frame reader.
// TRLC-LINKS: REQ-SDS-018
func irDecode(proto string, codes []uint8, colTimeS float64, cfg IRCfg,
	read func(m []irMark, us func(float64) float64, n int, out *irOut)) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: proto, Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: proto, Error: "no sample time"}
	}
	marks, high := irMarks(S, colTimeS)
	out := &irOut{format: cfg.Format}
	read(marks, func(d float64) float64 { return d * colTimeS * 1e6 }, S.n, out)
	if len(out.spans) == 0 {
		return Result{Proto: proto, Error: "no " + irNames[proto] + " frame"}
	}
	res := Result{OK: out.good, Proto: proto, Spans: out.spans, Text: strings.Join(out.toks, " "), Bytes: out.units,
		Thr: S.threshold, Inverted: high, SPB: out.unitUs * 1e-6 / colTimeS}
	if out.unitUs > 0 {
		res.Baud = int(math.Round(1e6 / out.unitUs))
	}
	if !out.good {
		res.Error = "no complete " + irNames[proto] + " frame"
	}
	return res
}

// irNames name the infrared protocols in messages.
var irNames = map[string]string{"irnec": "NEC", "irsirc": "SIRC", "irrc5": "RC-5", "irrc6": "RC-6"}

// irOut collects a decode's spans, transcript and units.
type irOut struct {
	spans  []Span
	toks   []string
	units  []int
	good   bool
	format string
	unitUs float64 // the protocol's bit time
}

// TRLC-LINKS: REQ-SDS-018
func (o *irOut) add(x0, x1 float64, n int, text, kind string, val int, unit bool) {
	o.spans = append(o.spans, pspan(x0, x1, n, text, kind, val))
	if text != "" {
		o.toks = append(o.toks, text)
	}
	if unit {
		o.units = append(o.units, val)
	}
}

// DecodeIRNEC decodes NEC remotes: a 9 ms mark and 4.5 ms space leader
// ("sof" LDR), then 32 bits, each a 560 us mark and a 560 us (0) or 1690 us
// (1) space, least significant first: address, its inverse (or the high
// byte of a 16-bit address), command and its inverse (flagged when it is
// not one), and a stop mark. A 9 ms mark with a 2.25 ms space is a repeat
// ("sof" REP). OK needs a frame whose command checks.
// TRLC-LINKS: REQ-SDS-018
func DecodeIRNEC(codes []uint8, colTimeS float64, cfg IRCfg) Result {
	return irDecode("irnec", codes, colTimeS, cfg, func(m []irMark, us func(float64) float64, n int, o *irOut) {
		o.unitUs = 1125
		for i := 0; i+1 < len(m); i++ {
			lead := us(m[i].x1 - m[i].x0)
			if lead < 8000 || lead > 10000 {
				continue
			}
			space := us(m[i+1].x0 - m[i].x1)
			if space >= 1800 && space <= 2800 {
				if stop := us(m[i+1].x1 - m[i+1].x0); stop >= 300 && stop <= 800 {
					o.add(m[i].x0, m[i+1].x1, n, "REP", "sof", 0, false)
					i++
				}
				continue
			}
			if space < 3500 || space > 5500 {
				continue
			}
			o.add(m[i].x0, m[i+1].x0, n, "LDR", "sof", 0, false)
			var vals [4]int
			ok := true
			j := i + 1
			for b := 0; b < 32; b++ {
				if j+1 >= len(m) {
					ok = false
					break
				}
				mk, per := us(m[j].x1-m[j].x0), us(m[j+1].x0-m[j].x0)
				bit := -1
				switch {
				case mk < 300 || mk > 800:
				case per >= 800 && per <= 1500:
					bit = 0
				case per >= 1700 && per <= 2800:
					bit = 1
				}
				if bit < 0 {
					ok = false
					break
				}
				vals[b/8] |= bit << (b % 8)
				j++
			}
			if !ok {
				o.add(m[j].x0, m[j].x0, n, "!", "frame-error", 0, false)
				i = j - 1
				continue
			}
			for k := 0; k < 4; k++ {
				x0, x1 := m[i+1+8*k].x0, m[i+9+8*k].x0
				txt, kind := hex2(vals[k]), "data"
				switch k {
				case 0:
					txt = "A" + txt
				case 2:
					txt = "C" + txt
				case 3:
					if vals[2]^vals[3] != 0xff {
						txt, kind = "!"+txt, "frame-error"
					}
				}
				o.add(x0, x1, n, txt, kind, vals[k], true)
			}
			if vals[2]^vals[3] == 0xff {
				o.good = true
			}
			i = j - 1
		}
	})
}

// DecodeIRSIRC decodes Sony SIRC remotes: a 2.4 ms mark ("sof" SIRC), then
// bits each a 600 us space and a 1200 us (1) or 600 us (0) mark, least
// significant first, ended by a longer space: 12, 15 or 20 bits, the 7-bit
// command ("C15"), the address (5 bits, or 8 for 15) and for 20 bits an
// 8-bit extension ("E1A"). Another length is an error. OK needs a frame
// of a valid length.
// TRLC-LINKS: REQ-SDS-018
func DecodeIRSIRC(codes []uint8, colTimeS float64, cfg IRCfg) Result {
	return irDecode("irsirc", codes, colTimeS, cfg, func(m []irMark, us func(float64) float64, n int, o *irOut) {
		o.unitUs = 600
		for i := 0; i < len(m); i++ {
			// (RC-6's leader is a 2.67 ms mark and its spaces 444 or 889 us)
			if w := us(m[i].x1 - m[i].x0); w < 2100 || w > 2620 {
				continue
			}
			var bits []int
			j := i + 1
			for ; j < len(m) && len(bits) < 20; j++ {
				sp := us(m[j].x0 - m[j-1].x1)
				if sp < 450 || sp > 800 {
					break
				}
				mk := us(m[j].x1 - m[j].x0)
				switch {
				case mk >= 350 && mk < 900:
					bits = append(bits, 0)
				case mk >= 900 && mk <= 1500:
					bits = append(bits, 1)
				default:
					j = -1
				}
				if j < 0 {
					break
				}
			}
			if j < 0 {
				o.add(m[i].x0, m[i].x1, n, "SIRC", "sof", 0, false)
				o.add(m[i].x1, m[i].x1, n, "!", "frame-error", 0, false)
				continue
			}
			o.add(m[i].x0, m[i].x1, n, "SIRC", "sof", 0, false)
			nb := len(bits)
			if nb != 12 && nb != 15 && nb != 20 {
				o.add(m[i].x1, m[j-1].x1, n, fmt.Sprintf("!%dbits", nb), "frame-error", 0, false)
				i = j - 1
				continue
			}
			field := func(from, cnt int, pfx string) {
				v := 0
				for k := 0; k < cnt; k++ {
					v |= bits[from+k] << k
				}
				o.add(m[i+1+from].x0, m[i+from+cnt].x1, n, pfx+hex2(v), "data", v, true)
			}
			field(0, 7, "C")
			switch nb {
			case 12:
				field(7, 5, "A")
			case 15:
				field(7, 8, "A")
			case 20:
				field(7, 5, "A")
				field(12, 8, "E")
			}
			o.good = true
			i = j - 1
		}
	})
}

// irHalves cuts runs of marks and spaces into whole units of unit us: the
// level of each unit (1 a mark) from x on, until a run is not 1..maxUnits
// units long (a space that long ends the frame). It returns the levels and
// each unit's start.
// TRLC-LINKS: REQ-SDS-018
func irHalves(m []irMark, from int, unit float64, maxUnits int, us func(float64) float64) ([]int, []float64) {
	var lv []int
	var xs []float64
	for j := from; j < len(m); j++ {
		k := int(math.Round(us(m[j].x1-m[j].x0) / unit))
		if k < 1 || k > maxUnits {
			break
		}
		step := (m[j].x1 - m[j].x0) / float64(k)
		for q := 0; q < k; q++ {
			lv, xs = append(lv, 1), append(xs, m[j].x0+float64(q)*step)
		}
		if j+1 >= len(m) {
			break
		}
		k = int(math.Round(us(m[j+1].x0-m[j].x1) / unit))
		if k < 1 || k > maxUnits {
			break
		}
		step = (m[j+1].x0 - m[j].x1) / float64(k)
		for q := 0; q < k; q++ {
			lv, xs = append(lv, 0), append(xs, m[j].x1+float64(q)*step)
		}
	}
	return lv, xs
}

// DecodeIRRC5 decodes Philips RC-5 remotes: Manchester bits of 1.778 ms (a
// 1 a space then a mark), fourteen most significant first: two start bits,
// the toggle ("rw" T0/T1), a 5-bit address and a 6-bit command (with the
// second start bit inverted as the command's seventh bit, RC-5X). Each
// frame opens after an idle of 5 ms or more ("sof" RC5). OK needs a whole
// frame.
// TRLC-LINKS: REQ-SDS-018
func DecodeIRRC5(codes []uint8, colTimeS float64, cfg IRCfg) Result {
	return irDecode("irrc5", codes, colTimeS, cfg, func(m []irMark, us func(float64) float64, n int, o *irOut) {
		const half = 889.0
		o.unitUs = 2 * half
		for i := 0; i < len(m); i++ {
			// a frame opens after an idle longer than any space in one
			if (i > 0 && us(m[i].x0-m[i-1].x1) < 5000) || (i == 0 && us(m[0].x0) < 2000) {
				continue
			}
			lv, xs := irHalves(m, i, half, 2, us)
			// the first start bit's space half precedes the first mark
			hx := half / us(1) // samples
			lv = append([]int{0}, lv...)
			xs = append([]float64{m[i].x0 - hx}, xs...)
			for len(lv) < 28 { // a last 0 bit's space runs into the idle
				lv = append(lv, 0)
				xs = append(xs, xs[len(xs)-1]+hx)
			}
			bits := make([]int, 14)
			ok := true
			for b := 0; b < 14; b++ {
				switch {
				case lv[2*b] == 0 && lv[2*b+1] == 1:
					bits[b] = 1
				case lv[2*b] == 1 && lv[2*b+1] == 0:
					bits[b] = 0
				default:
					ok = false
				}
			}
			o.add(xs[0], xs[0]+2*hx, n, "RC5", "sof", 0, false)
			if !ok || bits[0] != 1 {
				o.add(xs[0], xs[0], n, "!", "frame-error", 0, false)
				continue
			}
			bx := func(b int) float64 { return xs[2*b] }
			o.add(bx(2), bx(3), n, fmt.Sprintf("T%d", bits[2]), "rw", bits[2], false)
			a, c := 0, 0
			for k := 0; k < 5; k++ {
				a = a<<1 | bits[3+k]
			}
			for k := 0; k < 6; k++ {
				c = c<<1 | bits[8+k]
			}
			c |= (1 - bits[1]) << 6
			o.add(bx(3), bx(8), n, "A"+hex2(a), "data", a, true)
			o.add(bx(8), bx(13)+2*hx, n, "C"+hex2(c), "data", c, true)
			o.good = true
		}
	})
}

// DecodeIRRC6 decodes Philips RC-6 remotes (t = 444 us): a 6t mark and 2t
// space leader ("sof" RC6), a start bit, three mode bits ("rw" M0..M7) and
// the double-length trailer bit (the toggle, "rw" T0/T1), then the payload
// in Manchester bits of 2t (a 1 a mark then a space), most significant
// first: mode 0's 8-bit address and command; other modes' payload bytes.
// OK needs a frame with a whole payload.
// TRLC-LINKS: REQ-SDS-018
func DecodeIRRC6(codes []uint8, colTimeS float64, cfg IRCfg) Result {
	return irDecode("irrc6", codes, colTimeS, cfg, func(m []irMark, us func(float64) float64, n int, o *irOut) {
		const t = 444.0
		o.unitUs = 2 * t
		for i := 0; i+1 < len(m); i++ {
			lead, sp := us(m[i].x1-m[i].x0), us(m[i+1].x0-m[i].x1)
			if lead < 2200 || lead > 3100 || sp < 650 || sp > 1150 {
				continue
			}
			o.add(m[i].x0, m[i+1].x0, n, "RC6", "sof", 0, false)
			lv, xs := irHalves(m, i+1, t, 3, us)
			ux := t / us(1) // samples
			// manchester reads a pair of units, a 1 a mark then a space
			pair := func(u int) int {
				if u+1 >= len(lv) {
					return -1
				}
				switch {
				case lv[u] == 1 && lv[u+1] == 0:
					return 1
				case lv[u] == 0 && lv[u+1] == 1:
					return 0
				}
				return -1
			}
			for len(lv)%2 != 0 || len(lv) < 12 { // the idle after a last 1 bit
				lv = append(lv, 0)
				if len(xs) > 0 {
					xs = append(xs, xs[len(xs)-1]+ux)
				} else {
					xs = append(xs, m[i+1].x0)
				}
			}
			start := pair(0)
			mode := 0
			ok := start == 1
			for b := 0; b < 3 && ok; b++ {
				v := pair(2 + 2*b)
				ok = v >= 0
				mode = mode<<1 | v
			}
			toggle := -1
			if ok {
				switch {
				case lv[8] == 1 && lv[9] == 1 && lv[10] == 0 && lv[11] == 0:
					toggle = 1
				case lv[8] == 0 && lv[9] == 0 && lv[10] == 1 && lv[11] == 1:
					toggle = 0
				}
				ok = toggle >= 0
			}
			if !ok {
				o.add(m[i+1].x0, m[i+1].x0, n, "!", "frame-error", 0, false)
				continue
			}
			o.add(xs[2], xs[8], n, fmt.Sprintf("M%d", mode), "rw", mode, false)
			o.add(xs[8], xs[11]+ux, n, fmt.Sprintf("T%d", toggle), "rw", toggle, false)
			var bits []int
			for u := 12; ; u += 2 {
				v := pair(u)
				if v < 0 {
					if u+1 < len(lv) || u < len(lv) && lv[u] == 1 {
						bits = nil // a coding error inside the payload
					}
					break
				}
				bits = append(bits, v)
			}
			if mode == 0 && len(bits) != 16 || len(bits) == 0 || len(bits)%8 != 0 {
				o.add(xs[11]+ux, xs[11]+ux, n, "!", "frame-error", 0, false)
				continue
			}
			for b := 0; b < len(bits)/8; b++ {
				v := 0
				for k := 0; k < 8; k++ {
					v = v<<1 | bits[8*b+k]
				}
				txt := hex2(v)
				if mode == 0 {
					txt = []string{"A", "C"}[b] + txt
				}
				o.add(xs[12+16*b], xs[12+16*b+15]+ux, n, txt, "data", v, true)
			}
			o.good = true
		}
	})
}
