// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/streamview"
)

// Stream is the scope-side decoded stream (ADR-PANEL-DECODED-STREAM).
type Stream interface {
	Start(p engine.SerialParams, trig engine.StreamTrigger, value int, label string) error
	Stop()
	Active() bool
	Snapshot() streamview.View
	Refuse(reason string)
	Lines(from, limit int) streamview.LinesPage
	LinesBinary(from int, dst []byte) int
}

// AutoSource is a decode of the frame on screen and its sample time: a
// detection (detectOnScreen), or the decoder settings' decode
// (decodeOnScreen); ok when it read a bus.
type AutoSource func() (decode.Result, float64, bool)

// decode-stream menu choices
const (
	decModeView   = 0
	decModeStream = 1
)

var decTrigNames = [...]string{"Error", "Value", "Manual"}
var decTrigKinds = [...]engine.StreamTrigger{engine.StreamOnError, engine.StreamOnValue, engine.StreamManual}

// SetDecodeStream wires the decoded stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) SetDecodeStream(s Stream) {
	c.mu.Lock()
	c.stream = s
	c.mu.Unlock()
}

// startStream streams the bus the decoder settings name, as they stand (typed
// in, or filled in by Auto): no detection runs. What the hardware decoder
// needs beyond them (the threshold, an SPI clock rate, SENT's tick) is read
// off a decode of the frame on screen with those settings, else of a sampled
// record (the live envelope is too coarse at slow timebases: a 115200 Bd
// UART at 500us/div). Starting can load an FPGA image, so it runs off the
// panel goroutine. A stream still running (the stop setting changed, or the
// bus) ends first; a start superseded meanwhile gives way to the newer one.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-222, REQ-SDS-228
func (c *Controller) startStream() {
	c.mu.Lock()
	s, trig, value := c.stream, c.decTrig, c.decValue
	c.streamList, c.streamTop = false, -1 // a new stream shows its live tail
	c.streamGen++
	gen := c.streamGen
	c.mu.Unlock()
	if s == nil {
		return
	}
	current := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.streamGen == gen
	}
	queued := time.Now()
	c.offPanel(func() {
		t0 := time.Now()
		if d := t0.Sub(queued); d > 50*time.Millisecond {
			c.logf("panel: stream: waited %v to start", d.Round(time.Millisecond))
		}
		if s.Active() { // started again: the image it runs on stays for the next
			if h, ok := c.eng.(interface {
				HoldDecodedImage(context.Context) error
			}); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				h.HoldDecodedImage(ctx)
				cancel()
			}
			stopWait(s)
		}
		res, sampleS, ok := c.decodeOnScreen()
		if !ok && !c.eng.Snapshot().Running { // a stopped stream's record decodes with other settings
			c.eng.SetRunning(true)
		}
		if !ok {
			all := func(*engine.Frame, bool) bool { return true }
			l := c.detectWith(true, false, all, c.decodeOnScreen, decimatedWait)
			res, sampleS, ok = l.res, l.sampleS, l.found
		}
		sv := c.snap()
		name := busName(sv.decProto, sv.decBaud, sv.decChA, sv.decChB, sv.decCPOL, sv.decCPHA)
		c.logf("panel: stream: %s read in %v", name, time.Since(t0).Round(time.Millisecond))
		if !c.streaming() || !current() {
			return // Stream went off, or decode did, or a newer start runs
		}
		if !ok {
			s.Refuse(c.noBusNote("no " + name + " decoded on screen"))
			return
		}
		p, err := streamview.ParamsFromDecode(c.serialBus(engine.SerialParams{Addr: -1, RW: 2}), res, sampleS)
		if err != nil {
			s.Refuse(err.Error())
			return
		}
		// A stream still starting or ending (Stream turned off and on again
		// quickly) ends first: Start is refused while one is starting, and that
		// one then stops as the Off asked, leaving Stream On with none running.
		tStop := time.Now()
		stopWait(s)
		if d := time.Since(tStop); d > 50*time.Millisecond {
			c.logf("panel: stream: the stream before took %v to end", d.Round(time.Millisecond))
		}
		if !c.streaming() || !current() {
			return
		}
		t1 := time.Now()
		if sn, ok := s.(interface {
			StartNoted(engine.SerialParams, engine.StreamTrigger, int, string, string) error
		}); ok {
			err = sn.StartNoted(p, decTrigKinds[trig%3], value, name, streamImageNote(sv.decProto, sv.decBaud, c.eng.Snapshot().Image))
		} else {
			err = s.Start(p, decTrigKinds[trig%3], value, name)
		}
		// ARINC 429's slicer levels are learned from a sampled record a moment
		// after the settings name it: a stream started right after BUS entry
		// waits for them
		for end := time.Now().Add(levelsWait); errors.Is(err, engine.ErrLevelsLearning) && time.Now().Before(end) && c.streaming() && current(); {
			time.Sleep(250 * time.Millisecond)
			err = s.Start(p, decTrigKinds[trig%3], value, name)
		}
		c.logf("panel: stream: started in %v", time.Since(t1).Round(time.Millisecond))
		if err != nil {
			c.logf("panel: stream: %v", err)
		}
	})
}

