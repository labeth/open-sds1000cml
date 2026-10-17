// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The pulse-coded single-wire protocols (1-Wire, WS281x, DHT/AM230x,
// HDMI-CEC, SAE J1850 VPW) read a line as runs of one level between its
// crossings and classify each run by its length in microseconds. Mirrored
// step for step by decode_pulsewire.js.

// lrun is one run of a level: from crossing x0 to crossing x1 (samples);
// the record's first and last runs are open at the record's ends.
// TRLC-LINKS: REQ-SDS-018
type lrun struct {
	lvl    int
	x0, x1 float64
	open   bool
}

// levelRuns is the sliced line as its runs.
// TRLC-LINKS: REQ-SDS-018
func levelRuns(S sliced) []lrun {
	E := S.edges
	if len(E) == 0 {
		return []lrun{{lvl: logicAt(S, 0), x0: 0, x1: float64(S.n), open: true}}
	}
	first := 1
	if E[0].dir > 0 {
		first = 0
	}
	out := make([]lrun, 0, len(E)+1)
	out = append(out, lrun{lvl: first, x0: 0, x1: E[0].x, open: true})
	for k := 0; k < len(E); k++ {
		lv := 0
		if E[k].dir > 0 {
			lv = 1
		}
		x1, open := float64(S.n), true
		if k+1 < len(E) {
			x1, open = E[k+1].x, false
		}
		out = append(out, lrun{lvl: lv, x0: E[k].x, x1: x1, open: open})
	}
	return out
}

// us is a run's length in microseconds.
// TRLC-LINKS: REQ-SDS-018
func (r lrun) us(colTimeS float64) float64 { return (r.x1 - r.x0) * colTimeS * 1e6 }

// pspan is a span between two sample positions, clamped into the record.
// TRLC-LINKS: REQ-SDS-018
func pspan(x0, x1 float64, n int, text, kind string, val int) Span {
	return Span{ui(x0, n), ui(x1, n), text, kind, val}
}

// crc8Maxim is the 1-Wire ROM CRC-8 (x^8+x^5+x^4+1, reflected 0x8C, from 0).
// TRLC-LINKS: REQ-SDS-018
func crc8Maxim(b []int) int {
	c := 0
	for _, v := range b {
		c ^= v & 0xff
		for i := 0; i < 8; i++ {
			if c&1 != 0 {
				c = c>>1 ^ 0x8C
			} else {
				c >>= 1
			}
		}
	}
	return c
}

// OneWireCfg configures the 1-Wire decode; Overdrive starts in overdrive
// speed (the decode enters it on an Overdrive Skip or Match ROM and leaves
// it on a standard reset).
// TRLC-LINKS: REQ-SDS-018
type OneWireCfg struct {
	Overdrive bool
	Threshold float64
	HaveThr   bool
	Format    string
}

// oneWireROM names the ROM commands.
var oneWireROM = map[int]string{0x33: "RDROM", 0x0F: "RDROM", 0x55: "MATCH", 0xF0: "SEARCH", 0xEC: "ALARM",
	0xCC: "SKIP", 0x3C: "ODSKIP", 0x69: "ODMATCH", 0xA5: "RESUME"}

// owBit is one 1-Wire time slot.
type owBit struct {
	x0, x1 float64
	v      int
}

