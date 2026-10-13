// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/analysis"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/sramcapture"
)

// BUS (REQ-SDS-214): entering it with decode off finds the bus and decodes
// it; the pane lists the complete packets as a fixed-column hex dump with a
// separate string box, held still while the same messages repeat. Adjust
// selects a packet, its push picks bytes to trigger on, Position jumps match
// to match, its push goes back to the trigger, and the S/DIV push zooms onto
// the selected packet. BUS again shows the eye diagram (REQ-SDS-215).

// Eye grid size: 2 UI across 384 columns (2 px each, 768 px), the plot's
// height in 2 px rows; the eye is fed at least eyeFeedSPU samples per UI
// (every sample of a fast bus: the edges' timing figures need them, and the
// web's eye folds them all) and at most eyeMaxFed samples a capture (the
// fold costs about 1 ms per 1000 samples on the device's core).
const (
	eyeCols    = 384
	eyeFeedSPU = 256
	eyeMaxFed  = 16384
	// eyeRestartOff captures in a row off the folded bit period restart the
	// eye: the bus's rate changed.
	eyeRestartOff = 3
)

// eyeRows is the eye grid's height for the BUS plot.
func eyeRows() int { return (480 - wsDefs[wsview.Bus].plotTop - 12) / 2 }

// busDecoding reports a bus being decoded (not off, not an Auto that found none).
// TRLC-LINKS: REQ-SDS-214
func busDecoding(s *sv, a *anaState) bool {
	if s.decProto == framecalc.ProtoOff {
		return false
	}
	if s.decProto == framecalc.ProtoAuto {
		return a.resOK && a.res.Proto != "" && a.res.Proto != "off" && a.res.Proto != "none"
	}
	return true
}

// busListKey is what the packet list belongs to: a change starts it over.
func busListKey(s *sv) string {
	return fmt.Sprint(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA, s.st.TdivS)
}

// busFrame decodes the frame and offers its packets to the held list.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) busFrame(a *anaState, s *sv, f *engine.Frame) {
	if s.decProto == framecalc.ProtoOff {
		a.res, a.resOK, a.live = decode.Result{}, false, nil
		return
	}
	a.res = framecalc.Decode(f, decodeParamsOf(s))
	a.resOK = a.res.OK
	if !a.resOK {
		a.live = nil
		return
	}
	a.live = anaPackets(a.res, a.g.valid)
	if k := busListKey(s); k != a.listKey {
		a.list.Reset()
		a.listKey = k
	}
	if s.st.Running {
		a.list.Update(a.live) // held still while the messages repeat
	} else {
		a.list.Replace(a.live) // stopped: the capture on screen
	}
}

// selectedLive is the selected packet's index among the frame's packets
// (-1 when the frame does not hold it), for the strip's box and the zoom.
// TRLC-LINKS: REQ-SDS-214
func selectedLive(a *anaState, partial bool, sel int) int {
	if partial {
		return sel // the rows are the frame's own fragments
	}
	return a.list.SelectedIn(a.live)
}

// tokMatch reports whether a packet holds the match tokens (55, A?, ??, ERR).
// TRLC-LINKS: REQ-SDS-214
func tokMatch(toks []string, p Packet) bool {
	n := len(toks)
	if n == 0 || n > len(p.Bytes) {
		return false
	}
	ok := func(t string, b byte, bad bool) bool {
		if t == "ERR" {
			return bad
		}
		if bad {
			return false
		}
		h := hex2(b)
		return len(t) == 2 && (t[0] == '?' || t[0] == h[0]) && (t[1] == '?' || t[1] == h[1])
	}
	for i := 0; i+n <= len(p.Bytes); i++ {
		all := true
		for j := 0; j < n && all; j++ {
			bad := i+j < len(p.ByteErr) && p.ByteErr[i+j]
			all = ok(toks[j], p.Bytes[i+j], bad)
		}
		if all {
			return true
		}
	}
	return false
}

// busRows are the listed packets: the complete ones, or the fragments when
// nothing on the record is complete (a packet longer than the screen).
// TRLC-LINKS: REQ-SDS-214
func busRows(a *anaState) (rows []Packet, partial bool) {
	if r := a.list.Rows(); len(r) > 0 {
		return r, false
	}
	return a.live, len(a.live) > 0
}

// busSelected is the selected row (-1 none).
// TRLC-LINKS: REQ-SDS-214
func busSelected(a *anaState, rows []Packet, partial bool) int {
	if len(rows) == 0 {
		return -1
	}
	if partial {
		return 0
	}
	return clampInt(a.list.Selected(), 0, len(rows)-1)
}