// streamImageNote says why a stream of proto at rate baud takes seconds to
// start when the loaded FPGA image (loaded, as the engine names it) has no
// receiver for it: AUTO in BUS frames a UART on the qualifier image's idle
// qualifier, and its stream needs the general image's UART receiver (a load
// of about four seconds); a bus decoded gap-free in software streams on the
// stream image. "" when the image in use streams it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamImageNote(proto, baud int, loaded string) string {
	want := "general"
	soft := ""
	switch {
	case proto == framecalc.ProtoManchester || proto == framecalc.ProtoUSB:
		want = "line"
	case proto == framecalc.ProtoSENT || proto == framecalc.ProtoCAN || proto == framecalc.ProtoMIL1553 ||
		proto == framecalc.ProtoARINC429 || proto == framecalc.ProtoFlexRay:
		want = "packet"
	case proto > framecalc.ProtoFlexRay && proto != framecalc.ProtoMIDI:
		want = "qualify" // the symbol trigger, its records decoded in software
		if soft = engine.SoftStreamMode(proto-1, baud, proto == framecalc.ProtoOneWire && baud >= 100000); soft == "gap-free" {
			want = "stream"
		}
	}
	if loaded == "" || loaded == want {
		return ""
	}
	if soft == "gap-free" {
		return fmt.Sprintf("loading the stream FPGA image, about 1 s: %s is decoded gap-free from its continuous stream", protoName(proto))
	}
	name := map[string]string{"qualify": "qualifier"}[loaded]
	if name == "" {
		name = loaded
	}
	return fmt.Sprintf("loading the %s FPGA image, about 1 s: the %s image has no %s receiver", want, name, protoName(proto))
}

// streamModeText says how the bus streams (ADR-STREAM-SOFT-DECODE): the
// running stream's own words when it has them (a captures stream's share of
// signal time read), else what the protocol at its rate gets.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamModeText(s *sv) string {
	if s.streamMode != "" {
		return s.streamMode
	}
	switch engine.SoftStreamMode(s.decProto-1, s.decBaud, s.decProto == framecalc.ProtoOneWire && s.decBaud >= 100000) {
	case "gap-free":
		return "gap-free, software decode of the continuous stream"
	case "captures":
		return "captures, not gap-free (back-to-back records)"
	}
	return "gap-free, the FPGA's decoded events"
}

// streamValueMax is the widest unit a stream stops on: an S/PDIF sample's 24
// bits (an ARINC 429 data field is 19, a MIL-1553, MDIO or Wiegand unit 16,
// the others 8; an SWD data word stops on its low 24). Wider, the sheet's
// range check (a relative tolerance of 1e-9) would let -1 in.
const streamValueMax = 0xFFFFFF

// levelsWait bounds a stream's wait for the ARINC 429 levels.
const levelsWait = 5 * time.Second

// stopStream ends a running stream by hand; its record goes to the display.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) stopStream() {
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	if s != nil && s.Active() {
		c.offPanel(s.Stop)
	}
}

// stopWait ends a stream for live captures or another stream (its record is
// not shown: End, where the stream has it) and waits (bounded) for its end:
// one still starting ends at its first read.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func stopWait(s Stream) {
	stop := s.Stop
	if e, ok := s.(interface{ End() }); ok {
		stop = e.End
	}
	for end := time.Now().Add(30 * time.Second); s.Active() && time.Now().Before(end); {
		stop()
		time.Sleep(50 * time.Millisecond)
	}
}

