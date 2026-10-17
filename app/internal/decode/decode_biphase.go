// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The biphase single-wire protocols: DALI (Manchester, 1200 bit/s) and the
// biphase-mark codes of USB Power Delivery (BMC, 300 kbit/s) and S/PDIF.
// Each cuts the line's runs into whole half bits. Mirrored step for step by
// decode_biphase.js.

// DALICfg configures the DALI decode.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
type DALICfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
}

// DecodeDALI decodes DALI (IEC 62386): Manchester bits of 833 us, a 1 the
// line at its active level in the first half and idle in the second (the
// bus idles high), a frame a start bit (1, "sof" START), then 8 (backward,
// an answer "R5A"), 16 (forward: address "A01" and opcode "D90") or 24
// bits most significant first, then idle. The idle level is the level of
// the line's longest run. OK needs a whole frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func DecodeDALI(codes []uint8, colTimeS float64, cfg DALICfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "dali", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "dali", Error: "no sample time"}
	}
	marks, high := irMarks(S, colTimeS)
	n := S.n
	us := func(d float64) float64 { return d * colTimeS * 1e6 }
	const half = 416.67
	hx := half / us(1)
	var spans []Span
	var toks []string
	var units []int
	good := false
	for i := 0; i < len(marks); i++ {
		// a frame opens after the stop bits' idle (2 bits and the settling time)
		if (i > 0 && us(marks[i].x0-marks[i-1].x1) < 1600) || (i == 0 && us(marks[0].x0) < 900) {
			continue
		}
		lv, xs := irHalves(marks, i, half, 2, us)
		if len(lv) == 0 { // a mark of no whole half bit
			continue
		}
		if len(lv)%2 != 0 { // a last 1 bit's idle half runs into the stop bits
			lv = append(lv, 0)
			xs = append(xs, xs[len(xs)-1]+hx)
		}
		var bits []int
		ok := true
		for b := 0; 2*b+1 < len(lv); b++ {
			switch {
			case lv[2*b] == 1 && lv[2*b+1] == 0:
				bits = append(bits, 1)
			case lv[2*b] == 0 && lv[2*b+1] == 1:
				bits = append(bits, 0)
			default:
				ok = false
			}
		}
		spans = append(spans, pspan(xs[0], xs[0]+2*hx, n, "START", "sof", 0))
		toks = append(toks, "START")
		nb := len(bits) - 1
		if !ok || bits[0] != 1 || (nb != 8 && nb != 16 && nb != 24) {
			spans = append(spans, pspan(xs[0], xs[len(xs)-1]+hx, n, fmt.Sprintf("!%dbits", nb), "frame-error", 0))
			toks = append(toks, fmt.Sprintf("!%dbits", nb))
			continue
		}
		for b := 0; b < nb/8; b++ {
			v := 0
			for k := 0; k < 8; k++ {
				v = v<<1 | bits[1+8*b+k]
			}
			txt := hex2(v)
			switch {
			case nb == 8:
				txt = "R" + txt
			case b == 0:
				txt = "A" + txt
			case nb == 16:
				txt = "D" + txt
			}
			x0 := xs[2*(1+8*b)]
			spans = append(spans, pspan(x0, x0+16*hx, n, txt, "data", v))
			toks = append(toks, txt)
			units = append(units, v)
		}
		good = true
	}
	if len(spans) == 0 {
		return Result{Proto: "dali", Error: "no DALI frame"}
	}
	res := Result{OK: good, Proto: "dali", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, Inverted: high, SPB: 2 * hx, Baud: 1200}
	if !good {
		res.Error = "no whole DALI frame"
	}
	return res
}

// USBPDCfg configures the USB Power Delivery decode: Baud 0 is the
// standard 300 kbit/s.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
type USBPDCfg struct {
	Baud      int
	Threshold float64
	HaveThr   bool
	Format    string
}

