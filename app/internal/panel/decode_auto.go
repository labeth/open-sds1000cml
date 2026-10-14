// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// Auto is no decoding mode (REQ-SDS-214): it detects the bus once and fills
// the protocol, its lines, rate, SPI mode and bit order into the decoder
// settings, which then decode the bus everywhere (the strip, BUS, Stream, the
// serial trigger, the web and the phone) as if typed in. A detection per
// capture flipped the protocol on a bad frame and cost CPU on every one. One
// thing it kept: when the bus it found stops decoding while captures keep
// coming (the probes moved to another bus), Auto detects again, once.

// autoLostAfter is how long the filled-in settings may decode nothing,
// while captures come, before Auto detects again; autoLostCaptures is how
// many such captures that takes at the least. One bad capture never does.
const (
	autoLostAfter    = 2 * time.Second
	autoLostCaptures = 3
)

// autoWatch is what Auto filled in and whether that still decodes (guarded
// by Controller.mu). The zero value is settings set by hand: never watched.
type autoWatch struct {
	filled bool      // the decoder settings are a detection's, no hand edit since
	armed  bool      // they decoded the bus since: losing it detects again
	good   time.Time // the last capture they decoded
	tdiv   float64   // that capture's timebase
	bad    int       // captures since that decoded nothing
	seen   frameKey  // the capture judged last
}

// decodesBus reports a decode that read the bus: units, nearly all sound. A
// bus read with another's settings still yields units, with errors among
// them (a 115200 Bd line read at 460800 Bd: one unit in five a framing
// error); one error is a unit cut by the record's edge.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func decodesBus(res decode.Result) bool {
	if !res.OK || len(res.Bytes) == 0 {
		return false
	}
	bad := 0
	for _, sp := range res.Spans {
		if strings.HasSuffix(sp.Kind, "error") {
			bad++
		}
	}
	return bad <= 1 || 10*bad < len(res.Bytes)
}

// watchBus judges each new capture against the settings Auto filled in, and
// detects again once when they have decoded nothing for autoLostAfter (and
// autoLostCaptures captures) after decoding the bus. A capture the live
// envelope cannot resolve, or with no edges on either line, says nothing
// about the bus: a detection there would find nothing and leave the bus that
// comes next undetected. Nor does one at another timebase than the bus last
// decoded at: turned closer, a screen holds a few bits of one character, and
// a detection would take the timebase back. With no captures (stopped, or Normal with nothing
// triggering) nothing is judged. It runs within OnFrame: the detection itself
// runs off it.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (c *Controller) watchBus(s *sv, f *engine.Frame) {
	k := keyOf(f)
	c.mu.Lock()
	w, busy := c.auto, c.ui.busDetecting
	streaming := c.decMode == decModeStream
	c.mu.Unlock()
	if !w.filled || busy || streaming || s.decProto == framecalc.ProtoOff || !s.st.Running ||
		k == (frameKey{}) || k == w.seen || f.IsEnv || (f.PeakDetect && !f.Ordered) {
		return
	}
	res := framecalc.Decode(f, decodeParamsOf(s))
	good := decodesBus(res)
	// says nothing of the bus: an envelope too coarse for it, or lines with no
	// edges (a probe off, a gap in the traffic)
	neutral := !good && (f.PeakDetect && f.Ordered && !res.OK && strings.Contains(res.Error, "not resolved") || idleLines(f) ||
		w.armed && f.TdivS > 0 && w.tdiv > 0 && math.Abs(f.TdivS-w.tdiv) > 1e-3*w.tdiv)
	now := c.now()
	c.mu.Lock()
	if !c.auto.filled || c.auto.seen == k {
		c.mu.Unlock()
		return
	}
	c.auto.seen = k
	switch {
	case good:
		c.auto.armed, c.auto.good, c.auto.tdiv, c.auto.bad = true, now, f.TdivS, 0
	case !neutral:
		c.auto.bad++
	}
	lost := c.auto.armed && c.auto.bad >= autoLostCaptures && now.Sub(c.auto.good) >= autoLostAfter
	if lost {
		c.auto.armed = false // once: the next detection, or a bus decoding again, re-arms it
		c.toastLocked("AUTO: "+protoName(s.decProto)+" stopped decoding - detecting the bus again", 2000*time.Millisecond)
	}
	c.mu.Unlock()
	if lost {
		go c.autoDecode() // OnFrame's caller holds the frame source
	}
}

// idleLines reports a capture with no edges on either channel.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func idleLines(f *engine.Frame) bool {
	valid := framecalc.Valid(f)
	return decode.ShortestRun(framecalc.Channel(f, 0, valid)) == 0 && decode.ShortestRun(framecalc.Channel(f, 1, valid)) == 0
}
