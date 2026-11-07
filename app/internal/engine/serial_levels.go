// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"fmt"
	"math"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// The serial trigger is the FPGA's (ADR-SERIAL-HARDWARE-TRIGGER): its
// matcher slices the line at a fixed level, the UART matcher counts bits at a
// fixed rate, and the SPI matcher ends a word after a gap of 1.5 clock
// periods. A request that names none of them (the panel's names no level,
// the web's auto baud no rate) takes them from the signal: each capture's
// decoder threshold, kept in volts so a V/div or offset change re-expresses
// it, the UART's bit rate and the SPI clock rate measured on the line. Software never decides a serial
// trigger; it only places the trigger point on the matched bytes.

// learnSerialLevelsOf is learnSerialLevels with the channels' thresholds
// taken from lv when it is given.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) learnSerialLevelsOf(f *sample.Frame, sc [2]core.ChScale, lv *core.FrameLevels) {
	if f == nil || f.Len() < 64 {
		return
	}
	for ch := range 2 {
		codes := f.Codes8(0)
		if ch == 1 {
			codes = f.Codes8(1)
		}
		if len(codes) < f.Len() || !(sc[ch].Vdiv > 0) {
			continue
		}
		var thr float64
		var ok bool
		if lv != nil {
			lv.Of(ch, codes[:f.Len()])
			thr, ok = lv.Thr[ch], lv.ThrOK[ch]
		} else {
			thr, ok = decode.AutoThreshold(codes[:f.Len()])
		}
		if !ok {
			continue
		}
		v := (thr-128)*sc[ch].Vdiv/25 - sc[ch].Off
		if e.serLv.Have[ch].Load() {
			old := math.Float64frombits(e.serLv.Volts[ch].Load())
			if math.Abs(v-old)*25/sc[ch].Vdiv < 2 {
				continue
			}
		}
		e.serLv.Volts[ch].Store(math.Float64bits(v))
		e.serLv.Have[ch].Store(true)
	}
	if e.serialMode.Load() != SerialTrigger {
		return
	}
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	line := f.Codes8(0)
	if p.ChA == 1 {
		line = f.Codes8(1)
	}
	line = line[:min(f.Len(), len(line))]
	switch {
	case p.Proto == core.SerSPI && p.SPIClockHz <= 0:
		if hz := core.ClockRate(line, f.SampleS, f.Content == sample.PeakPairs); hz > 0 && core.ResolvesClock(math.Float64frombits(e.serLv.SPIHz.Load()), f) {
			e.serLv.SPIHz.Store(math.Float64bits(hz))
		}
		if e.serLv.SPIProbe.Swap(false) && e.clk.Now != nil {
			e.serLv.SPIQuiet.Store(e.clk.Now().UnixNano())
		}
	case p.Proto == core.SerI2C && p.I2CClockHz <= 0:
		if hz := core.ClockRate(line, f.SampleS, f.Content == sample.PeakPairs); hz > 0 && core.ResolvesClock(math.Float64frombits(e.serLv.SCLHz.Load()), f) {
			e.serLv.SCLHz.Store(math.Float64bits(hz))
		}
	case p.Proto == core.SerUART && p.Baud <= 0:
		if b := core.UARTBaud(line, f.SampleS); b > 0 {
			e.serLv.Baud.Store(math.Float64bits(b))
		}
	case p.Proto == core.SerSENT && (p.TickNs <= 0 || p.Nibbles <= 0):
		if tick, nib := core.SentShape(line, f.SampleS); tick > 0 {
			e.serLv.SentNs.Store(math.Float64bits(tick))
			e.serLv.SentNib.Store(int32(nib)) //nolint:gosec // G115: a SENT frame has a few nibbles
		}
	}
}

// learnedCode is channel ch's learned decoder threshold as a code at its
// current V/div and offset.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) learnedCode(ch int) float64 {
	v := math.Float64frombits(e.serLv.Volts[ch].Load())
	off := math.Float64frombits(e.trigOffV[ch].Load())
	return math.Round(math.Max(1, math.Min(254, 128+(v+off)*25/e.chVdivV(ch))))
}