// DecodeOneWire decodes 1-Wire: reset pulses (low 480 us, overdrive 48 us)
// open a transaction ("sof" RST, then "ack" PRES or "nak" NOPRES for the
// presence pulse), each time slot is a bit (low under 15 us, overdrive 2 us,
// a 1), eight make a byte least significant first. The byte after a reset
// is the ROM command; a Read, Match or Overdrive Match ROM's eight ROM bytes
// end in their CRC-8 (flagged when it does not check), and a Search ROM's
// slots come in threes (the bit, its complement, the direction) whose
// directions make the ROM bytes. OK needs a reset with a presence pulse and
// a byte after it.
// TRLC-LINKS: REQ-SDS-018
func DecodeOneWire(codes []uint8, colTimeS float64, cfg OneWireCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "onewire", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "onewire", Error: "no sample time"}
	}
	runs := levelRuns(S)
	n := S.n
	od := cfg.Overdrive
	var spans []Span
	var toks []string
	var units []int
	var bits []owBit
	good, inTx := false, false
	phase := 0 // 0 the ROM command, 1 ROM bytes (count left in romLeft), 2 search slots, 3 function bytes
	romLeft := 0
	var rom []int
	flushByte := func(bs []owBit, v int, txt, kind string) {
		spans = append(spans, pspan(bs[0].x0, bs[len(bs)-1].x1, n, txt, kind, v))
		toks = append(toks, txt)
		units = append(units, v)
	}
	takeBits := func() {
		for phase != 2 && len(bits) >= 8 {
			b := bits[:8]
			v := 0
			for i, x := range b {
				v |= x.v << i
			}
			bits = bits[8:]
			txt, kind := FmtByte(v, cfg.Format), "data"
			switch phase {
			case 0:
				if name, ok := oneWireROM[v]; ok {
					txt = name
				}
				switch v {
				case 0x33, 0x0F, 0x55, 0x69:
					phase, romLeft, rom = 1, 8, nil
				case 0xF0, 0xEC:
					phase, romLeft = 2, 0
				default:
					phase = 3
				}
				if v == 0x3C || v == 0x69 {
					od = true
				}
				good = good || inTx
			case 1:
				rom = append(rom, v)
				txt = hex2(v)
				if romLeft--; romLeft == 0 {
					if crc8Maxim(rom[:7]) != v {
						txt, kind = "!"+txt, "frame-error"
					}
					phase = 3
				}
			}
			flushByte(b, v, txt, kind)
		}
		for phase == 2 && len(bits) >= 24 {
			// eight bits of the ROM: each the third slot of a triplet
			v := 0
			for i := 0; i < 8; i++ {
				v |= bits[3*i+2].v << i
			}
			flushByte(bits[:24], v, hex2(v), "data")
			bits = bits[24:]
			if romLeft++; romLeft == 8 {
				phase, romLeft = 3, 0
			}
		}
	}
	endTx := func() {
		if len(bits) > 0 {
			spans = append(spans, pspan(bits[0].x0, bits[len(bits)-1].x1, n, "?", "gap", 0))
			toks = append(toks, "?")
		}
		bits = nil
	}
	for k := 0; k < len(runs); k++ {
		r := runs[k]
		if r.lvl != 0 || r.open {
			continue
		}
		w := r.us(colTimeS)
		slotMax, lowr, pdh := 120.0, 15.0, 60.0
		if od {
			slotMax, lowr, pdh = 16.0, 2.0, 6.0
		}
		switch {
		case w >= 440 || (od && w >= 40 && w < 80):
			endTx()
			if w >= 440 {
				od = false
				pdh = 60
			}
			spans = append(spans, pspan(r.x0, r.x1, n, "RST", "sof", 0))
			toks = append(toks, "RST")
			phase, romLeft, inTx = 0, 0, false
			// the presence pulse: a low within pdh after the release
			if k+2 < len(runs) && !runs[k+2].open && runs[k+1].us(colTimeS) <= pdh*1.25 {
				p := runs[k+2]
				spans = append(spans, pspan(p.x0, p.x1, n, "PRES", "ack", 1))
				toks = append(toks, "PRES")
				inTx = true
				k += 2
			} else {
				spans = append(spans, pspan(r.x1, r.x1, n, "NOPRES", "nak", 0))
				toks = append(toks, "NOPRES")
			}
		case w < slotMax*1.25:
			v := 0
			if w < lowr {
				v = 1
			}
			end := r.x0 + slotMax/2*1e-6/colTimeS
			if k+1 < len(runs) {
				end = math.Min(end, runs[k+1].x1)
			}
			end = math.Max(end, r.x1)
			bits = append(bits, owBit{r.x0, end, v})
			takeBits()
		}
	}
	endTx()
	if len(spans) == 0 {
		return Result{Proto: "onewire", Error: "no 1-Wire reset or slot"}
	}
	res := Result{OK: good, Proto: "onewire", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: 1e-6 / colTimeS}
	if od {
		res.Baud = 125000
	} else {
		res.Baud = 16300
	}
	if !good {
		res.Error = "no 1-Wire reset with presence and a byte after it"
	}
	return res
}

