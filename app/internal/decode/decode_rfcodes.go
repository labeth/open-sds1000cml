// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"strings"
)

// The line codes of simple radio and timing links read off one demodulated
// line: remote-control encoders (PT2262 family and EV1527), Oregon
// Scientific sensors' Manchester OOK, Miller coding, Morse code and the
// DCF77 time code. Mirrored step for step by decode_rfcodes.js.

// mark is one run of a line's active level: start, end (samples) and the
// idle after it up to the next mark (+Inf after the last).
type mark struct{ x0, x1, gap float64 }

// activeMarks finds a line's marks: the active level is the one its longest
// run is not (the idle between frames, a sync gap, a minute marker). It
// reports whether the marks are the high level.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func activeMarks(S sliced) ([]mark, bool) {
	runs := levelRuns(S)
	longest, idle := -1.0, 0
	for _, r := range runs {
		if d := r.x1 - r.x0; d > longest {
			longest, idle = d, r.lvl
		}
	}
	var out []mark
	for _, r := range runs {
		if r.lvl == idle || r.open {
			continue
		}
		if k := len(out); k > 0 {
			out[k-1].gap = r.x0 - out[k-1].x1
		}
		out = append(out, mark{r.x0, r.x1, math.Inf(1)})
	}
	return out, idle == 0
}

// lead is the idle before mark i: from the last mark, or the record's start.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func lead(m []mark, i int) float64 {
	if i == 0 {
		return m[0].x0
	}
	return m[i-1].gap
}

// quantile is the value at fraction q of a sorted copy of v (0 for none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func quantile(v []float64, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return nthValue(v, int(q*float64(len(v)-1)))
}

// rcTrit reads a PT2262 trit from two pulses (high, low, high, low) the way
// sigrok's rc_encode does: 0 short-long short-long, 1 long-short long-short,
// F short-long long-short (a long pulse two to five times the short, equal
// ones within half and one and a half); -1 none of these.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func rcTrit(e [4]float64) int {
	long := func(l, s float64) bool { return l >= 2*s && l <= 5*s }
	eq := func(a, b float64) bool { return a >= 0.5*b && a <= 1.5*b }
	switch {
	case long(e[1], e[0]) && eq(e[2], e[0]) && long(e[3], e[0]):
		return 0
	case long(e[0], e[1]) && eq(e[0], e[2]) && long(e[0], e[3]):
		return 1
	case long(e[1], e[0]) && long(e[2], e[0]) && eq(e[3], e[0]):
		return 2
	}
	return -1
}

// rcBit reads an EV1527 bit from one pulse: 0 short high and a long low, 1
// a long high and a short low (one and a half to five times); -1 neither.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func rcBit(h, l float64) int {
	switch {
	case l >= 1.5*h && l <= 5*h:
		return 0
	case h >= 1.5*l && h <= 5*l:
		return 1
	}
	return -1
}

