// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"math"
	"slices"
	"sync/atomic"
	"time"

	"open-sds/app/internal/decode"
)

// The serial trigger is the FPGA's (ADR-SERIAL-HARDWARE-TRIGGER): its
// matcher slices the line at a fixed level, the UART matcher counts bits at a
// fixed rate, and the SPI matcher ends a word after a gap of 1.5 clock
// periods. A request that names none of them (the panel's names no level,
// the web's auto baud no rate) takes them from the signal: each capture's
// decoder threshold, kept in volts so a V/div or offset change re-expresses
// it, the UART's bit rate and the SPI clock rate measured on the line. Software never decides a serial
// trigger; it only places the trigger point on the matched bytes.

// serialLevels is what the captures taught about the lines.
type serialLevels struct {
	volts [2]atomic.Uint64 // each channel's decoder threshold in volts at the BNC (float64 bits)
	have  [2]atomic.Bool
	spiHz atomic.Uint64 // the clock rate on the armed SPI request's clock line (float64 bits), 0 unknown
	// spiQuiet is when the hardware last triggered on the learned SPI clock,
	// or its plan began (unix ns, 0 none); spiProbe asks one untriggered
	// capture to measure the clock again (spiQuietCheck).
	spiQuiet atomic.Int64
	spiProbe atomic.Bool
	sclHz    atomic.Uint64 // the clock rate on the armed I2C request's SCL (float64 bits), 0 unknown
	baud     atomic.Uint64 // the bit rate on the armed UART request's line (float64 bits), 0 unknown
	sentNs   atomic.Uint64 // the armed SENT request's tick in ns (float64 bits), 0 unknown
	sentNib  atomic.Int32  // the nibbles of its frames (status, data, CRC), 0 unknown
	shiftS   atomic.Uint64 // how far the last match began before the FPGA's trigger, s (float64 bits)
}

// learnSerialLevels keeps each channel's decoder threshold from a capture
// taken at scale sc, and the SPI clock rate when an SPI request is armed. A
// level that moved less than two codes is kept, so the plan does not change
// (and re-arm) on every capture's noise.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) learnSerialLevels(f *Frame, sc [2]chScale) {
	if f == nil || f.Valid < 64 {
		return
	}
	for ch := 0; ch < 2; ch++ {
		codes := f.C1
		if ch == 1 {
			codes = f.C2
		}
		if len(codes) < f.Valid || !(sc[ch].vdiv > 0) {
			continue
		}
		thr, ok := decode.AutoThreshold(codes[:f.Valid])
		if !ok {
			continue
		}
		v := (thr-128)*sc[ch].vdiv/25 - sc[ch].off
		if e.serLv.have[ch].Load() {
			old := math.Float64frombits(e.serLv.volts[ch].Load())
			if math.Abs(v-old)*25/sc[ch].vdiv < 2 {
				continue
			}
		}
		e.serLv.volts[ch].Store(math.Float64bits(v))
		e.serLv.have[ch].Store(true)
	}
	if e.serialMode.Load() != SerialTrigger {
		return
	}
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	line := f.C1
	if p.ChA == 1 {
		line = f.C2
	}
	line = line[:min(f.Valid, len(line))]
	switch {
	case p.Proto == serSPI && p.SPIClockHz <= 0:
		if hz := clockRate(line, f.SampleS, f.PeakDetect); hz > 0 {
			e.serLv.spiHz.Store(math.Float64bits(hz))
		}
		if e.serLv.spiProbe.Swap(false) && e.clk.Now != nil {
			e.serLv.spiQuiet.Store(e.clk.Now().UnixNano())
		}
	case p.Proto == serI2C && p.I2CClockHz <= 0:
		if hz := clockRate(line, f.SampleS, f.PeakDetect); hz > 0 {
			e.serLv.sclHz.Store(math.Float64bits(hz))
		}
	case p.Proto == serUART && p.Baud <= 0:
		if b := uartBaud(line, f.SampleS); b > 0 {
			e.serLv.baud.Store(math.Float64bits(b))
		}
	case p.Proto == serSENT && (p.TickNs <= 0 || p.Nibbles <= 0):
		if tick, nib := sentShape(line, f.SampleS); tick > 0 {
			e.serLv.sentNs.Store(math.Float64bits(tick))
			e.serLv.sentNib.Store(int32(nib))
		}
	}
}