// pd4b5b decodes the 5-bit line symbols: 0..15 data, 16 error, 17..21
// Sync-1, Sync-2, Sync-3, RST-1, RST-2, 22 EOP.
var pd4b5b = [32]int{16, 16, 16, 16, 16, 16, 19, 20, 16, 1, 4, 5, 16, 22, 6, 7,
	16, 18, 8, 9, 2, 3, 10, 11, 17, 21, 12, 13, 14, 15, 0, 16}

// pdOrderedSets are the start-of-packet ordered sets and their names.
var pdOrderedSets = []struct {
	k    [4]int
	name string
}{
	{[4]int{17, 17, 17, 18}, "SOP"}, {[4]int{17, 17, 19, 19}, "SOP'"}, {[4]int{17, 19, 17, 19}, "SOP\""},
	{[4]int{17, 21, 21, 19}, "SOP'D"}, {[4]int{17, 21, 19, 18}, "SOP\"D"},
	{[4]int{20, 17, 20, 19}, "CRST"}, {[4]int{20, 20, 20, 21}, "HRST"},
}

// pdCtrlNames and pdDataNames name the message types.
var pdCtrlNames = map[int]string{1: "GoodCRC", 2: "GotoMin", 3: "Accept", 4: "Reject", 5: "Ping", 6: "PS_RDY",
	7: "GetSrcCap", 8: "GetSnkCap", 9: "DR_Swap", 10: "PR_Swap", 11: "VCONN_Swap", 12: "Wait", 13: "SoftReset"}
var pdDataNames = map[int]string{1: "SrcCap", 2: "Request", 3: "BIST", 4: "SnkCap", 15: "VDM"}

// crc32IEEE is the IEEE 802.3 CRC-32 (reflected 0xEDB88320, from all ones,
// inverted), as USB PD sends it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func crc32IEEE(b []int) uint32 {
	c := uint32(0xFFFFFFFF)
	for _, v := range b {
		c ^= uint32(v & 0xff)
		for i := 0; i < 8; i++ {
			if c&1 != 0 {
				c = c>>1 ^ 0xEDB88320
			} else {
				c >>= 1
			}
		}
	}
	return c ^ 0xFFFFFFFF
}

// bmcBit is one decoded biphase-mark bit: its value and where it starts.
type bmcBit struct {
	v  int
	x0 float64
}

