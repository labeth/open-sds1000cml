// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
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

// AutoSource returns the latest Auto decode and its frame's sample time.
type AutoSource func() (decode.Result, float64, bool)

// decode-stream menu choices
const (
	decModeView   = 0
	decModeStream = 1
)

var decTrigNames = [...]string{"Error", "Value", "Manual"}
var decTrigKinds = [...]engine.StreamTrigger{engine.StreamOnError, engine.StreamOnValue, engine.StreamManual}

// SetDecodeStream wires the decoded stream and the Auto decode it starts from.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) SetDecodeStream(s Stream, auto AutoSource) {
	c.mu.Lock()
	c.stream, c.autoSrc = s, auto
	c.mu.Unlock()
}

// startStream streams with the settings Auto last found. Starting can load an
// FPGA image, so it runs off the panel goroutine.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) startStream() {
	c.mu.Lock()
	s, auto, trig, value := c.stream, c.autoSrc, c.decTrig, c.decValue
	c.mu.Unlock()
	if s == nil || auto == nil {
		return
	}
	c.mu.Lock()
	c.streamList, c.streamTop = false, -1 // a new stream shows its live tail
	c.mu.Unlock()
	// Auto runs on the frame on screen now; it takes a while on long views,
	// so it runs with the start off the panel goroutine.
	go func() {
		t0 := time.Now()
		res, sampleS, ok := auto()
		c.logf("panel: stream: auto %s in %v", res.Proto, time.Since(t0).Round(time.Millisecond))
		if !ok {
			s.Refuse("Auto found no protocol on screen - show more of it (slower t/div)") // e.g. one UART byte at 10 us/div
			return
		}
		p, err := streamview.ParamsFromAuto(res, sampleS)
		if err != nil {
			s.Refuse(err.Error())
			return
		}
		label := fmt.Sprintf("%s %s", streamName(res.Proto), res.Src)
		if res.Baud > 0 {
			label = fmt.Sprintf("%s %d b/s %s", streamName(res.Proto), res.Baud, res.Src)
		}
		t1 := time.Now()
		err = s.Start(p, decTrigKinds[trig%3], value, label)
		c.logf("panel: stream: started in %v", time.Since(t1).Round(time.Millisecond))
		if err != nil {
			c.logf("panel: stream: %v", err)
		}
	}()
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

// streaming reports the DECODE menu in Auto Stream mode.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) streaming() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decProto == 1 && c.decMode == decModeStream && c.stream != nil
}

// decodeValueAdjust lets ADJUST step the stream trigger value by its
// accelerated count while that item is highlighted.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) decodeValueAdjust(delta int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.menuPage != pgDecode || c.decProto != 1 || c.menuSel != 4 {
		return false
	}
	c.decValue = ((c.decValue+delta)%0x10000 + 0x10000) % 0x10000
	return true
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

// StreamMode reports the DECODE menu in Auto Stream mode, for the HUD.
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
// outside Auto Stream mode.
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
// for /api/stream/lines; ok is false outside Auto Stream mode.
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