// busSelect moves the selection by dir packets.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-213
func (c *Controller) busAdjust(dir int) {
	s := c.snap()
	c.amu.Lock()
	if !busDecoding(s, &c.ana) {
		c.amu.Unlock()
		c.toast("No bus decoded: push Adjust to try again", 1500*time.Millisecond)
		return
	}
	rows, partial := busRows(&c.ana)
	if len(rows) == 0 {
		c.amu.Unlock()
		c.toast("No packets on screen", 1500*time.Millisecond)
		return
	}
	sel := busSelected(&c.ana, rows, partial)
	if !partial {
		c.ana.list.Select(clampInt(sel+dir, 0, len(rows)-1))
	}
	p := rows[clampInt(sel+dir, 0, len(rows)-1)]
	g := c.ana.g
	c.amu.Unlock()
	if s.zoom > 1 {
		c.panToPacket(g, p)
	}
}

// busNextMatch is Position < > in BUS: the next or previous packet holding
// the match bytes.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-213
func (c *Controller) busNextMatch(dir int) {
	s := c.snap()
	toks := matchTokens(s)
	c.amu.Lock()
	if !busDecoding(s, &c.ana) {
		c.amu.Unlock()
		c.toast("No bus decoded: push Adjust to try again", 1500*time.Millisecond)
		return
	}
	if len(toks) == 0 {
		c.amu.Unlock()
		c.toast("No match bytes yet: push Adjust to pick them from a packet", 1800*time.Millisecond)
		return
	}
	rows, partial := busRows(&c.ana)
	sel := busSelected(&c.ana, rows, partial)
	for j := sel + dir; j >= 0 && j < len(rows); j += dir {
		if tokMatch(toks, rows[j]) {
			if !partial {
				c.ana.list.Select(j)
			}
			p, g := rows[j], c.ana.g
			c.amu.Unlock()
			if s.zoom > 1 {
				c.panToPacket(g, p)
			}
			return
		}
	}
	c.amu.Unlock()
	where := "below"
	if dir < 0 {
		where = "above"
	}
	c.toast("No more packets with "+strings.Join(toks, " ")+" "+where, 1500*time.Millisecond)
}

// busToTrigger is the Position push in BUS: back to the trigger.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) busToTrigger() {
	s := c.snap()
	if s.zoom > 1 {
		c.setZoomOff(0)
	} else {
		c.centreTrigger()
	}
	c.amu.Lock()
	c.ana.list.Select(0)
	c.amu.Unlock()
	c.toast("Back to the trigger", 900*time.Millisecond)
}

// panToPacket centres the zoom window on a packet.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) panToPacket(g geom, p Packet) {
	if g.valid <= 0 {
		return
	}
	xc := g.edgeX
	if xc < 0 {
		xc = float64(g.valid) / 2
	}
	c.setZoomOff((float64(p.I0+p.I1)/2 - xc) / float64(g.valid))
}

// busZoom is the S/DIV push in BUS Packets: zoom onto the selected packet (a
// free-running scope stops so it holds still); again: zoom off.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) busZoom() {
	s := c.snap()
	if s.zoom > 1 {
		c.setZoom(1)
		c.toast("Zoom off", 900*time.Millisecond)
		return
	}
	c.amu.Lock()
	rows, partial := busRows(&c.ana)
	sel := busSelected(&c.ana, rows, partial)
	g := c.ana.g
	var p Packet
	idx := -1
	if sel >= 0 {
		idx = selectedLive(&c.ana, partial, sel)
		if idx >= 0 && idx < len(c.ana.live) {
			p = c.ana.live[idx]
		} else {
			idx = -1
		}
	}
	c.amu.Unlock()
	if idx < 0 {
		c.zoomToggle()
		return
	}
	win := g.winCols
	if win <= 0 || win > g.valid {
		win = g.valid
	}
	dur := float64(max(p.I1-p.I0, 1))
	z := 1
	for _, zz := range zoomFactors[1:] {
		if dur <= 0.8*float64(win)/float64(zz) {
			z = zz
		}
	}
	if z == 1 {
		c.toast("This packet already fills the screen - use a faster time/div", 1800*time.Millisecond)
		return
	}
	msg := ""
	if s.st.Running && s.st.SerialMode == 0 {
		c.setRun(1)
		msg = " (stopped - Run/Stop continues)"
	}
	c.setZoom(z)
	c.panToPacket(g, p)
	c.toast(fmt.Sprintf("Zoom %dx on packet %d%s", z, sel+1, msg), 1600*time.Millisecond)
}

