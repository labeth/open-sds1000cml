// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// AUTO in BUS (REQ-SDS-223) frames the bus for reading instead of fitting a
// waveform: it forgets the bus decoded, fits the vertical as autoset does
// (the swing on screen, the threshold midway), detects the bus again and
// fills in the decoder settings, puts the trigger on the start of a frame
// and the timebase where whole messages show with every bit resolved. The
// FPGA decides every trigger (ADR-SERIAL-HARDWARE-TRIGGER): the frame start
// is a hardware matcher's, or for UART the end of an idle gap the hardware
// pulse qualifier measures. BUS entry's detection sets the same trigger.

// How a detection frames the bus it finds (Controller.busFrameNext).
const (
	busFrameEntry = 1 // BUS's own detection: the frame-start trigger
	busFrameAuto  = 2 // AUTO in BUS: the trigger near the left, a whole message on screen too
)

// imageWait bounds the wait for the general image after a protocol trigger
// ends (a load takes a few seconds).
const imageWait = 10 * time.Second

// busSearchTdiv is the timebase AUTO in BUS searches from at the fastest:
// Default setup's.
const busSearchTdiv = 500e-6

// busSearchMaxTdiv is the timebase AUTO in BUS searches from at the slowest:
// under engine.RollMinTdiv.
const busSearchMaxTdiv = 5e-3

// busTrigPos is where AUTO in BUS puts the trigger: near the left, so the
// message that starts there fills the screen.
const busTrigPos = 0.1

// busAuto is the AUTO key in BUS. A second press while it runs cancels it,
// as a second AUTO cancels an autoset.
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) busAuto() {
	if c.frameFn == nil {
		c.autoset()
		return
	}
	from := c.markChanges()
	c.mu.Lock()
	if c.autosetBusy {
		if c.autosetStop != nil {
			close(c.autosetStop)
			c.autosetStop = nil
		}
		c.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	c.autosetBusy, c.autosetStop, c.autosetMsg = true, stop, "AUTO: framing the bus..."
	c.autosetRun++
	c.autosetFrom = from
	sync := c.detectSync
	c.mu.Unlock()
	c.pushLEDs()
	if sync {
		c.runBusAuto(stop)
		return
	}
	go c.runBusAuto(stop)
}

// runBusAuto forgets the bus, fits the vertical, detects the bus and frames
// it (frameBus).
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) runBusAuto(stop chan struct{}) {
	defer c.endAutoset(stop)
	// A UART framed before left the qualifier image loaded: the sweep and the
	// detection run on it (an edge there is as on the general image), and the
	// idle qualifier the UART is framed with needs it again, so neither load
	// of about four seconds happens. Released once the bus is framed.
	if h, ok := c.eng.(interface{ HoldQualifyImage(bool) }); ok {
		h.HoldQualifyImage(true)
		c.qualHeld.Store(true)
		defer func() {
			c.qualHeld.Store(false)
			h.HoldQualifyImage(false)
		}()
	}
	c.syncStream(true) // a stream ends: the fit sweeps live captures
	// The search running gives way (searchDropped) and puts the timebase
	// back: the entry is the scope's, not a timebase that search stepped to
	// (20ms/div, where a UART is no more than averaged bursts).
	if !c.awaitDetection(stop) {
		c.cancelledAuto(stop, c.autoEntryNow())
		return
	}
	entry := c.autoEntryNow()
	c.autoClearMatch(entry)
	entryTdiv := entry.tdiv
	c.forgetBus()
	if _, ok := c.autosetSweep(stop, goalBus); !ok {
		c.cancelledAuto(stop, entry)
		c.noSignalBus(stop)
		return
	}
	select {
	case <-stop:
		c.cancelledAuto(stop, entry)
		return
	default:
	}
	// The search starts where BUS entry's does after Default setup, on a screen
	// of many messages: the autoset's few cycles of the fastest edge rate held
	// part of an ARINC 429 word, and the search found no bus. A rolling
	// screen's chunks leave gaps no frame survives: an entry there searches
	// from the slowest timebase that does not roll (slower buses are looked
	// for after, findSlowBus).
	c.setTdivNearest(min(max(entryTdiv, busSearchTdiv), busSearchMaxTdiv))
	c.mu.Lock()
	c.autosetMsg = "AUTO: finding the bus..."
	c.ui.busDetecting = true
	c.busFrameNext = busFrameAuto
	c.mu.Unlock()
	c.pushLEDs()
	c.syncDecodeView(true)
	c.autoSearch.Store(true)
	c.runDetect()
	c.autoSearch.Store(false)
}