// DecodeRCEncode decodes remote-control encoder frames (PT2262, SC5262,
// HX2262 and EV1527 family): after a sync gap (a low ten short pulses long
// or more, the record's start too) come 24 pulses, read as twelve
// trits of two pulses (PT2262: "0", "1", "F" as 0, 1, 2) when all are
// trits or Variant is 12, else as 24 bits of one pulse (EV1527, Variant
// 24: three bytes, each least significant bit first), then the sync pulse.
// A pulse that reads as neither is flagged. Baud is the short pulse's rate,
// FrameBits 12 or 24. OK needs a whole frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func DecodeRCEncode(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "rcencode", Error: S.reason}
	}
	m, high := activeMarks(S)
	if len(m) < 25 {
		return Result{Proto: "rcencode", Error: "too few pulses"}
	}
	var w []float64
	for _, k := range m {
		w = append(w, k.x1-k.x0)
	}
	short := quantile(w, 0.25)
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good, frameBits := false, 0
	for i := 0; i+25 <= len(m); i++ {
		if lead(m, i) < 10*short {
			continue
		}
		f := m[i : i+25]
		// the 25th pulse is the sync: a long low after it, and none inside
		if f[24].gap < 10*short {
			continue
		}
		inner := true
		for _, k := range f[:24] {
			if k.gap >= 10*short {
				inner = false
			}
		}
		if !inner {
			continue
		}
		spans = append(spans, pspan(f[0].x0, f[0].x0, n, "", "sof", 0))
		trits := make([]int, 12)
		allTrits := true
		for t := 0; t < 12; t++ {
			a, b := f[2*t], f[2*t+1]
			trits[t] = rcTrit([4]float64{a.x1 - a.x0, a.gap, b.x1 - b.x0, b.gap})
			allTrits = allTrits && trits[t] >= 0
		}
		bad := false
		if cfg.Variant == 12 || (cfg.Variant != 24 && allTrits) {
			for t := 0; t < 12; t++ {
				txt, kind := "", "data"
				switch trits[t] {
				case 0, 1:
					txt = fmt.Sprint(trits[t])
				case 2:
					txt = "F"
				default:
					txt, kind, bad = "!", "frame-error", true
				}
				spans = append(spans, pspan(f[2*t].x0, f[2*t+1].x1+f[2*t+1].gap, n, txt, kind, max(0, trits[t])))
				toks = append(toks, txt)
				units = append(units, max(0, trits[t]))
			}
			if frameBits == 0 {
				frameBits = 12
			}
		} else {
			for by := 0; by < 3; by++ {
				v, ok := 0, true
				for k := 0; k < 8; k++ {
					p := f[8*by+k]
					b := rcBit(p.x1-p.x0, p.gap)
					if b < 0 {
						ok = false
						b = 0
					}
					v |= b << k
				}
				txt, kind := hex2(v), "data"
				if !ok {
					txt, kind, bad = "!"+txt, "frame-error", true
				}
				last := f[8*by+7]
				spans = append(spans, pspan(f[8*by].x0, last.x1+last.gap, n, txt, kind, v))
				toks = append(toks, txt)
				units = append(units, v)
			}
			if frameBits == 0 {
				frameBits = 24
			}
		}
		spans = append(spans, pspan(f[24].x0, f[24].x1, n, "SYNC", "stop", 0))
		toks = append(toks, "SYNC")
		good = good || !bad
		i += 24
	}
	if len(spans) == 0 {
		return Result{Proto: "rcencode", Error: "no encoder frame"}
	}
	res := Result{OK: good, Proto: "rcencode", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, Inverted: !high, SPB: short, FrameBits: frameBits}
	if colTimeS > 0 && short > 0 {
		res.Baud = int(math.Round(1 / (short * colTimeS)))
	}
	if !good {
		res.Error = "no whole encoder frame"
	}
	return res
}

// manchesterHalves cuts a line's runs into half bits of hp samples: each
// run one or two halves (rounded), its level each; a run of another length
// ends the list. It returns the levels and each half's start, from run k on.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func manchesterHalves(runs []lrun, k int, hp float64) ([]int, []float64) {
	var lv []int
	var xs []float64
	for ; k < len(runs); k++ {
		r := runs[k]
		q := int(math.Round((r.x1 - r.x0) / hp))
		if r.open && q > 2 {
			q = 2
		}
		if q < 1 || q > 2 {
			break
		}
		for j := 0; j < q; j++ {
			lv = append(lv, r.lvl)
			xs = append(xs, r.x0+float64(j)*(r.x1-r.x0)/float64(q))
		}
	}
	return lv, xs
}