// zoomToggle is the S/DIV push: zoom on (the last factor, 10x the first
// time) around the trigger, or off.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) zoomToggle() {
	s := c.snap()
	if s.zoom > 1 {
		c.setZoom(1)
		c.toast("Zoom off", 900*time.Millisecond)
		return
	}
	z := s.ui.zoomLast
	if z <= 1 {
		z = 10
	}
	c.setZoom(z)
	c.setZoomOff(0)
	c.toast(fmt.Sprintf("Zoom %dx (S/DIV sets the factor)", z), 1400*time.Millisecond)
}

// ---- detection ----

// resultProto is the decoder an Auto result names (0: none).
// TRLC-LINKS: REQ-SDS-214
func resultProto(res decode.Result) int {
	if !res.OK {
		return 0
	}
	return map[string]int{"uart": framecalc.ProtoUART, "i2c": framecalc.ProtoI2C, "spi": framecalc.ProtoSPI,
		"manchester": framecalc.ProtoManchester, "sent": framecalc.ProtoSENT, "canfd": framecalc.ProtoCAN,
		"mil1553": framecalc.ProtoMIL1553, "arinc429": framecalc.ProtoARINC429, "usbls": framecalc.ProtoUSB,
		"flexray": framecalc.ProtoFlexRay}[res.Proto]
}

// resultName names the bus an Auto result found: "UART 115200 8N1 on C1".
// TRLC-LINKS: REQ-SDS-214
func resultName(res decode.Result) string {
	chB := res.Roles.ChB & 1
	if chB == res.Roles.ChA&1 {
		chB = 1 - chB
	}
	return busName(resultProto(res), res.Baud, res.Roles.ChA&1, chB, res.Roles.CPOL, res.Roles.CPHA)
}

// lockBus fixes the decoder to a detected bus (an Auto result): protocol,
// lines and rate. A stream Auto was running ends, as leaving Auto ends it.
// It reports whether the result named a bus.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) lockBus(res decode.Result) bool {
	p := resultProto(res)
	if p == 0 {
		return false
	}
	c.mu.Lock()
	c.decProto = p
	c.decGen++
	c.decChA, c.decChB = res.Roles.ChA&1, res.Roles.ChB&1
	if c.decChB == c.decChA {
		c.decChB = 1 - c.decChA
	}
	if res.Baud > 0 {
		c.decBaud = res.Baud
	}
	if p == framecalc.ProtoSPI {
		c.decCPOL, c.decCPHA = res.Roles.CPOL, res.Roles.CPHA
	}
	stream := c.decMode == decModeStream
	if stream {
		c.decMode = decModeView
	}
	c.mu.Unlock()
	c.syncDecodeView(true)
	c.syncSerialBus()
	if stream {
		c.leaveStream()
	}
	return true
}

// lastAuto runs Auto on the frame on screen (the shared decode cache makes
// it free when the LCD decoded it already). Not for use inside OnFrame.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) lastAuto() (decode.Result, bool) {
	res, _, ok := c.lastAutoAt()
	return res, ok
}

// lastAutoAt is lastAuto with the decoded frame's sample interval.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) lastAutoAt() (decode.Result, float64, bool) {
	if c.frameFn == nil {
		return decode.Result{}, 0, false
	}
	var res decode.Result
	var sampleS float64
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.IsEnv || (f.PeakDetect && !f.Ordered) || len(f.C1) < 32 {
			return
		}
		res = framecalc.Decode(f, framecalc.DecodeParams{Proto: framecalc.ProtoAuto, Format: "hex"})
		sampleS = f.SampleS
	})
	return res, sampleS, res.OK && res.Proto != "" && res.Proto != "off"
}

// liveEnvelopePoints is the live readout's points per window at the least: an
// envelope of 1024 (min, max) buckets of Q8.8 words.
const liveEnvelopePoints = 2 * sramcapture.EnvelopeBucketsQ8

// readableFeature is the points per bus feature the timebase step aims for:
// framecalc.MinEnvelopeFeature with room for the serial trigger's wider
// window (12.5 divisions instead of 10).
const readableFeature = 5.0