// leaveStream ends stream mode's stream and resumes live captures, off the
// panel goroutine while a stream runs (endStream).
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) leaveStream() {
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	if s == nil || !s.Active() {
		c.endStream()
		return
	}
	c.offPanel(c.endStream)
}

// endStream ends stream mode's stream and resumes live captures. The engine
// ends a stream stopped, its record on the display, so RUN waits for that
// end: a RUN before it leaves the scope stopped (the packets frozen on the
// stream's record after the Stream view, "Trigger on it" or DEFAULT SETUP
// from a stream).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) endStream() {
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	if s != nil {
		stopWait(s)
	}
	if !c.streaming() { // Stream on again meanwhile: its own start stops the scope
		c.eng.SetRunning(true)
	}
}

// streaming reports a stream wanted: BUS's Stream view (or the BUS sheet's
// Stream row) on with a bus to stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) streaming() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return serialProto(c.decProto) && c.decMode == decModeStream && c.stream != nil
}

// streamOn is streaming for a state read.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func streamOn(s *sv) bool {
	return serialProto(s.decProto) && s.decMode == decModeStream && s.hasStream
}

// syncStreamView keeps the stream with BUS's Stream view: shown with a bus
// to stream and no detection or AUTO under way, the stream runs with the BUS
// sheet's settings and stop; else none does. Called on every change of those
// (the view, the workspace, the bus, a detection or AUTO ending).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) syncStreamView() { c.syncStream(false) }

// syncStream is syncStreamView; wait ends a stream before it returns (AUTO
// sweeps on live captures).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) syncStream(wait bool) {
	c.mu.Lock()
	want := c.ui.ws == wsview.Bus && c.ui.busStream && serialProto(c.decProto) && c.stream != nil &&
		!c.ui.busDetecting && !c.autosetBusy
	was := c.decMode == decModeStream
	c.decMode = decModeView
	if want {
		c.decMode = decModeStream
	}
	c.mu.Unlock()
	switch {
	case want && !was:
		c.startStream()
	case !want && was && wait:
		c.endStream()
	case !want && was:
		c.leaveStream()
	}
}

// showBusView puts BUS on its Stream view (stream) or, from it, on its
// packets: the BUS sheet's Stream row, set on the LCD, the web page or the
// phone, starts and ends the stream as the view does.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) showBusView(stream bool) {
	c.mu.Lock()
	if stream && c.ui.ws != wsview.Bus {
		c.ui.views[c.ui.ws] = c.viewMode
		c.ui.ws, c.ui.posJob = wsview.Bus, false
		c.viewMode = c.ui.views[wsview.Bus]
	}
	if stream != c.ui.busStream {
		c.ui.posJob = false
	}
	if stream {
		c.ui.busEye = false
		c.closePickLocked()
	}
	c.ui.busStream = stream
	c.mu.Unlock()
	c.endEyeRaw()
	c.syncStreamView()
}

// streamStopsTurn is Adjust in BUS Stream: what stops the stream (a decode
// error, the Stream value, Run/Stop by hand); a stream under way starts
// again with it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) streamStopsTurn(dir int) {
	c.mu.Lock()
	c.decTrig = mod3(c.decTrig + dir)
	t := c.decTrig
	c.mu.Unlock()
	c.toast("Stream stops "+streamStopText(t, c.snap().decValue), 1400*time.Millisecond)
	if c.streaming() {
		c.startStream()
	}
}

// streamStopText says what stops a stream: trig is decTrig.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func streamStopText(trig, value int) string {
	switch mod3(trig) {
	case 1:
		return "on the value " + hexText(float64(value))
	case 2:
		return "by hand (Run/Stop)"
	}
	return "on a decode error"
}

// streamAdjustPush is the Adjust push in BUS Stream: with no bus, detect it
// (the stream then starts); else stop the stream, or start it again.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) streamAdjustPush() {
	s := c.snap()
	switch {
	case s.ui.busDetecting:
	case !busDecoding(s):
		c.autoDecodeBus()
	case !s.hasStream || !serialProto(s.decProto):
		c.toast("No hardware stream for "+protoName(s.decProto), 1600*time.Millisecond)
	case !streamOn(s):
	case s.streamStopped:
		c.startStream()
	default:
		c.stopStream()
	}
}