// forgetBus is AUTO in BUS forgetting the bus decoded before: a serial
// trigger on it ends (its match stays for the bus found next), and an SPI
// bus found next samples its data at the middle of the eye measured on it
// (Data delay Auto): a delay kept from before, or Default setup's 0 ns, sat
// on the 104 MHz flash's data edges (margin -4 ns) and its bytes read wrong.
// TRLC-LINKS: REQ-SDS-223, REQ-SDS-230
func (c *Controller) forgetBus() {
	c.mu.Lock()
	c.decProto, c.decMode = framecalc.ProtoOff, decModeView
	c.decGen++
	c.auto = autoWatch{}
	c.decDelayAuto, c.spiEye, c.spiEyeKey, c.spiEyeVotes = true, decode.SPIEye{}, spiEyeK{}, decode.SPIEyeVotes{}
	c.mu.Unlock()
	// the clock rate is measured again too: a rate set for another bus
	// framed the next one by the wrong gap
	if se := c.serialTrig(); se != nil && se.SerialConfig().SPIClockHz != 0 {
		_ = c.setSPIClock(0)
	}
}

// busAutoNoticeFor is how long the notice says what AUTO in BUS did: the
// frame-start trigger it arms can load an FPGA image for seconds after, and
// the notice outlasts that.
const busAutoNoticeFor = 8 * time.Second

// noSignalBus ends an AUTO in BUS whose sweep found no signal to fit (not
// one cancelled): no bus is searched for, and BUS and the notice say so
// instead of showing decode off as if AUTO had not run.
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) noSignalBus(stop chan struct{}) {
	select {
	case <-stop:
		return // cancelled: cancelledAuto said so
	default:
	}
	note := c.noBusNote("no signal on CH1 or CH2 - no bus to find, decode stays off")
	c.mu.Lock()
	c.ui.busFailed = c.decProto == framecalc.ProtoOff
	c.ui.busNote = note
	c.toastLocked("AUTO: "+note, busAutoNoticeFor)
	c.mu.Unlock()
	c.publish()
}

// autoDecodeBus is BUS's own detection (entry with decode off, Adjust when
// no bus was found): Auto, then the frame-start trigger.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223
func (c *Controller) autoDecodeBus() {
	c.mu.Lock()
	if !c.ui.busDetecting {
		c.busFrameNext = busFrameEntry
	}
	c.mu.Unlock()
	c.autoDecode()
}

// takeBusFrame reports, once, how the detection under way frames the bus.
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) takeBusFrame() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.busFrameNext
	c.busFrameNext = 0
	return f
}

// busMsgs is what a decode of the bus shows of its messages: the longest
// complete one, the shortest idle between two, and for UART the longest time
// the line stays high inside one (the stop bit, the idle between its
// characters and a character's high bits), all in seconds (0: not seen).
type busMsgs struct {
	longest, idle, highRun float64
}