// readableTimebase steps the timebase to the slowest that reads the bus live
// when the current one cannot. The live readout is an envelope of
// liveEnvelopePoints over the screen (a quarter more while a serial trigger
// is armed), and a bus feature under framecalc.MinEnvelopeFeature of its points
// aliases (an I2C bus at 500us/div lists packets of no bytes). sampleS is the
// decoded frame's sample interval. It returns
// what the notice adds ("" when the timebase stays).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) readableTimebase(res decode.Result, sampleS float64) string {
	feature := decode.FeatureSamples(res) * sampleS
	if !(feature > 0) || len(c.tdivs) == 0 {
		return ""
	}
	s := c.snap()
	window := 10.0
	if s.st.SerialMode != 0 {
		window = 12.5
	}
	points := func(tdiv float64) float64 { return feature / (window * tdiv / liveEnvelopePoints) }
	// the timebase asked for: one set while a stream ran applies when it ends
	if points(s.st.TdivSet()) >= framecalc.MinEnvelopeFeature {
		return ""
	}
	best := 0
	for i, t := range c.tdivs {
		if points(t) >= readableFeature && t > c.tdivs[best] {
			best = i
		}
	}
	if c.setTdivIdx(best) != nil {
		return ""
	}
	return ", " + si(c.tdivs[best], "s") + "/div to read it"
}

// detectOn waits (bounded by detectWait) for a sampled frame that accept
// takes and runs auto (Auto on the frame on screen) on it, falling back to the
// frame on screen at the end. It returns the result, the decoded frame's
// sample interval and whether a bus was found.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) detectOn(accept func(*engine.Frame) bool, auto AutoSource) (decode.Result, float64, bool) {
	deadline := c.now().Add(detectWait)
	// While running, a frame captured after the search began: the one on
	// screen, or one in flight, may predate a setting just changed (an ARINC
	// bus is not found in a frame from before its bandwidth limit).
	var start uint64
	if c.frameFn != nil && c.snap().st.Running {
		c.frameFn(func(f *engine.Frame) {
			if f != nil {
				start = f.Seq
			}
		})
	}
	fresh := func(f *engine.Frame) bool { return start == 0 || f.Seq >= start+2 }
	for {
		if e, ok := c.eng.(interface{ LeaseDecodeView(time.Duration) }); ok {
			e.LeaseDecodeView(3 * time.Second)
		}
		raw, usable := false, false
		if c.frameFn != nil {
			c.frameFn(func(f *engine.Frame) {
				usable = f != nil && !f.IsEnv && !(f.PeakDetect && !f.Ordered) && f.Valid >= 32 && len(f.C1) >= f.Valid
				raw = usable && !f.PeakDetect && accept(f) && fresh(f)
			})
		}
		last := c.detectSync || !c.now().Before(deadline)
		if raw || (usable && last) {
			return auto()
		}
		if last {
			return decode.Result{}, 0, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// findBus searches for a bus as BUS's auto-detect does: auto on a sampled
// frame and, when that record hid the bus, again at the slowest timebase
// sampled at the converters' full rate, which stays when the bus is found
// there. A record hides a bus when it is decimated (a 10 Mbit/s FlexRay at
// 500us/div's 32 ns) or too short (Autoset's 10us/div holds about one UART
// byte). The timebase found there stays only while wanted (the
// caller still needs the bus: Auto still picked, a stream still asked for).
// One search runs at a time. It returns auto's result, the decoded frame's
// sample interval, whether a bus was found and the timebase it stepped to
// (0: none).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) findBus(auto AutoSource, wanted func() bool) (decode.Result, float64, bool, float64) {
	c.searchMu.Lock()
	defer c.searchMu.Unlock()
	res, sampleS, found := c.detectOn(func(*engine.Frame) bool { return true }, auto)
	// a scope waiting for a trigger captures nothing at the full rate either
	if full, t := c.fullRateS(), c.fullRateTdiv(); full > 0 && t > 0 && !found && t != c.snap().st.TdivSet() && !c.waitingForTrigger(c.snap()) {
		prev := c.snap().st.TdivSet()
		c.setTdivNearest(t)
		res, sampleS, found = c.detectOn(func(f *engine.Frame) bool {
			return f.SampleS <= 1.25*full && (f.TdivS == 0 || math.Abs(f.TdivS-t) <= 1e-3*t)
		}, auto)
		if found && wanted() {
			return res, sampleS, true, t
		}
		c.setTdivNearest(prev)
	}
	return res, sampleS, found, 0
}

// noBusNote is what a bus search that found nothing says: none says no bus
// was there, but a scope waiting for a trigger (Normal with the level off the
// signal) sends no new capture to search.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) noBusNote(none string) string {
	if c.waitingForTrigger(c.snap()) { // the Run/Stop lamp's amber
		return "no new capture - the scope waits for a trigger: Auto or Force, then again"
	}
	return none
}

// autoPicked reports Protocol = Auto.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) autoPicked() bool { return c.snapProto() == framecalc.ProtoAuto }

// offPanel runs fn off the panel goroutine, or inline in tests: a bus search
// reads sampled frames, seconds at slow timebases.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) offPanel(fn func()) {
	c.mu.Lock()
	sync := c.detectSync
	c.mu.Unlock()
	if sync {
		fn()
		return
	}
	go fn()
}