// WS281xCfg configures the WS281x LED-strip decode.
// TRLC-LINKS: REQ-SDS-018
type WS281xCfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
}

// DecodeWS281x decodes WS281x LED strips: each bit a high pulse and its low
// rest (800 or 400 kbit/s), a 1 the long high of the two the frame holds
// (one length only: high over half the period), bytes most significant bit
// first in wire order (G, R, B of each LED). A low of 50 us or more
// latches the frame ("rw" RST); each frame opens with a zero-width "sof".
// OK needs a frame of whole LEDs at a steady bit rate.
// TRLC-LINKS: REQ-SDS-018
func DecodeWS281x(codes []uint8, colTimeS float64, cfg WS281xCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "ws281x", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "ws281x", Error: "no sample time"}
	}
	runs := levelRuns(S)
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	var periods []float64
	type wbit struct {
		x0, hi, per float64 // rise, high length, period (0: the frame's last)
	}
	var frame []wbit
	flush := func(latch lrun) {
		if len(frame) == 0 {
			return
		}
		// the long highs from the short: midway when the frame holds both
		lo, hi := math.Inf(1), 0.0
		var pers []float64
		for _, b := range frame {
			lo, hi = math.Min(lo, b.hi), math.Max(hi, b.hi)
			if b.per > 0 {
				pers = append(pers, b.per)
			}
		}
		sort.Float64s(pers)
		med := 0.0
		if len(pers) > 0 {
			med = pers[len(pers)/2]
		}
		steady := len(pers) > 0
		for _, p := range pers {
			steady = steady && p >= 0.75*med && p <= 1.25*med
		}
		spans = append(spans, pspan(frame[0].x0, frame[0].x0, n, "", "sof", 0))
		nb := len(frame) / 8
		for b := 0; b < nb; b++ {
			v := 0
			for i := 0; i < 8; i++ {
				f := frame[8*b+i]
				per := f.per
				if per == 0 {
					per = med
				}
				one := f.hi > 0.5*per
				if hi > 1.5*lo {
					one = f.hi > (lo+hi)/2
				}
				if one {
					v |= 1 << (7 - i)
				}
			}
			end := frame[8*b+7].x0 + med
			txt := FmtByte(v, cfg.Format)
			spans = append(spans, pspan(frame[8*b].x0, end, n, txt, "data", v))
			toks = append(toks, txt)
			units = append(units, v)
		}
		if rest := len(frame) % 8; rest != 0 {
			spans = append(spans, pspan(frame[8*nb].x0, frame[len(frame)-1].x0+med, n, "?", "gap", 0))
			toks = append(toks, "?")
		}
		if !latch.open || latch.x1 > latch.x0 {
			spans = append(spans, pspan(latch.x0, latch.x1, n, "RST", "rw", 0))
			toks = append(toks, "RST")
		}
		if steady && nb >= 3 && nb%3 == 0 && len(frame)%8 == 0 && !latch.open {
			good = true
			periods = append(periods, med)
		}
		frame = nil
	}
	started := false // a frame may start: the record opened low for a latch's time
	for k, r := range runs {
		if r.lvl == 0 {
			if r.us(colTimeS) >= 50 {
				if len(frame) > 0 {
					frame[len(frame)-1].per = 0
					flush(r)
				}
				started = !r.open || r.x0 == 0
			}
			continue
		}
		if !started || r.open || k+1 >= len(runs) {
			continue
		}
		per := 0.0
		if k+2 < len(runs) {
			per = runs[k+2].x0 - r.x0
		}
		if r.us(colTimeS) > 10 { // no bit's high
			frame, started = nil, false
			continue
		}
		frame = append(frame, wbit{r.x0, r.x1 - r.x0, per})
	}
	frame = nil
	if len(spans) == 0 {
		return Result{Proto: "ws281x", Error: "no WS281x frame"}
	}
	res := Result{OK: good, Proto: "ws281x", Spans: spans, Text: strings.Join(toks, " "), Bytes: units, Thr: S.threshold}
	if len(periods) > 0 {
		res.SPB = periods[0]
		res.Baud = snapRate(int(math.Round(1/(periods[0]*colTimeS))), 400e3, 800e3)
	} else {
		res.Error = "no whole WS281x frame"
	}
	return res
}

