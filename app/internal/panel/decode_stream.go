// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
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
// panel goroutine.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-222
func (c *Controller) startStream() {
	c.mu.Lock()
	s, trig, value := c.stream, c.decTrig, c.decValue
	c.streamList, c.streamTop = false, -1 // a new stream shows its live tail
	c.mu.Unlock()
	if s == nil {
		return
	}
	c.offPanel(func() {
		t0 := time.Now()
		res, sampleS, ok := c.decodeOnScreen()
		if !ok {
			all := func(*engine.Frame, bool) bool { return true }
			l := c.detectWith(true, false, all, c.decodeOnScreen, decimatedWait)
			res, sampleS, ok = l.res, l.sampleS, l.found
		}
		sv := c.snap()
		name := busName(sv.decProto, sv.decBaud, sv.decChA, sv.decChB, sv.decCPOL, sv.decCPHA)
		c.logf("panel: stream: %s read in %v", name, time.Since(t0).Round(time.Millisecond))
		if !c.streaming() {
			return // Stream went off, or decode did, meanwhile
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
		for end := time.Now().Add(30 * time.Second); s.Active() && time.Now().Before(end); {
			s.Stop()
			time.Sleep(50 * time.Millisecond)
		}
		if !c.streaming() {
			return
		}
		t1 := time.Now()
		err = s.Start(p, decTrigKinds[trig%3], value, name)
		c.logf("panel: stream: started in %v", time.Since(t1).Round(time.Millisecond))
		if err != nil {
			c.logf("panel: stream: %v", err)
		}
	})
}

// stopStream ends a running stream by hand; its record goes to the display.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) stopStream() {
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	if s != nil && s.Active() {
		go s.Stop()
	}
}

// leaveStream ends stream mode's stream and resumes live captures. The engine
// ends a stream stopped, its record on the display, so RUN waits for that end:
// a RUN before it leaves the scope stopped ("Trigger on it" or DEFAULT SETUP
// from a stream).
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) leaveStream() {
	c.mu.Lock()
	s := c.stream
	c.mu.Unlock()
	if s == nil || !s.Active() {
		c.eng.SetRunning(true)
		return
	}
	go func() {
		s.Stop()
		for end := time.Now().Add(30 * time.Second); s.Active() && time.Now().Before(end); {
			time.Sleep(50 * time.Millisecond) // a stream still starting ends at its first read
		}
		if !c.streaming() { // Stream on again meanwhile: its own start stops the scope
			c.eng.SetRunning(true)
		}
	}()
}

// streaming reports the BUS sheet's Stream on with a bus to stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) streaming() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return serialProto(c.decProto) && c.decMode == decModeStream && c.stream != nil
}

// setDecProto sets the decoder (framecalc.ProtoOff..ProtoFlexRay); Auto
// detects the bus and fills the settings in (autoDecode). Another protocol
// ends a stream; a bit-rate protocol starts at its usual rate; clock and data
// stay on different channels; an armed serial trigger follows the bus.
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
	if p == framecalc.ProtoI2C || p == framecalc.ProtoSPI {
		c.decChB = 1 - c.decChA
	}
	if rateProto(p) && p != prev {
		c.decBaud = defaultRate(p)
	}
	if p == framecalc.ProtoUART && rateProto(prev) {
		c.decBaud = 115200
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
	return nil
}

// setDecBaud sets the UART baud or the bit rate of a bit-rate protocol.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) setDecBaud(b int) error {
	if b < 50 || b > 20000000 {
		return fmt.Errorf("baud %d is outside 50..20000000", b)
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
// decoded stream.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-222
func (c *Controller) setStreamMode(on bool) error {
	c.mu.Lock()
	if !serialProto(c.decProto) || c.stream == nil {
		c.mu.Unlock()
		if on {
			return fmt.Errorf("a stream needs a bus: set Protocol (Auto finds it)")
		}
		return nil
	}
	was := c.decMode == decModeStream
	c.decMode = map[bool]int{true: decModeStream, false: decModeView}[on]
	c.mu.Unlock()
	switch {
	case on && !was:
		c.startStream()
	case !on && was:
		c.leaveStream()
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-018
func streamName(proto string) string {
	names := map[string]string{"uart": "UART", "i2c": "I2C", "spi": "SPI", "manchester": "MANCH", "sent": "SENT",
		"canfd": "CAN", "mil1553": "1553", "usbls": "USB", "flexray": "FLEXR"}
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

// streamStopped is a stream that has ended (trigger, STOP or failure).
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

// toggleStreamList is F2 on a stopped stream: Wave <-> List. Opening the list
// centres the trigger line when there is one.
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

// scrollStreamList moves the open list by lines; it reports whether the knob
// was used for that.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) scrollStreamList(lines int) bool {
	s, stopped := c.streamStopped()
	if !stopped {
		return false
	}
	v := s.Snapshot()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.streamList {
		return false
	}
	last := max(0, v.Lines-streamview.ListRows)
	top := c.streamTop
	if top < 0 || top > last {
		top = last
	}
	c.streamTop = min(max(top+lines, 0), last)
	return true
}

// streamListToTrigger centres the open list on the trigger line.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) streamListToTrigger() bool {
	s, stopped := c.streamStopped()
	if !stopped {
		return false
	}
	v := s.Snapshot()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.streamList || v.TriggerLine < 0 {
		return false
	}
	c.streamTop = max(0, v.TriggerLine-streamview.ListRows/2)
	return true
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