// spiQuietCheck: a learned SPI clock the hardware has not triggered on for
// spiQuietNs is measured again from one untriggered capture. The bus may
// have slowed: a gap planned for the faster clock splits every byte, nothing
// triggers, and no triggered capture would teach the new rate.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func (e *Engine) spiQuietCheck(p SerialParams) {
	if p.Proto != core.SerSPI || p.SPIClockHz > 0 || !(math.Float64frombits(e.serLv.SPIHz.Load()) > 0) || e.clk.Now == nil {
		e.serLv.SPIQuiet.Store(0)
		return
	}
	now := e.clk.Now().UnixNano()
	switch at := e.serLv.SPIQuiet.Load(); {
	case at == 0:
		e.serLv.SPIQuiet.Store(now)
	case now-at > core.SPIQuietNs:
		e.serLv.SPIProbe.Store(true)
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
	p, why := e.planThresholds(p)
	if why != "" {
		return p, why
	}
	if p, why = e.planMeasuredRates(p); why != "" {
		return p, why
	}
	p = core.JoinUnits(p)
	// Each ARINC 429 word is a frame of its own, and a sequence counts within
	// one frame: two words never match. Said, rather than armed in vain.
	if p.Proto == core.SerARINC && len(core.PatternOf(p)) > 1 {
		return p, core.ArincOneWord
	}
	return p, ""
}

// planThresholds fills in p's missing line thresholds: the learned decoder
// threshold of each line at the current V/div and offset. why says which
// line's levels are still being learned.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) planThresholds(p SerialParams) (SerialParams, string) {
	twoLines := p.Proto == core.SerSPI || p.Proto == core.SerI2C || core.ClockedTwoLine(p.Proto)
	if !p.HaveThr {
		lines := []int{p.ChA & 1}
		if twoLines && !p.HaveThrB {
			lines = append(lines, 1-p.ChA&1)
		}
		for _, ch := range lines {
			if !e.serLv.Have[ch].Load() {
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
	return p, ""
}

// planMeasuredRates fills in the rates p leaves to the decoder's own
// measurement (SPI's and I2C's clock, UART's bit rate, SENT's tick). why
// says which is still being measured.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) planMeasuredRates(p SerialParams) (SerialParams, string) {
	if p.Proto == core.SerSPI && p.SPIClockHz <= 0 {
		hz := math.Float64frombits(e.serLv.SPIHz.Load())
		if !(hz > 0) || e.serLv.SPIProbe.Load() {
			return p, core.SPIMeasuring
		}
		p.SPIClockHz = int(math.Round(hz))
	}
	if p.Proto == core.SerI2C && p.I2CClockHz <= 0 { // measured on SCL; until then the general image's receiver
		if hz := math.Float64frombits(e.serLv.SCLHz.Load()); hz > 0 {
			p.I2CClockHz = int(math.Round(hz))
		}
	}
	if p.Proto == core.SerUART && p.Baud <= 0 { // a decoder's auto baud (the web's)
		b := math.Float64frombits(e.serLv.Baud.Load())
		if !(b > 0) {
			return p, "measuring the UART bit rate"
		}
		p.Baud = int(b)
	}
	if p.Proto == core.SerSENT && (p.TickNs <= 0 || p.Nibbles <= 0) { // the decoder's own: from the SYNC
		tick := math.Float64frombits(e.serLv.SentNs.Load())
		if !(tick > 0) {
			return p, "measuring the SENT tick"
		}
		if p.TickNs <= 0 {
			p.TickNs = tick
		}
		if p.Nibbles <= 0 {
			p.Nibbles = int(e.serLv.SentNib.Load()) // 0: the standard eight
		}
	}
	return p, ""
}

// serialMatchStart is where the match the FPGA triggered on began: decoding
// the request's length of the lines before the hardware trigger point hw
// (where the match ended), the start of the last match at or before it. -1
// when the decode does not find one there; the trigger point then stays the
// hardware's.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialMatchStart(f *sample.Frame, hw float64) float64 {
	if !(hw >= 0) || hw >= float64(f.Len()) || !(f.SampleS > 0) || (f.Content == sample.PeakPairs && !f.Flags.Has(sample.Ordered)) {
		return -1
	}
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	// The match and rate as the matcher took them: its units (a MIL-1553
	// word, an ARINC 429 field) are the decoded spans' values. The decoders
	// keep their own thresholds.
	if planned, why := e.serialPlanParams(p); why == "" {
		p.Bytes, p.Pattern, p.Baud, p.TickNs = planned.Bytes, planned.Pattern, planned.Baud, planned.TickNs
	}
	a, b := f.Codes8(0), f.Codes8(0)
	if p.ChA == 1 {
		a = f.Codes8(1)
	}
	if p.ChB == 1 {
		b = f.Codes8(1)
	}
	if len(a) < f.Len() || len(b) < f.Len() {
		return -1
	}
	at := int(hw)
	lo, hi := 0, f.Len()
	// A unit is at most the record: a slow bus's unit on a full-rate record
	// (an HDMI-CEC block of 24 ms is 12 M samples at 2 ns) times the frame's
	// units overflows a 32-bit int.
	if unit := min(core.SerialUnitSamples(p, f.SampleS, math.Float64frombits(e.serLv.SPIHz.Load())), f.Len()); unit > 0 {
		n := max(1, len(core.PatternOf(p)))
		lo = max(0, at-(n+2)*unit)
		hi = min(f.Len(), at+2*unit)
	}
	if pat := core.PatternOf(p); len(pat) == 2 && pat[0].Kind == core.PatternStart && pat[1].Kind == core.PatternData && pat[1].Mask == 0 {
		// the whole frame around it: a decoder drops a frame cut short (a CAN
		// frame read to its first data byte fails its CRC)
		if unit := min(core.SerialUnitSamples(p, f.SampleS, math.Float64frombits(e.serLv.SPIHz.Load())), f.Len()); unit > 0 {
			lo, hi = max(0, at-core.FrameUnitsBefore*unit), min(f.Len(), at+core.FrameUnitsAfter*unit)
		}
		return core.FrameUnitStart(a[lo:hi], b[lo:hi], f.SampleS, p, at-lo, lo)
	}
	ok, start := core.SerialMatchIn(a[lo:hi], b[lo:hi], f.SampleS, p, core.AnchorSpan{Target: at - lo, Lo: 0, Hi: at - lo})
	if !ok || start < 0 || start > at-lo {
		return -1
	}
	return float64(lo + start)
}