// offPanelErr is offPanel for a search that can fail: its error when it ran
// inline; off the panel goroutine its notice says how it ended.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) offPanelErr(fn func() error) error {
	c.mu.Lock()
	sync := c.detectSync
	c.mu.Unlock()
	if sync {
		return fn()
	}
	go func() { _ = fn() }()
	return nil
}

// findAuto is Protocol = Auto's search when it is picked: Auto decodes the
// live envelope, too coarse at slow timebases for a bus plain on screen (a
// 115200 Bd UART at 500us/div matches no protocol there). The
// bus is found on a sampled record as BUS's auto-detect finds it, and the
// timebase steps to the slowest that reads it live; decode stays Auto.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) findAuto() {
	res, sampleS, found, stepped := c.findBus(c.lastAutoAt, c.autoPicked)
	if c.snapProto() != framecalc.ProtoAuto {
		return // the protocol changed meanwhile
	}
	if found {
		c.toast("AUTO: "+resultName(res)+c.readTimebase(res, sampleS, stepped), 2200*time.Millisecond)
	} else {
		c.toast("AUTO: "+c.noBusNote("no bus found on CH1 or CH2 yet"), 2200*time.Millisecond)
	}
	c.publish()
}

// readTimebase steps to the timebase that reads a bus findBus found live
// (readableTimebase), and says where the timebase went: the step, or the
// full-rate timebase the search kept.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) readTimebase(res decode.Result, sampleS, stepped float64) string {
	if note := c.readableTimebase(res, sampleS); note != "" || stepped == 0 {
		return note
	}
	return ", " + si(stepped, "s") + "/div to read it"
}

// fullRateS is the record's finest sample interval, the fastest timebase's
// (0 without a ladder).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) fullRateS() float64 {
	if len(c.tdivs) == 0 {
		return 0
	}
	return engine.PlanSRAM(slices.Min(c.tdivs)).SampleS
}

// fullRateTdiv is the slowest timebase whose record is sampled at the full
// rate (0 without a ladder).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) fullRateTdiv() float64 { return engine.FullRateTdiv(c.tdivs) }

// busName names a decoded bus: "UART 115200 8N1 on C1".
// TRLC-LINKS: REQ-SDS-214
func busName(proto, baud, chA, chB int, cpol, cpha bool) string {
	switch proto {
	case framecalc.ProtoUART:
		return fmt.Sprintf("UART %d 8N1 on C%d", baud, chA+1)
	case framecalc.ProtoI2C:
		return fmt.Sprintf("I2C on C%d/C%d", chA+1, chB+1)
	case framecalc.ProtoSPI:
		return fmt.Sprintf("SPI mode %d on C%d/C%d", b2ic(cpol)<<1|b2ic(cpha), chA+1, chB+1)
	case framecalc.ProtoSENT:
		return fmt.Sprintf("SENT on C%d", chA+1)
	}
	if rateProto(proto) {
		return fmt.Sprintf("%s %s on C%d", protoName(proto), si(float64(baud), "b/s"), chA+1)
	}
	return protoName(proto)
}

// autoDecode finds the bus with Auto, locks it and decodes it. It runs when
// F2 enters BUS with decode off and on the Adjust push when nothing was found.
// The engine first sends sampled frames (detection cannot read an envelope),
// so the search runs off the panel goroutine.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) autoDecode() {
	c.mu.Lock()
	if c.ui.busDetecting {
		c.mu.Unlock()
		return
	}
	c.ui.busDetecting = true
	sync := c.detectSync
	c.mu.Unlock()
	c.syncDecodeView(true)
	if sync {
		c.runDetect()
		return
	}
	go c.runDetect()
}

// detectWait bounds how long BUS's auto-detect waits for a sampled frame.
const detectWait = 6 * time.Second