// sentShape measures a SENT line: its tick in ns and the nibbles a frame
// holds (status, data and CRC). Each pulse runs falling edge to falling edge;
// a SYNC is 56 ticks and a nibble 12 to 27, so a pulse followed by one in the
// nibble range at a 56th of its length is a SYNC. The tick is the SYNCs'
// median 56th, the nibbles the run of nibble pulses after a SYNC most often
// seen (a pause pulse or the next SYNC ends it). 0, 0 when the line shows no
// two frames.
// TRLC-LINKS: REQ-SDS-013
func sentShape(codes []uint8, sampleS float64) (tickNs float64, nibbles int) {
	thr, ok := decode.AutoThreshold(codes)
	if !ok || !(sampleS > 0) || len(codes) == 0 {
		return 0, 0
	}
	var falls []int
	high := float64(codes[0]) >= thr
	for i, c := range codes {
		now := float64(c) >= thr
		if high && !now {
			falls = append(falls, i)
		}
		high = now
	}
	if len(falls) < 4 {
		return 0, 0
	}
	period := make([]float64, len(falls)-1)
	for i := range period {
		period[i] = float64(falls[i+1] - falls[i])
	}
	nibble := func(p, tick float64) bool { r := p / tick; return r >= 12*0.8 && r <= 27*1.2 }
	var ticks []float64
	count := map[int]int{}
	for k := 0; k+1 < len(period); k++ {
		tick := period[k] / 56
		if !nibble(period[k+1], tick) {
			continue
		}
		n := 0
		for k+1+n < len(period) && nibble(period[k+1+n], tick) && n < 64 {
			n++
		}
		if k+1+n < len(period) { // the frame ended inside the capture
			ticks = append(ticks, tick)
			count[n]++
		}
		k += n
	}
	if len(ticks) < 2 {
		return 0, 0
	}
	for n, v := range count {
		if v > count[nibbles] || (v == count[nibbles] && n > nibbles) {
			nibbles = n
		}
	}
	slices.Sort(ticks)
	return ticks[len(ticks)/2] * sampleS * 1e9, nibbles
}

// standardBauds are the rates a measured UART line snaps to.
var standardBauds = []float64{1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600, 1e6, 2e6, 3e6}

// uartBaud measures a UART line's bit rate: the shortest common run between
// edges is one bit (the median of the shortest fifth of the runs, from at
// least sixteen edges), snapped to a standard rate within 3 %. 0 when the
// line's bits are not resolved at this sample interval (under four samples).
// TRLC-LINKS: REQ-SDS-013
func uartBaud(codes []uint8, sampleS float64) float64 {
	thr, ok := decode.AutoThreshold(codes)
	if !ok || !(sampleS > 0) || len(codes) == 0 {
		return 0
	}
	var runs []int
	high, last := float64(codes[0]) >= thr, 0
	for i, c := range codes {
		if now := float64(c) >= thr; now != high {
			if last > 0 {
				runs = append(runs, i-last)
			}
			high, last = now, i
		}
	}
	if len(runs) < 16 {
		return 0
	}
	slices.Sort(runs)
	short := runs[:max(1, len(runs)/5)]
	bit := float64(short[len(short)/2])
	if bit < 4 {
		return 0
	}
	b := 1 / (bit * sampleS)
	for _, s := range standardBauds {
		if math.Abs(b-s) <= 0.03*s {
			return s
		}
	}
	return math.Round(b)
}