// matchLeadS is the record a match keeps before the FPGA's trigger, in
// seconds, until a placement has been learned: frameStartLeadUnits for a
// frame start, else the matched units, which end at the trigger (an ARINC
// 429 word of 320 us did not fit the 0.2 ms before it at 200us/div, and the
// trigger stayed at the word's end, off its start). 0 when a unit's length
// is not known (I2C).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func (e *Engine) matchLeadS(sampleS float64) float64 {
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	if planned, why := e.serialPlanParams(p); why == "" {
		p = planned
	}
	if !(sampleS > 0) {
		return 0
	}
	pat := core.PatternOf(p)
	units := len(pat)
	if len(pat) == 2 && pat[0].Kind == core.PatternStart && pat[1].Kind == core.PatternData && pat[1].Mask == 0 {
		units = core.FrameStartLeadUnits
	}
	return float64(units) * float64(core.SerialUnitSamples(p, sampleS, math.Float64frombits(e.serLv.SPIHz.Load()))) * sampleS
}

// serialPreRoom is the record the live window holds before the hardware
// serial trigger beyond the screen: software moves the trigger point back to
// the match's start, within the match and two units more (serialMatchStart).
// A quarter screen when the unit is not known, two screens at most.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialPreRoom(sampleS float64, screen int) int {
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	if planned, why := e.serialPlanParams(p); why == "" {
		p = planned
	}
	unit := core.SerialUnitSamples(p, sampleS, math.Float64frombits(e.serLv.SPIHz.Load()))
	if unit <= 0 {
		return screen / 4
	}
	return min(2*screen, max(screen/16, (max(1, len(core.PatternOf(p)))+2)*unit))
}

// serialNoHardware says why an armed serial request has no matcher in the
// loaded image.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialNoHardware() string {
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	if p.Empty() {
		return "no bus to match"
	}
	planned, why := e.serialPlanParams(p)
	if why != "" {
		return why
	}
	if want := acq.ProtocolImage(planned); want != e.protocolImage && e.images != nil && e.hasImage(want) {
		if want == "" {
			want = acq.ImageGeneralName
		}
		return "loading the " + want + " FPGA image"
	}
	packet := func(proto int) bool { return e.caps().Packet&(1<<proto) != 0 }
	have := map[int]bool{
		core.SerUART: e.caps().UART, core.SerI2C: e.caps().I2C, core.SerSPI: e.caps().SPI, core.SerManchester: e.caps().Manchester,
		core.SerSENT: e.caps().SENT, core.SerCAN: packet(core.SerCAN), core.SerMIL1553: e.caps().MIL1553, core.SerARINC: packet(core.SerARINC),
		core.SerUSB: e.caps().USBLS, core.SerFlexRay: packet(core.SerFlexRay), core.SerMIDI: e.caps().UART,
	}
	for proto := core.SerLIN; proto < SerialProtocols; proto++ {
		if acq.SymbolProtocol(proto) {
			have[proto] = e.caps().Symbol
		}
	}
	if known, ok := have[p.Proto]; ok && !known {
		return "this FPGA image has no matcher for the bus"
	}
	if p.Proto == core.SerARINC && e.arincAux.Load() == 0 {
		return "learning the ARINC 429 levels"
	}
	if n := len(core.PatternOf(p)); n > 32 {
		return fmt.Sprintf("a match of %d units is over the 32 the matcher holds", n)
	}
	if core.SequenceNeeded(p) && !e.caps().Sequence {
		return "this FPGA image has no sequence trigger for a match this long or with wildcards"
	}
	return "the matcher cannot take this match"
}