// runDetect waits (bounded) for a sampled frame and detects the bus on it.
// The decode lease makes the engine read samples instead of the live
// peak-detect envelope; that read can take seconds at slow timebases, and an
// envelope is too coarse to find a bus there (a UART plain on screen at
// 500 us/div). Only when no sampled frame comes is
// the envelope tried. A record the timebase decimates hides a fast bus (a
// 10 Mbit/s FlexRay at 500 us/div's 32 ns): with none found there, the search
// runs again at the slowest timebase sampled at the converters' full rate,
// which stays when the bus is found there.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) runDetect() {
	c.mu.Lock()
	gen := c.decGen
	c.mu.Unlock()
	// A protocol set while the search ran (seconds at slow timebases) stands:
	// the search's lock, or its "decode stays off", must not undo a UART picked
	// on the phone meanwhile.
	mine := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.decGen == gen
	}
	res, sampleS, found, stepped := c.findBus(c.lastAutoAt, mine)
	if !mine() {
		c.mu.Lock()
		c.ui.busDetecting = false
		c.mu.Unlock()
		c.publish()
		return
	}
	locked := found && c.lockBus(res)
	tdivNote, none := "", ""
	if locked {
		tdivNote = c.readTimebase(res, sampleS, stepped)
	} else {
		none = c.noBusNote("no bus found on CH1 or CH2 - decode stays off")
	}
	c.mu.Lock()
	c.ui.busDetecting = false
	c.ui.busFailed = !locked
	if locked {
		c.toastLocked("AUTO: "+busName(c.decProto, c.decBaud, c.decChA, c.decChB, c.decCPOL, c.decCPHA)+", decode on"+tdivNote, 2200*time.Millisecond)
	} else if c.decGen == gen {
		c.decProto = framecalc.ProtoOff
		c.decGen++
		c.toastLocked("AUTO: "+none, 2400*time.Millisecond)
	}
	c.mu.Unlock()
	if !locked {
		c.syncDecodeView(false)
	}
	c.amu.Lock()
	c.ana.list.Reset()
	c.ana.listKey = ""
	c.amu.Unlock()
	c.publish()
}

// ---- pickers ----

// packetToks are a packet's bytes as picker tokens.
func packetToks(p Packet) []string {
	out := make([]string, len(p.Bytes))
	for i, b := range p.Bytes {
		if i < len(p.ByteErr) && p.ByteErr[i] {
			out[i] = "ERR"
		} else {
			out[i] = hex2(b)
		}
	}
	return out
}

// openPickWS is the Adjust push in BUS Packets: pick bytes of the listed
// packets, starting at the selected one.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) openPickWS() {
	s := c.snap()
	c.amu.Lock()
	rows, partial := busRows(&c.ana)
	sel := busSelected(&c.ana, rows, partial)
	var toks [][]string
	var starts []bool
	for _, p := range rows {
		toks = append(toks, packetToks(p))
		starts = append(starts, true)
	}
	c.amu.Unlock()
	if len(rows) == 0 {
		c.toast("No packets on screen to pick from", 1600*time.Millisecond)
		return
	}
	_ = s
	c.mu.Lock()
	c.ui.pick = pickFrom(toks, starts, sel)
	c.ui.wpick = true
	c.mu.Unlock()
}

// openPickSub is the BUS tab's "From screen": the bytes decoded on screen,
// starting at the first complete message.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) openPickSub() error {
	var res decode.Result
	s := c.snap()
	if s.decProto == framecalc.ProtoOff {
		return fmt.Errorf("no decoded bytes on screen: set Protocol first")
	}
	var valid int
	if c.frameFn != nil {
		c.frameFn(func(f *engine.Frame) {
			if f != nil && len(f.C1) > 0 {
				res = framecalc.Decode(f, decodeParamsOf(s))
				valid = framecalc.Valid(f)
			}
		})
	}
	ps := anaPackets(res, valid)
	if len(ps) == 0 {
		return fmt.Errorf("no decoded bytes on screen: set Protocol first")
	}
	var toks [][]string
	var starts []bool
	first := -1
	for i, p := range ps {
		toks = append(toks, packetToks(p))
		starts = append(starts, p.Complete || i > 0)
		if first < 0 && p.Complete {
			first = i
		}
	}
	c.mu.Lock()
	c.ui.edit = false
	c.ui.pick = pickFrom(toks, starts, max(first, 0))
	c.ui.sub = subPick
	c.mu.Unlock()
	return nil
}

// ---- the eye ----

// eyeConfig is what the eye folds: the data line and the decoder's bit
// time, or why there is no eye. measured is a bit time measured on this
// capture (the SPI or I2C clock), which differs from capture to capture in
// its last digits.
// TRLC-LINKS: REQ-SDS-215
func eyeConfig(s *sv, a *anaState) (ch int, bit float64, measured bool, why string) {
	ch, bit, why = eyeConfigOf(s, a)
	proto := s.decProto
	if proto == framecalc.ProtoAuto {
		proto = map[string]int{"spi": framecalc.ProtoSPI, "i2c": framecalc.ProtoI2C}[a.res.Proto]
	}
	return ch, bit, proto == framecalc.ProtoSPI || proto == framecalc.ProtoI2C, why
}