// clockRate measures a clock line's rate: the interval between rising edges
// near the one a quarter of them are no longer than (a word's clocks, not the
// gaps between words), from at least eight edges each four or more samples
// apart; extremes says the codes are bucket extremes (peak detect). 0 when
// the line shows no clock at this sample interval.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func clockRate(codes []uint8, sampleS float64, extremes bool) float64 {
	thr, ok := decode.AutoThreshold(codes)
	if !ok || !(sampleS > 0) {
		return 0
	}
	var rises []int
	high := len(codes) > 0 && float64(codes[0]) >= thr
	for i, c := range codes {
		now := float64(c) >= thr
		if now && !high {
			rises = append(rises, i)
		}
		high = now
	}
	if len(rises) < 8 {
		return 0
	}
	gaps := make([]int, len(rises)-1)
	for i := range gaps {
		gaps[i] = rises[i+1] - rises[i]
	}
	slices.Sort(gaps)
	// The mean of the gaps near the lower quartile's: a clock of a
	// non-integer number of samples (104 MHz: 4.8 at 2 ns) alternates.
	seed := gaps[len(gaps)/4]
	sum, n := 0, 0
	for _, g := range gaps {
		if 2*g >= seed && 2*g <= 3*seed {
			sum += g
			n++
		}
	}
	period := float64(sum) / float64(n)
	// The clock is not resolved at this timebase under four samples a period,
	// nor in bucket extremes (peak detect) under four buckets: a clock faster
	// than the buckets alternates their minima and maxima at a rate of the
	// buckets' own (a 25 MHz clock in 32 ns buckets read as 15.6 MHz).
	if period < 4 || (extremes && period < 8) {
		return 0
	}
	return 1 / (period * sampleS)
}

// learnedCode is channel ch's learned decoder threshold as a code at its
// current V/div and offset.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) learnedCode(ch int) float64 {
	v := math.Float64frombits(e.serLv.volts[ch].Load())
	off := math.Float64frombits(e.trigOffV[ch].Load())
	return math.Round(math.Max(1, math.Min(254, 128+(v+off)*25/e.chVdivV(ch))))
}

// spiMeasuring is serialPlanParams' reason while the SPI clock is measured.
const spiMeasuring = "measuring the SPI clock rate"

// spiQuietNs is how long a learned SPI clock may go without a hardware
// trigger before one capture measures it again.
const spiQuietNs = int64(2 * time.Second)

// spiQuietCheck: a learned SPI clock the hardware has not triggered on for
// spiQuietNs is measured again from one untriggered capture. The bus may
// have slowed: a gap planned for the faster clock splits every byte, nothing
// triggers, and no triggered capture would teach the new rate.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func (e *Engine) spiQuietCheck(p SerialParams) {
	if p.Proto != serSPI || p.SPIClockHz > 0 || !(math.Float64frombits(e.serLv.spiHz.Load()) > 0) || e.clk.Now == nil {
		e.serLv.spiQuiet.Store(0)
		return
	}
	now := e.clk.Now().UnixNano()
	switch at := e.serLv.spiQuiet.Load(); {
	case at == 0:
		e.serLv.spiQuiet.Store(now)
	case now-at > spiQuietNs:
		e.serLv.spiProbe.Store(true)
	}
}