// streamScrollJob is Position's push in BUS Stream: on, Position scrolls the
// transcript (a stopped stream's list opens on its trigger line); off, the
// transcript follows the newest lines again.
// TRLC-LINKS: REQ-SDS-213, REQ-SDS-228
func (c *Controller) streamScrollJob(on bool) {
	s, stopped := c.streamStopped()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streamTop = -1
	if !on || s == nil {
		return
	}
	if v := s.Snapshot(); stopped {
		c.streamList = true
		if v.TriggerLine >= 0 {
			c.streamTop = max(0, v.TriggerLine-streamview.ListRows/2)
		}
	}
}

// setDecProto sets the decoder (framecalc.ProtoOff..ProtoFlexRay); Auto
// detects the bus and fills the settings in (autoDecode). Another protocol
// ends a stream (BUS's Stream view then streams the new bus); a bit-rate
// protocol starts at its usual rate; clock and data stay on different
// channels; an armed serial trigger follows the bus.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214, REQ-SDS-222
func (c *Controller) setDecProto(p int) error {
	if p < framecalc.ProtoOff || p >= framecalc.NumProtos {
		return fmt.Errorf("no protocol %d", p)
	}
	if p == framecalc.ProtoAuto {
		c.autoDecode()
		return nil
	}
	c.mu.Lock()
	c.auto = autoWatch{} // set by hand
	prev := c.decProto
	c.decProto = p
	c.decGen++
	if framecalc.TwoLine(p) {
		c.decChB = 1 - c.decChA
	}
	if (rateProto(p) || p > framecalc.ProtoFlexRay) && p != prev {
		c.decBaud = defaultRate(p)
	}
	if p == framecalc.ProtoUART && rateProto(prev) {
		c.decBaud = 115200
	}
	if p != prev {
		// another bus: its framing and polarity are its own (a Modbus's even
		// parity is no UART's), and a layer of the old bus reads nothing here
		c.decParity, c.decInv = "", false
		c.dropLayerLocked()
	}
	stop := p != prev && c.decMode == decModeStream
	if stop {
		c.decMode = decModeView
	}
	if p != framecalc.ProtoOff {
		c.ui.busFailed = false
	}
	c.mu.Unlock()
	c.syncDecodeView(p != framecalc.ProtoOff)
	if p == framecalc.ProtoOff && c.eng.Snapshot().SerialMode != 0 {
		c.setTrigTypeIndex(c.eng.Snapshot(), 0)
		c.toast("Serial trigger off (no decode): Edge", 1800*time.Millisecond)
	}
	c.syncSerialBus()
	if stop {
		c.leaveStream()
	}
	c.syncStreamView()
	return nil
}

// maxDecBaud is the fastest UART the decoder settings hold: the fast
// image's receiver on the samples reads 62.5 MBd, from a transmitter up to
// 2 % fast.
const maxDecBaud = 64_000_000

// setDecBaud sets the UART baud or the bit rate of a bit-rate protocol.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) setDecBaud(b int) error {
	if b < 50 || b > maxDecBaud {
		return fmt.Errorf("baud %d is outside 50..%d", b, maxDecBaud)
	}
	c.mu.Lock()
	c.decBaud = b
	c.auto = autoWatch{} // set by hand
	c.mu.Unlock()
	c.syncSerialBus()
	return nil
}

// setDecRole puts a line on a channel: the first role (UART source, I2C SCL,
// SPI CLK) or the second (SDA, DATA); the other role moves to the other
// channel, so clock and data never share one.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) setDecRole(first bool, ch int) error {
	c.mu.Lock()
	c.auto = autoWatch{} // set by hand
	if first {
		c.decChA = ch & 1
		c.decChB = 1 - c.decChA
	} else {
		c.decChB = ch & 1
		c.decChA = 1 - c.decChB
	}
	c.mu.Unlock()
	c.syncSerialBus()
	return nil
}