// eyeConfigOf is eyeConfig's data line and bit time.
// TRLC-LINKS: REQ-SDS-215
func eyeConfigOf(s *sv, a *anaState) (ch int, bit float64, why string) {
	if !busDecoding(s, a) {
		return 0, 0, "No bus decoded: F2 again for Packets, then push Adjust to find it"
	}
	res := a.res
	if !res.OK {
		return 0, 0, res.Error
	}
	proto, baud, chA, chB := s.decProto, s.decBaud, s.decChA, s.decChB
	if proto == framecalc.ProtoAuto {
		chA, chB, baud = res.Roles.ChA, res.Roles.ChB, res.Baud
		proto = map[string]int{"uart": framecalc.ProtoUART, "i2c": framecalc.ProtoI2C, "spi": framecalc.ProtoSPI,
			"manchester": framecalc.ProtoManchester, "canfd": framecalc.ProtoCAN, "mil1553": framecalc.ProtoMIL1553,
			"arinc429": framecalc.ProtoARINC429, "usbls": framecalc.ProtoUSB, "flexray": framecalc.ProtoFlexRay}[res.Proto]
	}
	switch {
	case proto == framecalc.ProtoUART || rateProto(proto):
		ch = chA
		if baud > 0 {
			bit = 1 / float64(baud)
		}
	case proto == framecalc.ProtoSPI || proto == framecalc.ProtoI2C:
		ch = chB
		bit = res.SPB * a.g.sampleS
		if !s.chDisp[chA&1] {
			return 0, 0, fmt.Sprintf("The clock channel C%d is off", chA+1)
		}
	default:
		return 0, 0, "No bit period for " + protoName(proto)
	}
	if !(bit > 0) {
		return 0, 0, "No bit period yet"
	}
	if !s.chDisp[ch&1] {
		return 0, 0, fmt.Sprintf("The data channel C%d is off", ch+1)
	}
	return ch & 1, bit, ""
}

// eyeWhyText explains a capture the eye could not fold.
// TRLC-LINKS: REQ-SDS-215
func eyeWhyText(err string, ch int) string {
	switch err {
	case analysis.EyeNoRecord:
		return "No samples to fold"
	case analysis.EyeNoSwing:
		return fmt.Sprintf("No edges on C%d: is the bus idle?", ch+1)
	case analysis.EyeNoBitGrid:
		return "The edges do not fall on one bit period"
	case analysis.EyeTooFewSamples:
		return "Too few samples per bit: turn S/DIV clockwise or push Zoom"
	case analysis.EyeShortRecord:
		return "Under 2 UI on screen: turn S/DIV counter-clockwise"
	case analysis.EyeUIInconsistent:
		return "The bit period changed: push Adjust to clear the eye"
	case analysis.EyeNoLock:
		return "No bit grid fits the bursts yet"
	}
	return "Folding the bit stream..."
}

// eyeFrame folds the screen's data line into the eye: decimated to about
// eyeFeedSPU samples per UI and at most eyeMaxFed samples a capture. While it
// folds it leases full-rate raw frames, as the web's eye does: the screen's
// decimated readout (24 ns at 10us/div) smears the edges over its sample
// interval and leaves rise, fall and TIE as bounds.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeFrame(a *anaState, s *sv, f *engine.Frame) {
	ch, bit, measured, why := eyeConfig(s, a)
	if why == "" {
		if e, ok := c.eng.(interface{ LeaseDecodeView(time.Duration) }); ok {
			e.LeaseDecodeView(3 * time.Second) // renewed by each capture; ends with the eye
		}
	}
	if why == "" && (f.IsEnv || f.PeakDetect && !f.Ordered) {
		why = "The eye needs sampled captures (not peak detect or roll)"
	}
	if why != "" {
		a.eyeWhy, a.eyeOK = why, false
		return
	}
	// A clock period measured on each capture is no setting: the eye's own
	// check (the period within 2 % of the folded one) keeps it, and a bus
	// whose rate really changed restarts it below.
	keyBit := bit
	if measured {
		keyBit = 0
	}
	key := fmt.Sprint(ch, s.decProto, keyBit, s.vidx[ch], s.offV[ch], s.probe[ch], s.cpl[ch], s.inv[ch], f.SampleS, s.zoom,
		s.zoomOff, s.st.AcqMode, s.st.TdivS, s.st.TrigPosFrac)
	if a.eye == nil {
		a.eye = anaNewEye(eyeCols, eyeRows())
	}
	if key != a.eyeKey || a.eyeOff >= eyeRestartOff { // any change to the picture starts the eye over
		a.eye.Reset()
		a.eyeKey, a.eyeRes, a.eyeOK, a.eyeFed, a.eyeOff = key, EyeResult{}, false, frameKey{}, 0
	}
	a.eyeCh = ch
	if seen(&a.eyeFed, keyOf(f)) {
		return // folded already
	}
	left, win := windowOf(a.g, s)
	stride := max(1, int(bit/f.SampleS/eyeFeedSPU))
	n := win / stride
	lo := int(math.Floor(left))
	if n > eyeMaxFed {
		lo += (n - eyeMaxFed) * stride / 2
		n = eyeMaxFed
	}
	codes := displayCodes(f, s, ch, lo, n, stride)
	before := a.eyeRes.Records
	a.eye.Feed(codes, f.SampleS*float64(stride), bit)
	r := a.eye.Result()
	// Result's grid is the eye's buffer: the view keeps a copy of its own,
	// new only when a capture was folded.
	if r.Records != before || a.eyeRes.Grid == nil {
		r.Grid = append([]uint32(nil), r.Grid...)
	} else {
		r.Grid = a.eyeRes.Grid
	}
	switch {
	case r.Records != before:
		a.eyeOff = 0
	case r.LastErr == analysis.EyeUIInconsistent:
		a.eyeOff++
	}
	a.eyeRes = r
	a.eyeOK = r.Records > 0
	a.eyeWhy = ""
	if !a.eyeOK {
		a.eyeWhy = eyeWhyText(r.LastErr, ch)
	}
}