// serialPlanParams is p as the hardware matcher takes it: without a threshold
// of its own, the learned decoder threshold of its first line at the current
// V/div and offset, and of SPI's and I2C's second line at its own (the
// channels may sit apart on the screen: one level for both slices one of
// them off its swing, and the matcher never fires); for SPI without a clock
// rate, the measured one. why says what is still missing, "" when p is
// complete.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialPlanParams(p SerialParams) (SerialParams, string) {
	twoLines := p.Proto == serSPI || p.Proto == serI2C || clockedTwoLine(p.Proto)
	if !p.HaveThr {
		lines := []int{p.ChA & 1}
		if twoLines && !p.HaveThrB {
			lines = append(lines, 1-p.ChA&1)
		}
		for _, ch := range lines {
			if !e.serLv.have[ch].Load() {
				return p, fmt.Sprintf("learning the line levels on C%d", ch+1)
			}
		}
		p.Threshold, p.HaveThr = e.learnedCode(lines[0]), true
		if len(lines) == 2 {
			p.ThresholdB, p.HaveThrB = e.learnedCode(lines[1]), true
		}
	}
	if twoLines && !p.HaveThrB {
		p.ThresholdB, p.HaveThrB = p.Threshold, true
	}
	if p.Proto == serSPI && p.SPIClockHz <= 0 {
		hz := math.Float64frombits(e.serLv.spiHz.Load())
		if !(hz > 0) || e.serLv.spiProbe.Load() {
			return p, spiMeasuring
		}
		p.SPIClockHz = int(math.Round(hz))
	}
	if p.Proto == serI2C && p.I2CClockHz <= 0 { // measured on SCL; until then the general image's receiver
		if hz := math.Float64frombits(e.serLv.sclHz.Load()); hz > 0 {
			p.I2CClockHz = int(math.Round(hz))
		}
	}
	if p.Proto == serUART && p.Baud <= 0 { // a decoder's auto baud (the web's)
		b := math.Float64frombits(e.serLv.baud.Load())
		if !(b > 0) {
			return p, "measuring the UART bit rate"
		}
		p.Baud = int(b)
	}
	if p.Proto == serSENT && (p.TickNs <= 0 || p.Nibbles <= 0) { // the decoder's own: from the SYNC
		tick := math.Float64frombits(e.serLv.sentNs.Load())
		if !(tick > 0) {
			return p, "measuring the SENT tick"
		}
		if p.TickNs <= 0 {
			p.TickNs = tick
		}
		if p.Nibbles <= 0 {
			p.Nibbles = int(e.serLv.sentNib.Load()) // 0: the standard eight
		}
	}
	// A MIL-1553 word typed as two bytes, an ARINC 429 data field as up to
	// three (the scope's match editor holds bytes, as its BUS table shows them);
	// a sequence of words as their bytes in turn (two each, three each).
	switch {
	case p.Proto == serMIL1553 && len(p.Bytes) == 2:
		p.Bytes = joinBytes(p.Bytes, 0xffff)
	case p.Proto == serMIL1553 && len(p.Bytes) > 2 && len(p.Bytes)%2 == 0:
		p.Bytes = joinGroups(p.Bytes, 2, 0xffff)
	case p.Proto == serARINC && len(p.Bytes) >= 2 && len(p.Bytes) <= 3:
		p.Bytes = joinBytes(p.Bytes, 0x7ffff)
	case p.Proto == serARINC && len(p.Bytes) > 3 && len(p.Bytes)%3 == 0:
		p.Bytes = joinGroups(p.Bytes, 3, 0x7ffff)
	case (p.Proto == serMDIO || p.Proto == serWiegand) && len(p.Bytes) >= 2 && len(p.Bytes)%2 == 0:
		p.Bytes = joinGroups(p.Bytes, 2, 0xffff) // MDIO's header and data, Wiegand's fields: 16 bits each
	case p.Proto == serSPDIF && len(p.Bytes) >= 3 && len(p.Bytes)%3 == 0:
		p.Bytes = joinGroups(p.Bytes, 3, 0xffffff) // an S/PDIF sample: 24 bits
	case p.Proto == serMIL1553 && len(p.Pattern) >= 2:
		p.Pattern = groupPattern(p.Pattern, 2, 0xffff)
	case p.Proto == serARINC && len(p.Pattern) >= 3:
		p.Pattern = groupPattern(p.Pattern, 3, 0x7ffff)
	}
	// Each ARINC 429 word is a frame of its own, and a sequence counts within
	// one frame: two words never match. Said, rather than armed in vain.
	if p.Proto == serARINC && len(patternOf(p)) > 1 {
		return p, arincOneWord
	}
	return p, ""
}

// arincOneWord refuses an ARINC 429 match of more than one word: the
// matcher is not armed for it.
const arincOneWord = "an ARINC 429 match is one word (00 AB CD: its data field)"

// groupPattern is a byte pattern as units of n bytes each (the words of
// MIL-1553 and ARINC 429 the matcher takes): a unit of n data bytes one
// data element, values and masks big-endian within limit; n errors one error
// (the BUS table writes a bad word as ERR for each of its bytes). The
// pattern unchanged when a unit mixes them or does not fit.
// TRLC-LINKS: REQ-SDS-013
func groupPattern(pat []PatternElem, n, limit int) []PatternElem {
	if len(pat)%n != 0 {
		return pat
	}
	var out []PatternElem
	for i := 0; i < len(pat); i += n {
		g := pat[i : i+n]
		switch {
		case allKind(g, patternError):
			out = append(out, PatternElem{Kind: patternError})
		case allKind(g, patternData):
			v, m := 0, 0
			for _, e := range g {
				if e.Value < 0 || e.Value > 0xff || e.Mask < 0 || e.Mask > 0xff {
					return pat
				}
				v, m = v<<8|e.Value, m<<8|e.Mask
			}
			if v&^limit != 0 {
				return pat
			}
			out = append(out, PatternElem{Kind: patternData, Value: v, Mask: m & limit})
		default:
			return pat
		}
	}
	return out
}