// shortestRunCluster is the mean of a line's closed runs within 1.5 times
// the shortest one: a Manchester line's half bit.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func shortestRunCluster(runs []lrun) float64 {
	minR := math.Inf(1)
	for _, r := range runs {
		if !r.open && r.x1-r.x0 < minR {
			minR = r.x1 - r.x0
		}
	}
	sum, cnt := 0.0, 0
	for _, r := range runs {
		if !r.open && r.x1-r.x0 <= 1.5*minR {
			sum += r.x1 - r.x0
			cnt++
		}
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// DecodeOOK decodes Oregon Scientific weather sensors' on-off keyed
// Manchester bursts (sigrok's ook with ook_oregon): a bit is 1 when its
// first half is the high level (the carrier on), each burst's bits read
// from its first edge at a half bit of the shortest runs. Protocol 3 (and
// 2.1, whose bits come twice, inverted then as sent) opens with a preamble
// of ones and the sync nibble A ("sof"), then nibbles least significant
// bit first: units, hex. The checksum, the sum of the nibbles before it
// (2.1: or that sum less ten), sits low nibble first at the end or before a
// two-nibble CRC; a burst whose checksum does not check is flagged.
// Variant is 21 or 3, the version read first. Cover is 1 when a burst
// after its sync ran past the record's end (its checksum cut off). OK needs
// a burst whose checksum checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func DecodeOOK(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "ook", Error: S.reason}
	}
	runs := levelRuns(S)
	hpg := shortestRunCluster(runs)
	if !(hpg > 0) {
		return Result{Proto: "ook", Error: "no bursts"}
	}
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good, ver, cut := false, 0, false
	hp, hpRead := hpg, hpg // a burst's half bit; the last read one's
	for k := 0; k < len(runs); k++ {
		if !runs[k].open && runs[k].x1-runs[k].x0 < 8*hpg {
			continue
		}
		// a burst: the runs after an idle of eight of the record's shortest
		// half bits or more, up to the next such idle; each burst at its
		// own half bit
		e := k + 1
		for e < len(runs) && !runs[e].open && runs[e].x1-runs[e].x0 < 8*hpg {
			e++
		}
		burst := runs[k+1 : e]
		hp = shortestRunCluster(burst)
		if !(hp > 0) {
			continue
		}
		lv, xs := manchesterHalves(burst, 0, hp)
		var bits []int
		var bx []float64
		// a burst opens with its first edge at a bit's middle or start: the
		// pairing that reads more bits wins (the wrong one breaks at the
		// first double-length run)
		for off := 0; off < 2; off++ {
			var b []int
			var x []float64
			for j := off; j+1 < len(lv); j += 2 {
				if lv[j] == lv[j+1] {
					break
				}
				b = append(b, lv[j])
				x = append(x, xs[j])
			}
			if len(b) > len(bits) {
				bits, bx = b, x
			}
		}
		if len(bits) < 16 {
			continue
		}
		v, from := 0, -1
		// protocol 3: twelve ones or more, then 0101
		ones := 0
		for j := 0; j+4 <= len(bits); j++ {
			if ones >= 12 && bits[j] == 0 && bits[j+1] == 1 && bits[j+2] == 0 && bits[j+3] == 1 {
				v, from = 3, j+4
				break
			}
			ones = (ones + 1) * bits[j]
		}
		if from < 0 {
			// protocol 2.1: pairs (inverse, bit); seventeen preamble bits or
			// more (its first may merge into the idle), the sync's pairs
			for j := 17; j+8 <= len(bits); j++ {
				s := bits[j : j+8]
				if s[0] == 1 && s[1] == 0 && s[2] == 0 && s[3] == 1 && s[4] == 1 && s[5] == 0 && s[6] == 0 && s[7] == 1 && bits[j-1] == 1 && bits[j-2] == 0 {
					v, from = 21, j+8
					break
				}
			}
		}
		if from < 0 {
			continue
		}
		data, dx := bits[from:], bx[from:]
		if v == 21 {
			var d []int
			var x []float64
			for j := 0; j+1 < len(data); j += 2 {
				if data[j] == data[j+1] {
					break
				}
				d = append(d, data[j+1])
				x = append(x, dx[j])
			}
			data, dx = d, x
		}
		if len(data) < 32 {
			continue
		}
		if ver == 0 {
			ver = v
		}
		hpRead = hp
		bitLen := 2 * hp
		if v == 21 {
			bitLen = 4 * hp
		}
		spans = append(spans, pspan(bx[0], dx[0], n, "", "sof", 0))
		var nib []int
		for j := 0; j+4 <= len(data); j += 4 {
			nib = append(nib, data[j]|data[j+1]<<1|data[j+2]<<2|data[j+3]<<3)
		}
		ck := oregonChecksum(nib, v)
		for j, x := range nib {
			txt, kind := fmt.Sprintf("%X", x), "data"
			if ck < 0 && j == len(nib)-1 {
				txt, kind = "!"+txt, "frame-error"
			}
			spans = append(spans, pspan(dx[4*j], dx[4*j+3]+bitLen, n, txt, kind, x))
			toks = append(toks, txt)
			units = append(units, x)
		}
		if ck >= 0 {
			good = true
		} else if e == len(runs) {
			cut = true
		}
		k = e - 1 // the idle after the burst opens the next
	}
	if len(spans) == 0 {
		return Result{Proto: "ook", Error: "no Oregon burst"}
	}
	res := Result{OK: good, Proto: "ook", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: 2 * hpRead, FrameBits: ver}
	if colTimeS > 0 {
		res.Baud = int(math.Round(1 / (2 * hpRead * colTimeS)))
	}
	if cut {
		res.Cover = 1
	}
	if !good {
		res.Error = "no Oregon burst whose checksum checks"
	}
	return res
}