// displayCodes is n samples of a channel from lo, every stride-th, as the
// LCD draws them: the display coupling, the invert and the 2 and 5 mV
// detents' magnification (Q8.8 where the frame carries it).
// TRLC-LINKS: REQ-SDS-215
func displayCodes(f *engine.Frame, s *sv, ch, lo, n, stride int) []float64 {
	valid := framecalc.Valid(f)
	if ch == 1 && len(f.C2) < valid || n <= 0 {
		return nil
	}
	sig := framecalc.Channel(f, ch, valid)
	cpl := s.cpl[ch]
	if cpl != analog.CplDC {
		sig = analog.CoupleDisplay(sig, cpl)
	}
	q := f.Q1
	if ch == 1 {
		q = f.Q2
	}
	fine := cpl == analog.CplDC && len(q) >= valid
	z := float64(max(analog.Detents[s.vidx[ch]].Zoom, 1))
	out := make([]float64, 0, n)
	for k := 0; k < n; k++ {
		i := lo + k*stride
		if i < 0 || i >= valid {
			continue
		}
		v := float64(sig[i])
		if fine {
			v = float64(q[i]) / 256
		}
		if s.inv[ch] {
			v = 255 - v
		}
		out = append(out, 128+(v-128)*z)
	}
	return out
}

// dispVoltsPerCode is a channel's volts per displayed code at the tip.
// TRLC-LINKS: REQ-SDS-215
func dispVoltsPerCode(s *sv, ch int) float64 {
	return analog.Detents[s.vidx[ch&1]].VdivV / 25 * s.probe[ch&1]
}

// eyeClear is the Adjust push in the eye: start folding again.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeClear() {
	c.amu.Lock()
	if c.ana.eye != nil {
		c.ana.eye.Reset()
	}
	c.ana.eyeRes, c.ana.eyeOK, c.ana.eyeFed = EyeResult{}, false, frameKey{}
	c.amu.Unlock()
	c.toast("Eye cleared: folding again from the next capture", 1400*time.Millisecond)
}

// eyeThreshold is the threshold cursor in displayed codes: set by Adjust,
// else the crossing level, else mid-swing.
// TRLC-LINKS: REQ-SDS-215
func eyeThreshold(u *uiState, r EyeResult) float64 {
	if u.eyeThSet {
		return u.eyeTh
	}
	if !(r.Hi > r.Lo) {
		return 128
	}
	if !math.IsNaN(r.Crossing) {
		return r.Lo + r.Crossing*(r.Hi-r.Lo)
	}
	return (r.Lo + r.Hi) / 2
}

// eyeRowOf is the grid row of a code (row 0 = the top of the window).
// TRLC-LINKS: REQ-SDS-215
func eyeRowOf(r EyeResult, code float64) int {
	return clampInt(int((eyeCodeHi-code)/(eyeCodeHi-eyeCodeLo)*float64(r.Rows)), 0, max(r.Rows-1, 0))
}

// eyeColOf is the grid column of a time in UI from the window's left edge.
// TRLC-LINKS: REQ-SDS-215
func eyeColOf(r EyeResult, ui float64) int {
	return clampInt(int(math.Round(ui/2*float64(r.Cols))), 0, max(r.Cols-1, 0))
}