// allKind reports whether every element of g is of kind k.
// TRLC-LINKS: REQ-SDS-013
func allKind(g []PatternElem, k int) bool {
	for _, e := range g {
		if e.Kind != k {
			return false
		}
	}
	return true
}

// joinGroups is bytes as big-endian values of n bytes each (joinBytes of
// each group); bytes unchanged when a group does not join.
// TRLC-LINKS: REQ-SDS-013
func joinGroups(bytes []int, n, limit int) []int {
	var out []int
	for i := 0; i+n <= len(bytes); i += n {
		v := joinBytes(bytes[i:i+n], limit)
		if len(v) != 1 {
			return bytes
		}
		out = append(out, v[0])
	}
	return out
}

// joinBytes is bytes as one big-endian value when every one is a byte and the
// value fits limit; bytes unchanged otherwise.
// TRLC-LINKS: REQ-SDS-013
func joinBytes(bytes []int, limit int) []int {
	v := 0
	for _, b := range bytes {
		if b < 0 || b > 0xff {
			return bytes
		}
		v = v<<8 | b
	}
	if v > limit {
		return bytes
	}
	return []int{v}
}

// serialMatchStart is where the match the FPGA triggered on began: decoding
// the request's length of the lines before the hardware trigger point hw
// (where the match ended), the start of the last match at or before it. -1
// when the decode does not find one there; the trigger point then stays the
// hardware's.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialMatchStart(f *Frame, hw float64) float64 {
	if !(hw >= 0) || hw >= float64(f.Valid) || !(f.SampleS > 0) || (f.PeakDetect && !f.Ordered) {
		return -1
	}
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	// The match and rate as the matcher took them: its units (a MIL-1553
	// word, an ARINC 429 field) are the decoded spans' values. The decoders
	// keep their own thresholds.
	if planned, why := e.serialPlanParams(p); why == "" {
		p.Bytes, p.Pattern, p.Baud, p.TickNs = planned.Bytes, planned.Pattern, planned.Baud, planned.TickNs
	}
	a, b := f.C1, f.C1
	if p.ChA == 1 {
		a = f.C2
	}
	if p.ChB == 1 {
		b = f.C2
	}
	if len(a) < f.Valid || len(b) < f.Valid {
		return -1
	}
	at := int(hw)
	lo, hi := 0, f.Valid
	if unit := serialUnitSamples(p, f.SampleS, math.Float64frombits(e.serLv.spiHz.Load())); unit > 0 {
		n := max(1, len(patternOf(p)))
		lo = max(0, at-(n+2)*unit)
		hi = min(f.Valid, at+2*unit)
	}
	if pat := patternOf(p); len(pat) == 2 && pat[0].Kind == patternStart && pat[1].Kind == patternData && pat[1].Mask == 0 {
		// the whole frame around it: a decoder drops a frame cut short (a CAN
		// frame read to its first data byte fails its CRC)
		if unit := serialUnitSamples(p, f.SampleS, math.Float64frombits(e.serLv.spiHz.Load())); unit > 0 {
			lo, hi = max(0, at-frameUnitsBefore*unit), min(f.Valid, at+frameUnitsAfter*unit)
		}
		return frameUnitStart(a[lo:hi], b[lo:hi], f.SampleS, p, at-lo, lo)
	}
	ok, start := serialMatchIn(a[lo:hi], b[lo:hi], f.SampleS, p, anchorSpan{target: at - lo, lo: 0, hi: at - lo})
	if !ok || start < 0 || start > at-lo {
		return -1
	}
	return float64(lo + start)
}

// frameStartLeadUnits is the record a frame-start match keeps before the
// FPGA's trigger until one has been placed (and how far it moved is known):
// the frame's opening and the units the image fires after it - a SENT
// frame's SYNC and first nibble (the raw record at 200us/div held 0.2 ms
// before the trigger, the SYNC began right at its start, and no frame start
// was found); the line image fires on a Manchester frame's fourth word.
const frameStartLeadUnits = 4