// DHTCfg configures the DHT11/DHT22/AM230x decode.
// TRLC-LINKS: REQ-SDS-018
type DHTCfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
}

// DecodeDHT decodes the DHT11/DHT22/AM230x sensors' single-wire reading: the
// host's start low (0.5 ms or more, "sof" START), the sensor's 80 us low
// and 80 us high response ("ack" RESP), then 40 bits, each a 50 us low and a
// high of 26-28 us (a 0) or 70 us (a 1), most significant first: humidity
// (2 bytes), temperature (2) and the checksum, the low byte of the sum of
// the four (flagged when it does not check). OK needs a reading whose
// checksum checks.
// TRLC-LINKS: REQ-SDS-018
func DecodeDHT(codes []uint8, colTimeS float64, cfg DHTCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "dht", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "dht", Error: "no sample time"}
	}
	runs := levelRuns(S)
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	in := func(r lrun, lo, hi float64) bool {
		w := r.us(colTimeS)
		return !r.open && w >= lo && w <= hi
	}
	for k := 0; k+4 < len(runs); k++ {
		st := runs[k]
		if st.lvl != 0 || st.open || st.us(colTimeS) < 500 || st.us(colTimeS) > 30000 {
			continue
		}
		// the host's release, the sensor's response low and high
		if !in(runs[k+1], 5, 250) || !in(runs[k+2], 40, 120) || !in(runs[k+3], 40, 120) {
			continue
		}
		spans = append(spans, pspan(st.x0, st.x1, n, "START", "sof", 0))
		spans = append(spans, pspan(runs[k+2].x0, runs[k+3].x1, n, "RESP", "ack", 0))
		toks = append(toks, "START", "RESP")
		j := k + 4
		var vals []int
		var xs [][2]float64
		for len(vals) < 5 && j < len(runs) {
			v, x0, ok := 0, runs[j].x0, true
			for b := 0; b < 8; b++ {
				if j+1 >= len(runs) || !in(runs[j], 25, 90) || runs[j+1].lvl != 1 || !in(runs[j+1], 10, 100) {
					ok = false
					break
				}
				if runs[j+1].us(colTimeS) > 48 {
					v |= 1 << (7 - b)
				}
				j += 2
			}
			if !ok {
				break
			}
			vals = append(vals, v)
			xs = append(xs, [2]float64{x0, runs[j-1].x1})
		}
		for i, v := range vals {
			txt, kind := FmtByte(v, cfg.Format), "data"
			if i == 4 {
				txt = hex2(v)
				if (vals[0]+vals[1]+vals[2]+vals[3])&0xff != v {
					txt, kind = "!"+txt, "frame-error"
				} else {
					good = true
				}
			}
			spans = append(spans, pspan(xs[i][0], xs[i][1], n, txt, kind, v))
			toks = append(toks, txt)
			units = append(units, v)
		}
		if len(vals) < 5 {
			at := runs[min(j, len(runs)-1)].x0
			spans = append(spans, pspan(at, at, n, "?", "gap", 0))
			toks = append(toks, "?")
		}
		k = j - 1
	}
	if len(spans) == 0 {
		return Result{Proto: "dht", Error: "no DHT start and response"}
	}
	res := Result{OK: good, Proto: "dht", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: 1e-6 / colTimeS, Baud: 10000}
	if !good {
		res.Error = "no DHT reading whose checksum checks"
	}
	return res
}

// CECCfg configures the HDMI-CEC decode.
// TRLC-LINKS: REQ-SDS-018
type CECCfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
}