// oregonChecksum finds where an Oregon burst's checksum sits: the nibble
// index of its low nibble (the last two nibbles, or the two before a
// two-nibble CRC), -1 when neither checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func oregonChecksum(nib []int, ver int) int {
	for _, at := range []int{len(nib) - 2, len(nib) - 4} {
		if at < 8 {
			continue
		}
		sum := 0
		for _, x := range nib[:at] {
			sum += x
		}
		got := nib[at] | nib[at+1]<<4
		if sum&0xff == got || (ver == 21 && (sum-10)&0xff == got) {
			return at
		}
	}
	return -1
}

// DecodeMiller decodes Miller coding (delay modulation; sigrok's miller): a
// 1 has a transition at its middle, a 0 at its start when it follows a 0
// and none after a 1. A line whose low runs are all pauses under a third
// of a bit is modified Miller (ISO 14443 A), read at its falling edges;
// otherwise every edge counts. A message opens with a 0 at its first edge
// and ends at an idle of two bits and a half; its bits make bytes least
// significant first (a last partial byte too). Baud 0 reads the bit time
// off the edges' spacing (one, one and a half or two bits). Cover is the
// share of the spacings inside messages that are a bit and a half (none on
// a Manchester or NRZ line). OK needs a message of a byte or more without a
// coding error.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func DecodeMiller(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "miller", Error: S.reason}
	}
	runs := levelRuns(S)
	var lows, highs []float64
	for _, r := range runs {
		if r.open {
			continue
		}
		if r.lvl == 0 {
			lows = append(lows, r.x1-r.x0)
		} else {
			highs = append(highs, r.x1-r.x0)
		}
	}
	var xs []float64
	for _, e := range S.edges {
		xs = append(xs, e.x)
	}
	var iv []float64
	for k := 1; k < len(xs); k++ {
		iv = append(iv, xs[k]-xs[k-1])
	}
	if len(iv) < 8 {
		return Result{Proto: "miller", Error: "too few edges"}
	}
	// pauses: the edges that count are the falls
	pauses := quantile(lows, 1) < 0.4*quantile(highs, 0.5)
	if pauses {
		xs = xs[:0]
		for _, e := range S.edges {
			if e.dir < 0 {
				xs = append(xs, e.x)
			}
		}
		iv = iv[:0]
		for k := 1; k < len(xs); k++ {
			iv = append(iv, xs[k]-xs[k-1])
		}
	}
	T := 0.0
	if cfg.Baud > 0 && colTimeS > 0 {
		T = 1 / (float64(cfg.Baud) * colTimeS)
	} else {
		lo := quantile(iv, 0)
		// within a message the spacings are 1, 1.5 and 2 bits: the shortest
		// is 1.5 bits when the longest is 4/3 of it
		var in []float64
		for _, v := range iv {
			if v < 2.6*lo {
				in = append(in, v)
			}
		}
		hi := quantile(in, 1)
		// the shortest spacing's mean, over those within a tenth of it
		sum, cnt := 0.0, 0
		for _, v := range iv {
			if v <= 1.1*lo {
				sum += v
				cnt++
			}
		}
		T = sum / float64(cnt)
		if r := hi / lo; r > 1.2 && r < 1.45 {
			T /= 1.5
		}
	}
	if !(T > 0) {
		return Result{Proto: "miller", Error: "no bit time"}
	}
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	inMsg, halfMore := 0, 0 // spacings inside messages; of a bit and a half
	for k := 0; k < len(xs); {
		// a message: its first edge after an idle (or the record's start)
		if k > 0 && xs[k]-xs[k-1] < 2*T {
			k++
			continue
		}
		type mb struct {
			v      int
			x0, x1 float64
		}
		bits := []mb{{0, xs[k], xs[k] + T}}
		prev, start, bad := 0, xs[k]+T, false
		j := k + 1
		for ; j < len(xs); j++ {
			q := (xs[j] - xs[j-1]) / T * 2
			d := math.Round(q) / 2
			if d < 2.5 && math.Abs(q-2*d) > 0.3 {
				// off a half-bit multiple by over 15 % of a bit: no Miller
				bad = true
				break
			}
			if d < 2.5 {
				inMsg++
				if d == 1.5 {
					halfMore++
				}
			}
			if prev == 0 {
				// after a 0: a 0 has its edge a bit later, a 1 at a bit and
				// a half; two bits or more are the idle after the message
				if d >= 2 {
					break
				}
				switch d {
				case 1:
					bits = append(bits, mb{0, xs[j], xs[j] + T})
					start = xs[j] + T
				case 1.5:
					bits = append(bits, mb{1, start, xs[j] + T/2})
					prev, start = 1, xs[j]+T/2
				default:
					bad = true
				}
			} else {
				// after a 1: a 1 a bit later, 0 0 at a bit and a half, 0 1 at
				// two; longer is a last 0 and the idle
				if d >= 2.5 {
					bits = append(bits, mb{0, start, start + T})
					break
				}
				switch d {
				case 1:
					bits = append(bits, mb{1, start, xs[j] + T/2})
					start = xs[j] + T/2
				case 1.5:
					bits = append(bits, mb{0, start, xs[j]}, mb{0, xs[j], xs[j] + T})
					prev, start = 0, xs[j]+T
				case 2:
					bits = append(bits, mb{0, start, start + T}, mb{1, xs[j] - T/2, xs[j] + T/2})
					start = xs[j] + T/2
				default:
					bad = true
				}
			}
			if bad {
				break
			}
		}
		if !bad && j == len(xs) && prev == 1 && float64(n)-xs[j-1] >= 2.5*T {
			// the record ends in the idle after a 1: the last 0 has no edge
			bits = append(bits, mb{0, start, start + T})
		}
		spans = append(spans, pspan(bits[0].x0, bits[0].x0, n, "", "sof", 0))
		for b := 0; b < len(bits); b += 8 {
			v := 0
			e := min(b+8, len(bits))
			for q := b; q < e; q++ {
				v |= bits[q].v << (q - b)
			}
			txt := hex2(v)
			spans = append(spans, pspan(bits[b].x0, bits[e-1].x1, n, txt, "data", v))
			toks = append(toks, txt)
			units = append(units, v)
		}
		if bad {
			spans = append(spans, pspan(xs[j], xs[j], n, "!", "frame-error", 0))
			toks = append(toks, "!")
			k = j + 1
			continue
		}
		if len(bits) >= 8 {
			good = true
		}
		k = j // the edge after the idle opens the next message
	}
	res := Result{OK: good, Proto: "miller", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: T, Inverted: pauses}
	if inMsg > 0 {
		res.Cover = float64(halfMore) / float64(inMsg)
	}
	if colTimeS > 0 {
		res.Baud = int(math.Round(1 / (T * colTimeS)))
	}
	if !good {
		res.Error = "no Miller message"
	}
	return res
}

