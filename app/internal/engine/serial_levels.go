// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"math"
	"slices"
	"sync/atomic"

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
	volts   [2]atomic.Uint64 // each channel's decoder threshold in volts at the BNC (float64 bits)
	have    [2]atomic.Bool
	spiHz   atomic.Uint64 // the clock rate on the armed SPI request's clock line (float64 bits), 0 unknown
	baud    atomic.Uint64 // the bit rate on the armed UART request's line (float64 bits), 0 unknown
	sentNs  atomic.Uint64 // the armed SENT request's tick in ns (float64 bits), 0 unknown
	sentNib atomic.Int32  // the nibbles of its frames (status, data, CRC), 0 unknown
	shiftS  atomic.Uint64 // how far the last match began before the FPGA's trigger, s (float64 bits)
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
		if hz := clockRate(line, f.SampleS); hz > 0 {
			e.serLv.spiHz.Store(math.Float64bits(hz))
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
// that a quarter of them are no longer than (a word's clocks, not the gaps
// between words), from at least eight edges each four or more samples apart.
// 0 when the line shows no clock at this sample interval.
// TRLC-LINKS: REQ-SDS-013
func clockRate(codes []uint8, sampleS float64) float64 {
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
	period := gaps[len(gaps)/4]
	if period < 4 {
		return 0 // the clock is not resolved at this timebase
	}
	return 1 / (float64(period) * sampleS)
}

// serialPlanParams is p as the hardware matcher takes it: without a threshold
// of its own, the learned decoder threshold of its first line at the current
// V/div and offset; for SPI without a clock rate, the measured one. why says
// what is still missing, "" when p is complete.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialPlanParams(p SerialParams) (SerialParams, string) {
	if !p.HaveThr {
		ch := p.ChA & 1
		if !e.serLv.have[ch].Load() {
			return p, fmt.Sprintf("learning the line levels on C%d", ch+1)
		}
		v := math.Float64frombits(e.serLv.volts[ch].Load())
		off := math.Float64frombits(e.trigOffV[ch].Load())
		p.Threshold = math.Round(math.Max(1, math.Min(254, 128+(v+off)*25/e.chVdivV(ch))))
		p.HaveThr = true
	}
	if p.Proto == serSPI && p.SPIClockHz <= 0 {
		hz := math.Float64frombits(e.serLv.spiHz.Load())
		if !(hz > 0) {
			return p, "measuring the SPI clock rate"
		}
		p.SPIClockHz = int(math.Round(hz))
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
	// three (the scope's match editor holds bytes, as its BUS table shows them).
	switch {
	case p.Proto == serMIL1553 && len(p.Bytes) == 2:
		p.Bytes = joinBytes(p.Bytes, 0xffff)
	case p.Proto == serARINC && len(p.Bytes) >= 2 && len(p.Bytes) <= 3:
		p.Bytes = joinBytes(p.Bytes, 0x7ffff)
	}
	return p, ""
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
		p.Bytes, p.Baud, p.TickNs = planned.Bytes, planned.Baud, planned.TickNs
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
	ok, start := serialMatchIn(a[lo:hi], b[lo:hi], f.SampleS, p, anchorSpan{target: at - lo, lo: 0, hi: at - lo})
	if !ok || start < 0 || start > at-lo {
		return -1
	}
	return float64(lo + start)
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
		serUSB: e.hardwareUSBLS, serFlexRay: packet(serFlexRay)}
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