// DecodeCEC decodes HDMI-CEC: a start bit (low 3.7 ms in a 4.5 ms period,
// "sof" START), then blocks of ten bits each in a 2.4 ms period, a 1 a
// 0.6 ms low and a 0 a 1.5 ms low: eight data bits most significant first,
// the end-of-message bit and the acknowledge bit. The first block is the
// header ("4>0": initiator and destination), the next the opcode and
// operands. A directly addressed block is acknowledged by the follower
// holding the bit low ("ack" A, else "nak" N); a broadcast one the other way.
// OK needs a message whose bits all keep their timing.
// TRLC-LINKS: REQ-SDS-018
func DecodeCEC(codes []uint8, colTimeS float64, cfg CECCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "cec", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "cec", Error: "no sample time"}
	}
	runs := levelRuns(S)
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	ms := func(r lrun) float64 { return r.us(colTimeS) / 1000 }
	// bitAt reads the bit whose low is runs[j]: its value and its end (the
	// next fall, or the nominal period at the message's end).
	bitAt := func(j int) (v int, end float64, ok bool) {
		if j+1 >= len(runs) || runs[j].lvl != 0 || runs[j].open {
			return 0, 0, false
		}
		lw := ms(runs[j])
		switch {
		case lw >= 0.4 && lw <= 0.8:
			v = 1
		case lw >= 1.3 && lw <= 1.7:
			v = 0
		default:
			return 0, 0, false
		}
		per := (runs[j+1].x1 - runs[j].x0) * colTimeS * 1000
		if runs[j+1].open || per > 2.75 {
			return v, runs[j].x0 + 2.4e-3/colTimeS, true // the last bit: idle after
		}
		if per < 2.05 {
			return 0, 0, false
		}
		return v, runs[j+1].x1, true
	}
	for k := 0; k+1 < len(runs); k++ {
		st := runs[k]
		if st.lvl != 0 || st.open || ms(st) < 3.5 || ms(st) > 3.9 || runs[k+1].open {
			continue
		}
		if per := ms(st) + ms(runs[k+1]); per < 4.3 || per > 4.7 {
			continue
		}
		spans = append(spans, pspan(st.x0, runs[k+1].x1, n, "START", "sof", 0))
		toks = append(toks, "START")
		j := k + 2
		dest := -1
		blocks, clean := 0, true
		for {
			var bs [10]int
			var ends [10]float64
			x0 := runs[min(j, len(runs)-1)].x0
			got := 0
			for ; got < 10; got++ {
				v, end, bok := bitAt(j)
				if !bok {
					break
				}
				bs[got], ends[got] = v, end
				j += 2
			}
			if got < 10 { // a block cut short, or the message ended with no EOM
				clean = false
				spans = append(spans, pspan(x0, x0, n, "!", "frame-error", 0))
				toks = append(toks, "!")
				break
			}
			v := 0
			for b := 0; b < 8; b++ {
				v = v<<1 | bs[b]
			}
			txt := FmtByte(v, cfg.Format)
			if blocks == 0 {
				dest = v & 0xf
				txt = fmt.Sprintf("%X>%X", v>>4, v&0xf)
			}
			spans = append(spans, pspan(x0, ends[7], n, txt, "data", v))
			toks = append(toks, txt)
			units = append(units, v)
			if bs[8] == 1 {
				spans = append(spans, pspan(ends[7], ends[8], n, "EOM", "rw", 1))
				toks = append(toks, "EOM")
			}
			acked := bs[9] == 0
			if dest == 0xf {
				acked = bs[9] == 1
			}
			ak, at := "ack", "A"
			if !acked {
				ak, at = "nak", "N"
			}
			spans = append(spans, pspan(ends[8], ends[9], n, at, ak, bs[9]))
			toks = append(toks, at)
			blocks++
			if bs[8] == 1 {
				break
			}
		}
		if blocks > 0 && clean {
			good = true
		}
		k = j - 1
	}
	if len(spans) == 0 {
		return Result{Proto: "cec", Error: "no CEC start bit"}
	}
	res := Result{OK: good, Proto: "cec", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: 2.4e-3 / colTimeS, Baud: 417}
	if !good {
		res.Error = "no complete CEC message"
	}
	return res
}

// J1850Cfg configures the SAE J1850 VPW decode.
// TRLC-LINKS: REQ-SDS-018
type J1850Cfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
}