// matchLeadS is the record a match keeps before the FPGA's trigger, in
// seconds, until a placement has been learned: frameStartLeadUnits for a
// frame start, else the matched units, which end at the trigger (an ARINC
// 429 word of 320 us did not fit the 0.2 ms before it at 200us/div, and the
// trigger stayed at the word's end, off its start). 0 when a unit's length
// is not known (I2C).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func (e *Engine) matchLeadS(sampleS float64) float64 {
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	if planned, why := e.serialPlanParams(p); why == "" {
		p = planned
	}
	if !(sampleS > 0) {
		return 0
	}
	pat := patternOf(p)
	units := len(pat)
	if len(pat) == 2 && pat[0].Kind == patternStart && pat[1].Kind == patternData && pat[1].Mask == 0 {
		units = frameStartLeadUnits
	}
	return float64(units*serialUnitSamples(p, sampleS, math.Float64frombits(e.serLv.spiHz.Load()))) * sampleS
}

// frameUnitsBefore and frameUnitsAfter bound the record a frame-start match
// is placed on, in units around the FPGA's trigger: the frame's opening (a
// CAN identifier and control field, two units and a half) before it, the
// rest of the longest frame (FlexRay's 254 bytes) after it.
const (
	frameUnitsBefore = 8
	frameUnitsAfter  = 264
)

// frameUnitStart is where a frame-start match (a frame's start, then any
// unit) began: the matcher fires as the frame's first unit ends, so the
// decoded unit ending nearest the FPGA's trigger at, and the frame's opening
// right before it (its start of a decoder that marks none: SPI's idle gap).
// -1 when no unit ends near it.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func frameUnitStart(a, b []uint8, sampleS float64, p SerialParams, at, lo int) float64 {
	d, ok := serialDecoder(p.Proto)
	if !ok {
		return -1
	}
	if at := frameOpening(d.Decode(a, b, sampleS, p).Spans, at); at >= 0 {
		return float64(lo + at)
	}
	return -1
}

// frameOpening is frameUnitStart on a decode's spans: the start of the frame
// whose first unit ends nearest at, -1 for none.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func frameOpening(spans []decode.Span, at int) int {
	best, bestD := -1, math.MaxInt
	for k, sp := range spans {
		if sp.Kind != "data" || sp.I0 > at {
			continue
		}
		dd := at - sp.I1
		if dd < 0 {
			dd = -dd
		}
		if dd < bestD && dd <= max(1, sp.I1-sp.I0) {
			best, bestD = k, dd
		}
	}
	if best < 0 {
		return -1
	}
	// the frame opens before its first unit: its SYNC, SOF or START when the
	// decoder marks one there, past the frame's own fields (a CAN identifier)
	// and its units before the one the FPGA fired on - the line and packet
	// images fire a few units into the frame (a Manchester or USB frame's
	// fourth), as they match buffered units. Units back to back belong to
	// one frame; a unit with no opening marked before it opens itself.
	prev := spans[best]
	for k := best - 1; k >= 0; k-- {
		switch sp := spans[k]; sp.Kind {
		case "start", "sof", "sync":
			return sp.I0
		case "data":
			if prev.I0-sp.I1 > max(1, prev.I1-prev.I0) {
				return spans[best].I0
			}
			prev = sp
		case "gap", "stop", "frame-error", "parity-error", "crc":
			return spans[best].I0
		}
	}
	return spans[best].I0
}