// setStreamMode starts (with the decoder settings) or stops the hardware
// decoded stream: BUS on its Stream view, or back on its packets.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-222, REQ-SDS-228
func (c *Controller) setStreamMode(on bool) error {
	c.mu.Lock()
	bus := serialProto(c.decProto) && c.stream != nil
	inView := c.ui.busStream
	c.mu.Unlock()
	switch {
	case on && !bus:
		return fmt.Errorf("a stream needs a bus: set Protocol (Auto finds it)")
	case on || inView:
		c.showBusView(on)
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-018
func streamName(proto string) string {
	names := map[string]string{"uart": "UART", "i2c": "I2C", "spi": "SPI", "manchester": "MANCH", "sent": "SENT",
		"canfd": "CAN", "mil1553": "1553", "usbls": "USB", "flexray": "FLEXR", "lin": "LIN", "dmx512": "DMX",
		"midi": "MIDI", "modbus": "MODBUS", "onewire": "1WIRE", "ws281x": "WS281X", "dht": "DHT", "cec": "CEC",
		"j1850": "J1850", "irnec": "NEC", "irsirc": "SIRC", "irrc5": "RC5", "irrc6": "RC6", "dali": "DALI",
		"usbpd": "USBPD", "spdif": "SPDIF", "ps2": "PS2", "swd": "SWD", "mdio": "MDIO", "wiegand": "WIEG",
		"quad": "QUAD", "stepper": "STEP", "pwm": "PWM", "rcencode": "RC", "ook": "OOK", "miller": "MILLER",
		"morse": "MORSE", "dcf77": "DCF77", "em4100": "EM4100", "em4305": "EM4305", "t55xx": "T55XX", "swim": "SWIM",
		"sdq": "SDQ", "pjdl": "PJON", "dsi": "DSI", "qi": "QI", "sdcard": "SD", "pdi": "PDI", "cjtag": "CJTAG",
		"maple": "MAPLE", "caliper": "CALIP", "irmp": "IR"}
	if n, ok := names[proto]; ok {
		return n
	}
	return proto
}

// StreamMode reports the BUS sheet's Stream on, for the HUD.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) StreamMode() bool { return c.streaming() }

// StreamList reports whether a stopped stream shows its transcript list (not
// the record's waveform) and the list's first line; -1 follows the newest.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) StreamList() (bool, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streamList, c.streamTop
}

// streamStopped is the stream wanted (nil none) and whether it has ended
// (trigger, STOP or failure).
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) streamStopped() (Stream, bool) {
	if !c.streaming() {
		return nil, false
	}
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	return s, !s.Active()
}

// toggleStreamList is Zoom on a stopped stream in BUS Stream (and the BUS
// sheet's Stream shows row): Wave <-> List. Opening the list centres the
// trigger line when there is one.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) toggleStreamList() {
	s, stopped := c.streamStopped()
	if !stopped {
		return
	}
	v := s.Snapshot()
	c.mu.Lock()
	c.streamList = !c.streamList
	if c.streamList {
		c.streamTop = -1
		if v.TriggerLine >= 0 {
			c.streamTop = max(0, v.TriggerLine-streamview.ListRows/2)
		}
	}
	c.mu.Unlock()
}

// scrollStreamList moves the transcript by lines: a stream under way holds
// still where it is scrolled to (the newest lines go on below it), a stopped
// stream's waveform opens its list.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) scrollStreamList(lines int) {
	s, stopped := c.streamStopped()
	if s == nil {
		return
	}
	v := s.Snapshot()
	c.mu.Lock()
	defer c.mu.Unlock()
	if stopped {
		c.streamList = true
	}
	last := max(0, v.Lines-streamview.ListRows)
	top := c.streamTop
	if top < 0 || top > last {
		top = last
	}
	c.streamTop = min(max(top+lines, 0), last)
}

// StreamStatus reports the decoded stream for /api/status; ok is false
// outside stream mode.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) StreamStatus() (streamview.View, bool) {
	if !c.streaming() {
		return streamview.View{}, false
	}
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	return s.Snapshot(), true
}

// StreamLines reads the decoded stream's transcript from absolute line from,
// for /api/stream/lines; ok is false without a stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) StreamLines(from, limit int) (streamview.LinesPage, bool) {
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	if s == nil {
		return streamview.LinesPage{}, false
	}
	return s.Lines(from, limit), true
}

// StreamWindow is the stream's newest rows transcript lines as the LCD's list
// writes them, for the web's BUS (/api/stream/window); ok is false without a
// stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) StreamWindow(rows int) (streamview.Page, bool) {
	c.mu.Lock()
	s, ok := c.stream.(interface {
		Window(rows, top int) streamview.Page
	})
	c.mu.Unlock()
	if !ok {
		return streamview.Page{}, false
	}
	return s.Window(rows, -1), true
}

// StreamLinesBinary encodes the stream's transcript from absolute line from
// (decodedlines batch format); ok is false without a stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) StreamLinesBinary(from int, dst []byte) (int, bool) {
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	if s == nil {
		return 0, false
	}
	return s.LinesBinary(from, dst), true
}