// bmcBits decodes biphase-mark runs: a run of about a bit time is a 0, two
// of about half one a 1; a run over 1.5 bit times (or any other) ends a burst.
// It returns the bursts.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func bmcBits(S sliced, bit float64) [][]bmcBit {
	var out [][]bmcBit
	var cur []bmcBit
	half := false
	hx := 0.0
	E := S.edges
	for k := 1; k < len(E); k++ {
		g := E[k].x - E[k-1].x
		switch {
		case g > 1.5*bit:
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur, half = nil, false
		case g > 0.75*bit:
			if half { // a lone half bit: coding error, the burst ends
				out = append(out, cur)
				cur, half = nil, false
				continue
			}
			cur = append(cur, bmcBit{0, E[k-1].x})
		default:
			if half {
				cur = append(cur, bmcBit{1, hx})
				half = false
			} else {
				half, hx = true, E[k-1].x
			}
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// DecodeUSBPD decodes USB Power Delivery messages on a CC line: biphase-mark
// bits at 300 kbit/s, the preamble, the start-of-packet ordered set ("sof"
// SOP, SOP', SOP"... or a reset), then 4b5b symbols, two to a byte, low
// nibble first: the 16-bit header (its first byte named by the message type,
// "Request"), the data objects and the CRC-32 (flagged when it does not
// check), then EOP. OK needs a message whose CRC checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func DecodeUSBPD(codes []uint8, colTimeS float64, cfg USBPDCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "usbpd", Error: S.reason}
	}
	baud := cfg.Baud
	if baud <= 0 {
		baud = 300000
	}
	if !(colTimeS > 0) {
		return Result{Proto: "usbpd", Error: "no sample time"}
	}
	bit := 1 / (float64(baud) * colTimeS)
	if !(bit >= 4) {
		return Result{Proto: "usbpd", Error: fmt.Sprintf("%.1f samples/bit; need >= 4", bit)}
	}
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	for _, burst := range bmcBits(S, bit) {
		if len(burst) < 60 {
			continue
		}
		sym := func(i int) int {
			v := 0
			for k := 0; k < 5; k++ {
				v |= burst[i+k].v << k
			}
			return pd4b5b[v]
		}
		at, name := -1, ""
		for i := 0; i+20 <= len(burst) && at < 0; i++ {
			for _, os := range pdOrderedSets {
				hits := 0
				for q := 0; q < 4; q++ {
					if sym(i+5*q) == os.k[q] {
						hits++
					}
				}
				if hits >= 3 {
					at, name = i, os.name
					break
				}
			}
		}
		if at < 0 {
			continue
		}
		end := func(i int) float64 {
			if i < len(burst) {
				return burst[i].x0
			}
			return burst[len(burst)-1].x0 + bit
		}
		spans = append(spans, pspan(burst[at].x0, end(at+20), n, name, "sof", 0))
		toks = append(toks, name)
		if name == "HRST" || name == "CRST" {
			continue
		}
		idx := at + 20
		var vals []int
		var xs []float64
		bad := false
		readByte := func() bool {
			if idx+10 > len(burst) {
				return false
			}
			lo, hi := sym(idx), sym(idx+5)
			if lo > 15 || hi > 15 {
				bad = true
				return false
			}
			vals = append(vals, lo|hi<<4)
			xs = append(xs, burst[idx].x0)
			idx += 10
			return true
		}
		if !readByte() || !readByte() {
			spans = append(spans, pspan(end(idx), end(idx), n, "!", "frame-error", 0))
			toks = append(toks, "!")
			continue
		}
		head := vals[0] | vals[1]<<8
		count := head >> 12 & 7
		ok := true
		for k := 0; k < 4*count+4 && ok; k++ {
			ok = readByte()
		}
		crcOK := ok && !bad
		if crcOK {
			got := uint32(vals[len(vals)-4]) | uint32(vals[len(vals)-3])<<8 | uint32(vals[len(vals)-2])<<16 | uint32(vals[len(vals)-1])<<24
			crcOK = crc32IEEE(vals[:len(vals)-4]) == got
		}
		for k, v := range vals {
			txt, kind := hex2(v), "data"
			if k == 0 {
				names := pdCtrlNames
				if count > 0 {
					names = pdDataNames
				}
				if nm, has := names[head&0xf]; has {
					txt = nm
				}
			}
			if ok && !bad && k >= len(vals)-4 && !crcOK {
				txt, kind = "!"+txt, "frame-error"
			}
			spans = append(spans, pspan(xs[k], end(at+20+10*(k+1)), n, txt, kind, v))
			toks = append(toks, txt)
			units = append(units, v)
		}
		if !ok || bad {
			spans = append(spans, pspan(end(idx), end(idx), n, "!", "frame-error", 0))
			toks = append(toks, "!")
			continue
		}
		if idx+5 <= len(burst) && sym(idx) == 22 {
			spans = append(spans, pspan(burst[idx].x0, end(idx+5), n, "EOP", "rw", 0))
			toks = append(toks, "EOP")
		}
		good = good || crcOK
	}
	if len(spans) == 0 {
		return Result{Proto: "usbpd", Error: "no USB PD start of packet"}
	}
	res := Result{OK: good, Proto: "usbpd", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: bit, Baud: baud}
	if !good {
		res.Error = "no USB PD message whose CRC checks"
	}
	return res
}

// SPDIFCfg configures the S/PDIF decode.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
type SPDIFCfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
}