// measureMsgs measures the bus's messages on a sampled record decoded with
// the decoder settings: the frame on screen, else a decimated record.
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) measureMsgs() busMsgs {
	res, sampleS, ok := c.decodeOnScreen()
	valid := 0
	if !ok {
		all := func(*engine.Frame, bool) bool { return true }
		l := c.detectWith(true, false, all, c.decodeOnScreen, decimatedWait)
		res, sampleS, ok = l.res, l.sampleS, l.found
	}
	if !ok || !(sampleS > 0) {
		return busMsgs{}
	}
	if c.frameFn != nil {
		c.frameFn(func(f *engine.Frame) {
			if f != nil {
				valid = framecalc.Valid(f)
			}
		})
	}
	var m busMsgs
	ps := anaPackets(res, valid)
	for i, p := range ps {
		if p.Complete {
			m.longest = max(m.longest, float64(p.I1-p.I0)*sampleS)
		}
		if i > 0 {
			if g := float64(p.I0-ps[i-1].I1) * sampleS; g > 0 && (m.idle == 0 || g < m.idle) {
				m.idle = g
			}
		}
	}
	if res.Proto == "uart" && res.Baud > 0 {
		bit := 1 / float64(res.Baud)
		gap := 0.0 // the longest idle between two characters of one message
		pi, prevEnd, prevPkt := 0, -1, -1
		for _, sp := range res.Spans {
			if sp.Kind != "data" {
				continue
			}
			for pi < len(ps) && ps[pi].I1 < sp.I0 {
				pi++
			}
			if pi == prevPkt && prevEnd >= 0 {
				gap = max(gap, float64(sp.I0-prevEnd)*sampleS)
			}
			prevEnd, prevPkt = sp.I1, pi
		}
		// a character's eight data bits and stop bit can all be high
		m.highRun = gap + 10*bit
	}
	return m
}

// uartIdle is the idle-gap width the pulse qualifier triggers after: midway
// between the longest high time inside a message and the shortest idle
// between two; 0 when the record shows no such gap (a stream with no pauses).
// TRLC-LINKS: REQ-SDS-223
func uartIdle(m busMsgs) float64 {
	if !(m.highRun > 0) || !(m.idle > 1.5*m.highRun) {
		return 0
	}
	return (m.highRun + m.idle) / 2
}

// sofPattern is the frame-start match a protocol's hardware matcher takes,
// with what it names; ok false when the bus has none (UART). I2C's own
// matcher fires on the address byte after every START with an empty match;
// ARINC 429's words are frames each; a MIL-1553 message opens with a command
// word; the others' decoders report the start of a frame, so a frame's first
// unit after it.
// TRLC-LINKS: REQ-SDS-223
func sofPattern(proto int) (bytes []int, pattern []engine.PatternElem, name string, ok bool) {
	any := engine.PatternElem{Kind: 2}
	switch proto {
	case framecalc.ProtoI2C:
		return nil, nil, "I2C START (address byte)", true
	case framecalc.ProtoARINC429:
		return nil, []engine.PatternElem{any}, "ARINC word", true
	case framecalc.ProtoMIL1553:
		return nil, []engine.PatternElem{{Kind: 2, Value: engine.MILCommand, Mask: engine.MILCommand}}, "1553 command word", true
	case framecalc.ProtoSPI, framecalc.ProtoManchester, framecalc.ProtoSENT, framecalc.ProtoCAN, framecalc.ProtoUSB, framecalc.ProtoFlexRay:
		return nil, []engine.PatternElem{{Kind: 1}, any}, protoName(proto) + " frame start", true
	case framecalc.ProtoMIDI: // a status byte opens a message: the UART receiver's unit match
		return nil, []engine.PatternElem{midiStatus}, "MIDI status byte", true
	}
	if proto > framecalc.ProtoFlexRay && proto < framecalc.NumProtos { // a break, a reset, a leader: the frame start
		return nil, []engine.PatternElem{{Kind: 1}, any}, protoName(proto) + " frame start", true
	}
	return nil, nil, "", false
}