// j1850CRC is the SAE J1850 CRC-8: polynomial 0x1D from all ones, inverted.
// TRLC-LINKS: REQ-SDS-018
func j1850CRC(b []int) int {
	c := 0xff
	for _, v := range b {
		c ^= v & 0xff
		for i := 0; i < 8; i++ {
			if c&0x80 != 0 {
				c = (c<<1 ^ 0x1D) & 0xff
			} else {
				c = c << 1 & 0xff
			}
		}
	}
	return c ^ 0xff
}

// DecodeJ1850 decodes SAE J1850 VPW (10.4 kbit/s): the line idles low
// (passive), a frame opens with a 200 us high (active) start of frame
// ("sof" SOF), then each bit is one run, its value by level and length (a
// short 64 us active or long 128 us passive run a 1, a long active or short
// passive run a 0), bytes most significant first; a passive run of 200 us
// ends the data (EOD), the last byte the CRC-8 (flagged when it does not
// check). An in-frame response after the EOD (a normalization bit, then
// bytes) is read on into the same frame. OK needs a frame whose CRC checks.
// TRLC-LINKS: REQ-SDS-018
func DecodeJ1850(codes []uint8, colTimeS float64, cfg J1850Cfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "j1850", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "j1850", Error: "no sample time"}
	}
	runs := levelRuns(S)
	n := S.n
	var spans []Span
	var toks []string
	var units []int
	good := false
	emit := func(vals []int, xs [][2]float64, crc bool) {
		for i, v := range vals {
			txt, kind := FmtByte(v, cfg.Format), "data"
			if crc && i == len(vals)-1 {
				txt = hex2(v)
				if len(vals) < 2 || j1850CRC(vals[:len(vals)-1]) != v {
					txt, kind = "!"+txt, "frame-error"
				} else {
					good = true
				}
			}
			spans = append(spans, pspan(xs[i][0], xs[i][1], n, txt, kind, v))
			toks = append(toks, txt)
			units = append(units, v)
		}
	}
	for k := 0; k < len(runs); k++ {
		r := runs[k]
		w := r.us(colTimeS)
		if r.lvl != 1 || r.open || w < 163 || w > 239 {
			continue
		}
		spans = append(spans, pspan(r.x0, r.x1, n, "SOF", "sof", 0))
		toks = append(toks, "SOF")
		var vals []int
		var xs [][2]float64
		v, nb := 0, 0
		bx0 := 0.0
		inIFR, ok := false, true
		j := k + 1
		for ; j < len(runs); j++ {
			q := runs[j]
			qw := q.us(colTimeS)
			if q.lvl == 0 && (q.open || qw >= 163) { // EOD / EOF
				if nb != 0 {
					ok = false
				}
				emit(vals, xs, !inIFR)
				vals, xs, v, nb = nil, nil, 0, 0
				if q.open || qw >= 239 || inIFR || j+1 >= len(runs) {
					break
				}
				// an in-frame response: a normalization bit, then bytes
				nbw := runs[j+1].us(colTimeS)
				if runs[j+1].lvl != 1 || runs[j+1].open || nbw < 34 || nbw > 163 {
					break
				}
				spans = append(spans, pspan(q.x0, q.x1, n, "EOD", "rw", 0))
				toks = append(toks, "EOD")
				inIFR = true
				j++
				continue
			}
			if q.open || qw < 34 || qw > 163 {
				ok = false
				break
			}
			long := qw >= 96
			bit := 0
			if (q.lvl == 1) != long {
				bit = 1
			}
			if nb == 0 {
				bx0 = q.x0
			}
			v = v<<1 | bit
			if nb++; nb == 8 {
				vals = append(vals, v)
				xs = append(xs, [2]float64{bx0, q.x1})
				v, nb = 0, 0
			}
		}
		if !ok {
			if len(vals) > 0 {
				emit(vals, xs, false)
			}
			spans = append(spans, pspan(runs[min(j, len(runs)-1)].x0, runs[min(j, len(runs)-1)].x0, n, "!", "frame-error", 0))
			toks = append(toks, "!")
		}
		k = j
	}
	if len(spans) == 0 {
		return Result{Proto: "j1850", Error: "no J1850 start of frame"}
	}
	res := Result{OK: good, Proto: "j1850", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: 64e-6 / colTimeS, Baud: 10400}
	if !good {
		res.Error = "no J1850 frame whose CRC checks"
	}
	return res
}