// spdifHalf finds S/PDIF's half cell, in samples: the mean of the shortest
// cluster of edge gaps (each is one, two or three half cells), 0 when the
// line shows too few edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func spdifHalf(S sliced) float64 {
	var gaps []float64
	for k := 1; k < len(S.edges); k++ {
		gaps = append(gaps, S.edges[k].x-S.edges[k-1].x)
	}
	if len(gaps) < 64 {
		return 0
	}
	sort.Float64s(gaps)
	g0 := gaps[len(gaps)/20]
	sum, cnt := 0.0, 0
	for _, g := range gaps {
		if g >= 0.7*g0 && g <= 1.35*g0 {
			sum += g
			cnt++
		}
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// DecodeSPDIF decodes S/PDIF (IEC 60958): biphase-mark cells at 64 times
// the sample rate, each subframe a preamble that breaks the code (B opens a
// block of 192 frames, "sof" B; M the first channel, W the second, "rw"),
// then 28 cells: the 24-bit sample (auxiliary and audio bits, least
// significant first; a unit, "1A2B3C"), the validity, user, channel status
// and parity bits (even parity over the 28; a sample whose parity fails is
// flagged). OK needs two subframes whose parity checks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func DecodeSPDIF(codes []uint8, colTimeS float64, cfg SPDIFCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "spdif", Error: S.reason}
	}
	h := spdifHalf(S)
	if !(h >= 2) {
		return Result{Proto: "spdif", Error: "no biphase-mark cells (half cell under 2 samples)"}
	}
	E := S.edges
	n := S.n
	// each edge gap in half cells: 1, 2 or 3 (0 none)
	k := make([]int, len(E))
	for i := 1; i < len(E); i++ {
		q := int(math.Round((E[i].x - E[i-1].x) / h))
		if q >= 1 && q <= 3 {
			k[i] = q
		}
	}
	var spans []Span
	var toks []string
	var units []int
	goodN := 0
	for i := 1; i+3 < len(E); i++ {
		if k[i] != 3 {
			continue
		}
		var name string
		switch {
		case k[i+1] == 1 && k[i+2] == 1 && k[i+3] == 3:
			name = "B"
		case k[i+1] == 3 && k[i+2] == 1 && k[i+3] == 1:
			name = "M"
		case k[i+1] == 2 && k[i+2] == 1 && k[i+3] == 2:
			name = "W"
		default:
			continue
		}
		// 28 cells: a gap of two halves a 0, two of one a 1
		j := i + 4
		var bits []int
		for len(bits) < 28 && j < len(E) {
			if k[j] == 2 {
				bits = append(bits, 0)
				j++
			} else if k[j] == 1 && j+1 < len(E) && k[j+1] == 1 {
				bits = append(bits, 1)
				j += 2
			} else {
				break
			}
		}
		x0 := E[i-1].x
		kind := "sof"
		if name != "B" {
			kind = "rw"
		}
		spans = append(spans, pspan(x0, E[i+3].x, n, name, kind, 0))
		toks = append(toks, name)
		if len(bits) < 28 {
			if j < len(E) {
				spans = append(spans, pspan(E[i+3].x, E[i+3].x, n, "!", "frame-error", 0))
				toks = append(toks, "!")
			}
			i = j - 1
			continue
		}
		v, ones := 0, 0
		for b := 0; b < 28; b++ {
			if b < 24 {
				v |= bits[b] << b
			}
			ones += bits[b]
		}
		txt, kd := fmt.Sprintf("%06X", v), "data"
		if ones%2 != 0 {
			txt, kd = "!"+txt, "parity-error"
		} else {
			goodN++
		}
		spans = append(spans, pspan(E[i+3].x, E[j-1].x+2*h, n, txt, kd, v))
		toks = append(toks, txt)
		units = append(units, v)
		i = j - 1
	}
	if len(spans) == 0 {
		return Result{Proto: "spdif", Error: "no S/PDIF preamble"}
	}
	res := Result{OK: goodN >= 2, Proto: "spdif", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: 2 * h}
	if colTimeS > 0 {
		// the cell rate, 64 times a standard sample rate
		res.Baud = snapRate(int(math.Round(1/(2*h*colTimeS))), 64*32e3, 64*44.1e3, 64*48e3, 64*88.2e3, 64*96e3, 64*176.4e3, 64*192e3)
	}
	if goodN < 2 {
		res.Error = "no S/PDIF subframes whose parity checks"
	}
	return res
}