// morseCode is the Morse alphabet the decoder reads, a code as dots and
// dashes.
var morseCode = map[string]byte{".-": 'A', "-...": 'B', "-.-.": 'C', "-..": 'D', ".": 'E', "..-.": 'F', "--.": 'G',
	"....": 'H', "..": 'I', ".---": 'J', "-.-": 'K', ".-..": 'L', "--": 'M', "-.": 'N', "---": 'O', ".--.": 'P',
	"--.-": 'Q', ".-.": 'R', "...": 'S', "-": 'T', "..-": 'U', "...-": 'V', ".--": 'W', "-..-": 'X', "-.--": 'Y',
	"--..": 'Z', ".----": '1', "..---": '2', "...--": '3', "....-": '4', ".....": '5', "-....": '6', "--...": '7',
	"---..": '8', "----.": '9', "-----": '0', ".-.-.-": '.', "--..--": ',', "---...": ':', "..--..": '?',
	"-....-": '-', "-..-.": '/', "-.--.": '(', "-.--.-": ')', "-...-": '=', ".-.-.": '+', ".--.-.": '@'}

// MorseOf is a character's Morse code ("" for none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func MorseOf(c byte) string { return morseOf[c] }

// morseOf is morseCode turned round: each character's code (each has one).
var morseOf = func() (t [256]string) {
	for k, v := range morseCode {
		t[v] = k
	}
	return t
}()