// defaultMatch reports a match the frame-start trigger may replace: none,
// or one a frame-start trigger set (SOF then any unit, one any unit, or a
// MIL-1553 command word). Bytes a user picked or typed stay, a typed SOF
// with more after it too.
// TRLC-LINKS: REQ-SDS-223
func defaultMatch(p engine.SerialParams) bool {
	anyUnit := func(e engine.PatternElem) bool { return e.Kind == 2 && e.Mask == 0 }
	cmd := engine.PatternElem{Kind: 2, Value: engine.MILCommand, Mask: engine.MILCommand}
	switch {
	case len(p.Bytes) > 0:
		return false
	case len(p.Pattern) == 0:
		return true
	case len(p.Pattern) == 2:
		return p.Pattern[0].Kind == 1 && anyUnit(p.Pattern[1])
	}
	return len(p.Pattern) == 1 && (anyUnit(p.Pattern[0]) || p.Pattern[0] == cmd || p.Pattern[0] == midiStatus)
}

// frameTrigger puts the hardware trigger on the start of the bus's frames
// and says what it chose ("TRIG: I2C START (address byte)"). A match the user
// set wins: the serial trigger arms on it. A bus with no frame-start matcher
// and no idle gap keeps the trigger it has.
// TRLC-LINKS: REQ-SDS-223, REQ-SDS-013
func (c *Controller) frameTrigger(m busMsgs) string {
	s := c.snap()
	se := c.serialTrig()
	if se != nil {
		p := se.SerialConfig()
		if s.decProto == framecalc.ProtoUART && s.decLayer == "sbus" && (defaultMatch(p) || sbusStart(p)) {
			p.Bytes, p.Pattern = []int{sbusHeader}, nil
			c.armSerialWith(p)
			return "TRIG: S.BUS frame start (0F)"
		}
		if !defaultMatch(p) {
			c.armSerialWith(p)
			return "TRIG: your match " + strings.Join(tokensOf(p), " ")
		}
		if bytes, pattern, name, ok := sofPattern(s.decProto); ok {
			p.Bytes, p.Pattern = bytes, pattern
			if s.decProto == framecalc.ProtoI2C {
				p.Addr, p.RW = -1, 2
			}
			c.armSerialWith(p)
			return "TRIG: " + name
		}
	}
	if s.decProto == framecalc.ProtoUART {
		if w := uartIdle(m); w > 0 {
			c.idleTrigger(s.decChA, w)
			return fmt.Sprintf("TRIG: UART first byte (idle > %s)", si(w, "s"))
		}
		return "TRIG: edge (no idle between UART messages to start on)"
	}
	return "TRIG: edge (no frame-start trigger for " + protoName(s.decProto) + ")"
}

// armSerialWith arms the serial trigger on the decoded bus with p's match
// (an empty match too: I2C's any address).
// TRLC-LINKS: REQ-SDS-223, REQ-SDS-013
func (c *Controller) armSerialWith(p engine.SerialParams) {
	se := c.serialTrig()
	if se == nil {
		return
	}
	c.syncDecodeView(true)
	c.eng.SetTrigType(0) // the hardware edge paces captures
	se.SetSerialParams(c.serialBus(p))
	se.SetSerialMode(engine.SerialTrigger)
}

// idleTrigger is UART's frame start: the hardware pulse qualifier on the
// line's high level (idle), firing where a high time longer than w ends -
// the first start bit after the idle between two messages.
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) idleTrigger(ch int, w float64) {
	st := c.eng.Snapshot()
	if st.SerialMode != 0 {
		c.setTrigTypeIndex(st, 0)
	}
	c.eng.SetTrigSource(ch & 1)
	c.eng.SetTrigSlope(true) // a high pulse: the idle
	_ = c.editPulse(func() {
		c.pulseLvl, c.pulseCond = 0.5, engine.CondGreater
		c.pulseMin, c.pulseMax = w/2*1e9, w*1e9
	}, true)
	c.setTrigTypeIndex(c.eng.Snapshot(), 1)
}