// serialUnitSamples is one decoded unit's length in samples (a UART
// character, eight SPI clocks, a Manchester word, a MIL-1553 word, an ARINC
// 429 word with its gap, a CAN, FlexRay or USB byte with its stuffing or
// framing, SENT's longest nibble), 0 when it is not known (I2C's clock): a
// match is then sought over the whole capture.
// TRLC-LINKS: REQ-SDS-013
func serialUnitSamples(p SerialParams, sampleS, spiHz float64) int {
	if !(sampleS > 0) {
		return 0
	}
	bits := 0.0 // a unit's bits at the request's rate
	switch p.Proto {
	case serUART:
		bits = 10 // start, 8 data, stop
		if p.Bits > 0 {
			bits = float64(p.Bits) + 2
		}
		if p.Parity != "" && p.Parity != "none" {
			bits++
		}
	case serSPI:
		hz := float64(p.SPIClockHz)
		if !(hz > 0) {
			hz = spiHz
		}
		if hz > 0 {
			return int(math.Ceil(8 / hz / sampleS))
		}
		return 0
	case serManchester:
		bits = 8
		if p.Bits > 0 {
			bits = float64(p.Bits)
		}
	case serMIL1553:
		bits = 20 // sync, 16 data, parity
	case serARINC:
		bits = 36 // 32 and the 4-bit gap
	case serCAN, serFlexRay, serUSB:
		bits = 10
	case serLIN, serMIDI:
		bits = 10
	case serDMX, serModbus:
		bits = 11
	case serOneWire, serWS281x, serDHT, serJ1850, serIRNEC, serIRRC6, serDALI, serMDIO:
		bits = 8 // a byte (RC-6 and DALI, MDIO: their bit times at Baud)
	case serIRSIRC, serIRRC5:
		bits = 8 // the longest field: SIRC's address, RC-5's command
	case serCEC:
		bits = 10 // a block
	case serUSBPD:
		bits = 10 // a byte: two 4b5b symbols
	case serSPDIF:
		bits = 32 // a subframe
	case serPS2:
		bits = 11
	case serSWD:
		bits = 46 // request, turnaround, ACK and a data word
	case serWiegand:
		bits = 16
	case serSENT:
		if p.TickNs > 0 {
			return int(math.Ceil(27 * p.TickNs * 1e-9 / sampleS))
		}
		return 0
	}
	if !(bits > 0) || p.Baud <= 0 {
		return 0
	}
	return int(math.Ceil(bits / float64(p.Baud) / sampleS))
}

// serialPreRoom is the record the live window holds before the hardware
// serial trigger beyond the screen: software moves the trigger point back to
// the match's start, within the match and two units more (serialMatchStart).
// A quarter screen when the unit is not known, two screens at most.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialPreRoom(sampleS float64, screen int) int {
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	if planned, why := e.serialPlanParams(p); why == "" {
		p = planned
	}
	unit := serialUnitSamples(p, sampleS, math.Float64frombits(e.serLv.spiHz.Load()))
	if unit <= 0 {
		return screen / 4
	}
	return min(2*screen, max(screen/16, (max(1, len(patternOf(p)))+2)*unit))
}

// serialNoHardware says why an armed serial request has no matcher in the
// loaded image.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialNoHardware() string {
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	if p.empty() {
		return "no bus to match"
	}
	planned, why := e.serialPlanParams(p)
	if why != "" {
		return why
	}
	if want := protocolImage(planned); want != e.protocolImage && e.images != nil && e.hasImage(want) {
		if want == "" {
			want = "general"
		}
		return "loading the " + want + " FPGA image"
	}
	packet := func(proto int) bool { return e.hardwarePacket&(1<<proto) != 0 }
	have := map[int]bool{serUART: e.hardwareUART, serI2C: e.hardwareI2C, serSPI: e.hardwareSPI, serManchester: e.hardwareManchester,
		serSENT: e.hardwareSENT, serCAN: packet(serCAN), serMIL1553: e.hardwareMIL1553, serARINC: packet(serARINC),
		serUSB: e.hardwareUSBLS, serFlexRay: packet(serFlexRay), serMIDI: e.hardwareUART}
	for proto := serLIN; proto <= serWiegand; proto++ {
		if symbolProtocol(proto) {
			have[proto] = e.hardwareSymbol
		}
	}
	if known, ok := have[p.Proto]; ok && !known {
		return "this FPGA image has no matcher for the bus"
	}
	if p.Proto == serARINC && e.arincAux.Load() == 0 {
		return "learning the ARINC 429 levels"
	}
	if n := len(patternOf(p)); n > 32 {
		return fmt.Sprintf("a match of %d units is over the 32 the matcher holds", n)
	}
	if sequenceNeeded(p) && !e.hardwareSequence {
		return "this FPGA image has no sequence trigger for a match this long or with wildcards"
	}
	return "the matcher cannot take this match"
}