// DecodeMorse decodes Morse code on a keyed line (sigrok's morse): marks
// shorter than two units are dots, longer ones dashes; a space under two
// units parts elements, under five letters, and longer ones words (each
// word's first letter "sof"). The unit is the mean of the marks within one
// and a half times the shortest. Each letter is a unit, its character code;
// a code outside the alphabet is "?" and flagged. Baud is units a second,
// Cover the share of the marks within a quarter of a dot or a dash (three
// units). OK needs two letters.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func DecodeMorse(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "morse", Error: S.reason}
	}
	m, high := activeMarks(S)
	if len(m) < 2 {
		return Result{Proto: "morse", Error: "too few marks"}
	}
	minW := math.Inf(1)
	for _, k := range m {
		minW = math.Min(minW, k.x1-k.x0)
	}
	sum, cnt := 0.0, 0
	for _, k := range m {
		if k.x1-k.x0 <= 1.5*minW {
			sum += k.x1 - k.x0
			cnt++
		}
	}
	u := sum / float64(cnt)
	if cfg.Baud > 0 && colTimeS > 0 {
		u = 1 / (float64(cfg.Baud) * colTimeS)
	}
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	letters := 0
	// A letter's code is at most six elements: past seven it is no letter,
	// and keeping it whole made a line that never pauses for a letter (a
	// fast bus, noise) cost quadratic time and memory.
	var code [8]byte
	nc := 0
	x0 := 0.0
	timed := 0 // marks within a quarter of a dot or a dash
	for _, k := range m {
		if w := (k.x1 - k.x0) / u; (w >= 0.75 && w <= 1.25) || (w >= 2.25 && w <= 3.75) {
			timed++
		}
	}
	for i, k := range m {
		if nc == 0 {
			x0 = k.x0
			if i == 0 || lead(m, i) >= 5*u {
				spans = append(spans, pspan(k.x0, k.x0, n, "", "sof", 0))
			}
		}
		el := byte('-')
		if k.x1-k.x0 < 2*u {
			el = '.'
		}
		code[min(nc, len(code)-1)] = el
		nc = min(nc+1, len(code))
		if k.gap < 2*u {
			continue
		}
		c, ok := morseCode[string(code[:nc])]
		txt, kind := string(rune(c)), "data"
		if !ok {
			txt, kind, c = "?", "frame-error", '?'
		}
		spans = append(spans, pspan(x0, k.x1, n, txt, kind, int(c)))
		toks = append(toks, txt)
		units = append(units, int(c))
		if ok {
			letters++
		}
		nc = 0
	}
	good := letters >= 2
	res := Result{OK: good, Proto: "morse", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, Inverted: !high, SPB: u, Cover: float64(timed) / float64(len(m))}
	if colTimeS > 0 {
		res.Baud = int(math.Round(1 / (u * colTimeS)))
	}
	if !good {
		res.Error = "too few Morse letters"
	}
	return res
}

// dcf77Fields are the time code's fields: the first bit, the width and the
// name of each unit (its value BCD, least significant bit first).
var dcf77Fields = []struct {
	at, w int
	name  string
}{{21, 7, "MIN"}, {29, 6, "HR"}, {36, 6, "DAY"}, {42, 3, "DOW"}, {45, 5, "MON"}, {50, 8, "YR"}}