// messageTimebase is the fastest timebase at least tdiv on which a message
// of length msg fits after the trigger, while the live readout still
// resolves the bus's shortest feature (framecalc.MinEnvelopeFeature points)
// or, past that, a sampled record does (readableFeature samples): BUS then
// decodes its sampled records at a lower frame rate (busRawFrames; a sped-up
// DCF77 minute at 20ms/div). tdiv itself when none does.
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) messageTimebase(msg, featureS, tdiv float64) float64 {
	after := (1 - busTrigPos) * 10
	if !(msg > 0) || msg <= after*tdiv || len(c.tdivs) == 0 {
		return tdiv
	}
	window := 12.5 // the serial trigger's wider window
	tds := c.tdivs // the ladder, ascending as the engine lists it
	if !slices.IsSorted(tds) {
		tds = slices.Clone(tds)
		slices.Sort(tds)
	}
	for _, t := range tds {
		if t < tdiv*0.999 || msg*1.05 > after*t {
			continue
		}
		if featureS > 0 && featureS/(window*t/liveEnvelopePoints) < framecalc.MinEnvelopeFeature &&
			featureS/engine.PlanSRAM(t).SampleS < readableFeature {
			break
		}
		return t
	}
	return tdiv
}

// frameBus finishes AUTO in BUS and BUS entry's detection once the bus is
// filled in: the frame-start trigger, and for AUTO the trigger near the left
// and a timebase that shows a whole message. It returns what the notice adds.
// TRLC-LINKS: REQ-SDS-223
func (c *Controller) frameBus(featureS float64, auto bool) string {
	m := c.measureMsgs()
	note := c.frameTrigger(m)
	if auto {
		_ = c.setTrigPos(busTrigPos)
		td := c.eng.Snapshot().TdivSet()
		if t := c.messageTimebase(m.longest, featureS, td); math.Abs(t-td) > 1e-3*td {
			c.setTdivNearest(t)
			note += ", " + si(t, "s") + "/div for a whole message"
		}
	}
	return note
}

// edgeOnGeneralImage puts the trigger back to an edge and waits (bounded)
// for the general image: a pulse or serial trigger runs on the qualifier or a
// protocol image, and a sweep that measured there, or while the image went
// back, fitted the vertical wrong (an ARINC 429 bus then went unfound, a
// UART's trigger was left off the signal). An edge on a qualifier image held
// for AUTO in BUS (HoldQualifyImage) is as on the general image.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-223
func (c *Controller) edgeOnGeneralImage() {
	if st := c.eng.Snapshot(); st.SerialMode != 0 || st.TrigType != 0 {
		c.setTrigTypeIndex(st, 0)
	}
	for end, settled := c.now().Add(imageWait), 0; settled < 2 && c.now().Before(end) && !c.detectSync; time.Sleep(100 * time.Millisecond) {
		st := c.eng.Snapshot()
		general := st.Image == "" || st.Image == "general" || (st.Image == "qualify" && c.qualHeld.Load())
		if general && !strings.HasPrefix(st.Busy, "loading") {
			settled++
		} else {
			settled = 0
		}
	}
}

// endAutoset ends an AUTO sweep: the rows it changed are kept for the remote
// that started it, the busy flag drops, and the sweep's sample lease ends.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) endAutoset(stop chan struct{}) {
	// the rows the sweep changed, for the remote that started it: kept with
	// its number before the busy flag drops, so a snapshot that shows the
	// sweep ended also lists them
	c.mu.Lock()
	run, from := c.autosetRun, c.autosetFrom
	c.mu.Unlock()
	changed, _ := c.changesSince(from)
	c.mu.Lock()
	c.autosetBusy = false
	if c.autosetStop == stop {
		c.autosetStop = nil
	}
	c.autosetDone, c.autosetChanged = run, changed
	c.mu.Unlock()
	// The sweep's sample lease ends with it: held on, the live view stayed
	// the filtered decimated capture (2.34 MHz at 5us/div) for 3 s after.
	if e, ok := c.eng.(interface{ LeaseSampleView(time.Duration) }); ok {
		e.LeaseSampleView(0)
	}
	c.syncStreamView() // BUS's Stream view streams again, the bus as AUTO left it
	c.pushLEDs()
}