// dcf77Second is the second of a time code sent faster than the
// broadcast's (a test source's): the median spacing of the pulses' starts,
// from 1 ms to 2 s; 0 for the broadcast's own (within 10 % of 1 s) or none.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func dcf77Second(m []mark, colTimeS float64) float64 {
	var sp []float64
	for i := 1; i < len(m); i++ {
		sp = append(sp, (m[i].x0-m[i-1].x0)*colTimeS)
	}
	s := medianOf(sp)
	if s < 1e-3 || s > 2 || (s >= 0.9 && s <= 1.1) {
		return 0
	}
	return s
}

// DecodeDCF77 decodes the DCF77 time code on a receiver's output (sigrok's
// dcf77): a pulse each second, 100 ms a 0 and 200 ms a 1 (40..160 and
// 161..260 ms), and none in the 59th second: a pulse 1.6 to 2.4 s after the
// one before is second 0 of a minute ("sof"). The minute (BCD, bits 21..27,
// even parity at 28), hour (29..34, parity 35), day (36..41), day of the
// week (42..44), month (45..49) and year (50..57, the date's parity at 58)
// are units, their BCD values; a failed parity flags its field (the
// date's parity all four date fields). Baud scales the code (its bits a
// second); 0 reads the second off the pulses' spacing (dcf77Second), the
// broadcast's 1 s near it. OK needs a field whose parity checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func DecodeDCF77(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "dcf77", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "dcf77", Error: "no sample time"}
	}
	m, high := activeMarks(S)
	sec := 1.0 // the code's second (Baud: its bits a second)
	if cfg.Baud > 0 {
		sec = 1 / float64(cfg.Baud)
	} else if s := dcf77Second(m, colTimeS); s > 0 {
		sec = s
	}
	ms := func(d float64) float64 { return d * colTimeS * 1e3 / sec }
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	c := -1 // the second of the minute; -1 unknown
	var bits [59]int
	var bx [60]float64
	// unit adds field f (with par, its parity failed)
	unit := func(f int, par bool) {
		d := dcf77Fields[f]
		v := 0
		for q := 0; q < d.w; q++ {
			v |= bits[d.at+q] << q
		}
		txt, kind := fmt.Sprintf("%s%X", d.name, v), "data"
		if par {
			txt, kind = "!"+txt, "parity-error"
		} else {
			good = true
		}
		spans = append(spans, pspan(bx[d.at], bx[d.at+d.w], n, txt, kind, v))
		toks = append(toks, txt)
		units = append(units, v)
	}
	odd := func(from, to int) bool {
		p := 0
		for q := from; q <= to; q++ {
			p ^= bits[q]
		}
		return p != 0
	}
	for i, k := range m {
		if i > 0 {
			if d := ms(k.x0 - m[i-1].x0); d >= 1600 && d <= 2400 {
				c = 0
				spans = append(spans, pspan(k.x0, k.x0, n, "", "sof", 0))
			}
		}
		if c < 0 || c > 58 {
			continue
		}
		w := ms(k.x1 - k.x0)
		b := -1
		switch {
		case w >= 40 && w <= 160:
			b = 0
		case w > 160 && w <= 260:
			b = 1
		}
		if b < 0 {
			spans = append(spans, pspan(k.x0, k.x1, n, "!", "frame-error", 0))
			toks = append(toks, "!")
			c = -1
			continue
		}
		bits[c], bx[c], bx[c+1] = b, k.x0, k.x0+sec/colTimeS
		switch c {
		case 28:
			unit(0, odd(21, 28))
		case 35:
			unit(1, odd(29, 35))
		case 58:
			// the date's fields share the parity at 58
			bad := odd(36, 58)
			for f := 2; f < len(dcf77Fields); f++ {
				unit(f, bad)
			}
		}
		c++
	}
	if len(spans) == 0 {
		return Result{Proto: "dcf77", Error: "no DCF77 minute"}
	}
	res := Result{OK: good, Proto: "dcf77", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, Inverted: !high, SPB: sec / colTimeS, Baud: int(math.Round(1 / sec))}
	if !good {
		res.Error = "no DCF77 field whose parity checks"
	}
	return res
}
