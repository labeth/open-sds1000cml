// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/analysis"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/frames"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/sramcapture"
)

// BUS (REQ-SDS-214): entering it with decode off finds the bus and decodes
// it; the pane lists the complete packets as a fixed-column hex dump with a
// separate string box, held still while the same messages repeat. Adjust
// selects a packet, its push picks bytes to trigger on; Position moves the
// trigger, and its push switches it to jumping match to match (pushed again,
// back to the trigger); the S/DIV push zooms onto the selected packet. BUS again shows the eye diagram (REQ-SDS-215).

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
// TRLC-LINKS: REQ-SDS-214
func eyeRows() int { return (480 - wsDefs[wsview.Bus].plotTop - 12) / 2 }

// busDecoding reports a bus being decoded (decode not off).
// TRLC-LINKS: REQ-SDS-214
func busDecoding(s *sv) bool { return s.decProto != framecalc.ProtoOff }

// busListKey is what the packet list belongs to: a change starts it over.
// SPI's data delay counts as set: Auto's, re-measured on every full-rate
// record, moved by a fraction of a nanosecond and started the list over and
// ended BUS's full-rate lease mid-read (a 10 MHz bus at 10us/div decoded
// only the frames that happened to be full-rate).
// TRLC-LINKS: REQ-SDS-214
func busListKey(s *sv) string {
	delay := any(s.decDelayNs)
	if s.decDelayAuto {
		delay = "auto"
	}
	return fmt.Sprint(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA, s.decLSB, s.st.TdivS, delay,
		s.decLayer, s.decLayerAddr, s.decLayerDir, s.decParity, s.decInv)
}

// busFrame decodes the frame and offers its packets to the held list.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) busFrame(a *anaState, s *sv, f *engine.Frame) {
	if s.decProto == framecalc.ProtoOff {
		a.res, a.resOK, a.live, a.rawKey = decode.Result{}, false, nil, ""
		c.endBusRaw()
		return
	}
	a.res = framecalc.Decode(f, decodeParamsOf(s))
	c.busRawFrames(a, s, f)
	a.resOK = a.res.OK
	c.noteSPIEye(s, f)
	if !a.resOK {
		a.live = nil
		return
	}
	a.live = layerPackets(s, a.res.Proto, anaPackets(a.res, a.g.valid))
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

// busHits counts the rows the match tokens hit: the rows and a frame's
// packets are replaced whole, never changed in place, so the count holds
// for the same rows and tokens (the caption asks at every display tick).
type busHits struct {
	rows    *Packet
	n, hits int
	toks    string
	have    bool
}

// matchHits is how many of rows hold the match tokens (c.amu).
// TRLC-LINKS: REQ-SDS-214
func (a *anaState) matchHits(rows []Packet, toks []string) int {
	var first *Packet
	if len(rows) > 0 {
		first = &rows[0]
	}
	key := strings.Join(toks, " ")
	if h := a.hits; h.have && h.rows == first && h.n == len(rows) && h.toks == key {
		return h.hits
	}
	n := 0
	for i := range rows {
		if tokMatch(toks, rows[i]) {
			n++
		}
	}
	a.hits = busHits{rows: first, n: len(rows), hits: n, toks: key, have: true}
	return n
}

// busMarks are the strip's packet marks for a frame's packets, the
// selected one and the match tokens, built once for them; gen numbers each
// build.
type busMarks struct {
	live   *Packet
	n, sel int
	toks   string
	marks  []wsview.PacketMark
	gen    uint64
	have   bool
}

// marksGen numbers the strip's mark builds (wsview.BusPane.MarksGen).
var marksGen atomic.Uint64

// packetMarks are the strip's marks of live: the selected packet (liveSel)
// boxed, those holding the match tokens ticked (c.amu). The slice is shared
// and must not be changed.
// TRLC-LINKS: REQ-SDS-214
func (a *anaState) packetMarks(live []Packet, liveSel int, toks []string) ([]wsview.PacketMark, uint64) {
	var first *Packet
	if len(live) > 0 {
		first = &live[0]
	}
	key := strings.Join(toks, " ")
	if m := a.marks; m.have && m.live == first && m.n == len(live) && m.sel == liveSel && m.toks == key {
		return m.marks, m.gen
	}
	var marks []wsview.PacketMark
	for i, q := range live {
		marks = append(marks, wsview.PacketMark{I0: q.I0, I1: q.I1, Sel: i == liveSel, Match: len(toks) > 0 && tokMatch(toks, q),
			Text: q.Note, Err: q.NoteErr})
	}
	a.marks = busMarks{live: first, n: len(live), sel: liveSel, toks: key, marks: marks, gen: marksGen.Add(1), have: true}
	return marks, a.marks.gen
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
// SOF is the packet's start: the units after it open the packet. CMD is a
// MIL-1553 message's command word, its first (two bytes); it opens the packet.
// TRLC-LINKS: REQ-SDS-214
func tokMatch(toks []string, p Packet) bool {
	if len(toks) > 0 && (toks[0] == "SOF" || toks[0] == "CMD") {
		skip := 0
		if toks[0] == "CMD" {
			if !p.HasAddr || len(p.Bytes) < 2 {
				return false
			}
			skip = 2
		}
		if len(toks) == 1 {
			return len(p.Bytes) > 0
		}
		q := p
		q.Bytes = p.Bytes[skip:]
		if len(p.ByteErr) > skip {
			q.ByteErr = p.ByteErr[skip:]
		} else {
			q.ByteErr = nil
		}
		return tokMatchAt(toks[1:], q, 0)
	}
	n := len(toks)
	if n == 0 || n > len(p.Bytes) {
		return false
	}
	for i := 0; i+n <= len(p.Bytes); i++ {
		if tokMatchAt(toks, p, i) {
			return true
		}
	}
	return false
}

// tokMatchAt reports whether the match tokens hold at byte i of a packet.
// TRLC-LINKS: REQ-SDS-214
func tokMatchAt(toks []string, p Packet, i int) bool {
	n := len(toks)
	if n == 0 || i+n > len(p.Bytes) {
		return false
	}
	for j := 0; j < n; j++ {
		bad := i+j < len(p.ByteErr) && p.ByteErr[i+j]
		if !tokByte(toks[j], p.Bytes[i+j], bad) {
			return false
		}
	}
	return true
}

// tokByte reports whether one match token holds a byte (bad: decoded with
// an error): ERR, STS (a MIDI status byte) or two hex digits, ? any.
// TRLC-LINKS: REQ-SDS-214
func tokByte(t string, b byte, bad bool) bool {
	if t == "ERR" {
		return bad
	}
	if bad {
		return false
	}
	if t == "STS" {
		return b >= 0x80
	}
	const d = "0123456789ABCDEF" // hex2's digits, without its string
	return len(t) == 2 && (t[0] == '?' || t[0] == d[b>>4]) && (t[1] == '?' || t[1] == d[b&15])
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
	if !busDecoding(s) {
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
	if !busDecoding(s) {
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

// busToTrigger is Position leaving BUS's next match: the packet list back
// to the trigger's packet and a zoom window back on the trigger (the
// trigger position stays where it was set).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) busToTrigger(s *sv) {
	if s.zoom > 1 {
		c.setZoomOff(0)
	}
	c.amu.Lock()
	c.ana.list.Select(0)
	c.amu.Unlock()
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

// resultProto is the decoder a detection names (0: none).
// TRLC-LINKS: REQ-SDS-214
func resultProto(res decode.Result) int {
	if !res.OK {
		return 0
	}
	return framecalc.ProtoOf(res.Proto)
}

// fillIn writes a detected bus into the decoder settings, as if entered by
// hand: protocol, lines, rate, SPI mode and bit order. A stream running on the
// bus before ends: it decoded another. It reports whether the result named a
// bus.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (c *Controller) fillIn(res decode.Result) bool {
	p := resultProto(res)
	if p == 0 {
		return false
	}
	c.mu.Lock()
	if p == framecalc.ProtoSPI && c.decProto != framecalc.ProtoSPI {
		// a new SPI bus samples its data at the middle of the eye measured on
		// it, as AUTO in BUS does (forgetBus): a fixed delay kept from before,
		// or Default setup's 0 ns, sat on a fast bus's data edges
		c.decDelayAuto, c.spiEye, c.spiEyeKey, c.spiEyeVotes = true, decode.SPIEye{}, spiEyeK{}, decode.SPIEyeVotes{}
	}
	c.decProto = p
	c.decGen++
	c.decChA, c.decChB = res.Roles.ChA&1, res.Roles.ChB&1
	if c.decChB == c.decChA {
		c.decChB = 1 - c.decChA
	}
	if res.Baud > 0 {
		c.decBaud = res.Baud
		if rateProto(p) {
			c.decBaud = standardRate(res.Baud)
		}
	}
	if p == framecalc.ProtoSPI {
		c.decCPOL, c.decCPHA, c.decLSB = res.Roles.CPOL, res.Roles.CPHA, !res.Roles.MSB
	}
	c.decParity, c.decInv, c.decBits = res.Parity, res.Inverted, res.FrameBits
	if res.Baud <= 0 && !rateProto(p) && p != framecalc.ProtoUART {
		c.decBaud = defaultRate(p) // the hardware matcher's rate
	}
	c.auto = autoWatch{filled: true, good: c.now()}
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

// detectOnScreen runs the detection on the frame on screen. Not for use
// inside OnFrame.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (c *Controller) detectOnScreen() (decode.Result, float64, bool) {
	if c.frameFn == nil {
		return decode.Result{}, 0, false
	}
	f := c.frameCopy(searchable)
	if f == nil {
		return decode.Result{}, 0, false
	}
	var res decode.Result
	atSearchPriority(func() { res = framecalc.Detect(f, "hex") })
	return res, f.SampleS, res.OK && res.Proto != "" && res.Proto != "off"
}

// searchable reports a frame a bus search or a decode can read: samples in
// time order, enough of them.
// TRLC-LINKS: REQ-SDS-214
func searchable(f *engine.Frame) bool {
	return f != nil && !f.IsEnv && !(f.PeakDetect && !f.Ordered) && len(f.C1) >= 32
}

// frameCopy is a copy of the frame on screen when ok accepts it (nil
// otherwise), for long work: a bus search or a decode of the record run on
// the snapshot itself would hold the fan-out's read lock meanwhile, and with
// it the next snapshot and every reader after it (the LCD, the web page's
// status) froze for as long as the search took (over a minute on a busy line
// on the scope's ARM). Not for use inside OnFrame.
// TRLC-LINKS: REQ-SDS-020, REQ-SDS-214
func (c *Controller) frameCopy(ok func(*engine.Frame) bool) *engine.Frame {
	var cp *engine.Frame
	c.frameFn(func(f *engine.Frame) {
		if ok(f) {
			cp = new(engine.Frame)
			frames.CopyFrame(cp, f)
		}
	})
	return cp
}

// decodeOnScreen decodes the frame on screen with the decoder settings. Not
// for use inside OnFrame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func (c *Controller) decodeOnScreen() (decode.Result, float64, bool) {
	if c.frameFn == nil {
		return decode.Result{}, 0, false
	}
	p := decodeParamsOf(c.snap())
	f := c.frameCopy(searchable)
	if f == nil {
		return decode.Result{}, 0, false
	}
	res := framecalc.Decode(f, p)
	return res, f.SampleS, res.OK && len(res.Bytes) > 0
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
// decoded frame's sample interval. within, when above 0, steps to the slowest
// timebase up to it that reads the bus even when the current one does (a bus
// found on a short record shows more of itself there). arming: a serial
// trigger is armed next, so its window counts though none is armed yet (AUTO
// arms the frame start after this: 9600 Bd Modbus passed at 5ms/div on 10
// divisions and its live view, on 12.5, could not decode it). It returns
// what the notice adds ("" when the timebase stays).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) readableTimebase(res decode.Result, sampleS, within float64, arming bool) string {
	feature := decode.EnvelopeFeatureSamples(res) * sampleS
	if !(feature > 0) || len(c.tdivs) == 0 {
		return ""
	}
	s := c.snap()
	window := 10.0
	if s.st.SerialMode != 0 || arming {
		window = 12.5
	}
	points := func(tdiv float64) float64 { return feature / (window * tdiv / liveEnvelopePoints) }
	// the timebase asked for: one set while a stream ran applies when it ends
	if within <= 0 && points(s.st.TdivSet()) >= framecalc.MinEnvelopeFeature {
		return ""
	}
	best := -1
	for i, t := range c.tdivs {
		if points(t) >= readableFeature && (best < 0 || t > c.tdivs[best]) && (within <= 0 || t <= within*1.001) {
			best = i
		}
	}
	switch {
	case best < 0 && within > 0, best >= 0 && within > 0 && c.tdivs[best] == s.st.TdivSet():
		return ""
	case best < 0:
		best = 0
	}
	if c.setTdivIdx(best) != nil {
		return ""
	}
	return ", " + si(c.tdivs[best], "s") + "/div to read it"
}

// look is one phase of a bus search: auto's result, the decoded frame's
// sample interval, whether it named a bus and, for a decimated record,
// whether the record resolves every edge it holds (resolved).
// TRLC-LINKS: REQ-SDS-214
type look struct {
	res      decode.Result
	sampleS  float64
	found    bool
	resolved bool
	runs     [2]int     // a decimated record's shortest run between edges per channel (decode.ShortestRun)
	envRun   [2]float64 // the live envelope's bound on each channel's edge spacing where busy, seconds (decode.EnvelopeRun; 0: none)
	envBusy  [2]float64 // the share of the envelope's buckets each channel swings across
	envSeen  bool       // a live envelope of the timebase was read
}

// detectOn waits for a sampled frame of the timebase on screen and runs auto
// (Auto on the frame on screen) on it. It asks first for a decimated record -
// a few thousand filtered samples a screen, read in tens of milliseconds - and
// keeps what auto finds there when it stands (decimatedStands); else a
// full-rate record (bounded by detectWait, falling back to the frame on
// screen at the end) when that is short and can show more: a megasample at
// 200us/div, seconds to read, made BUS take 8 s to name a UART, and the
// search at the full-rate timebase reads a short record instead
// (findAtFullRate). It returns the result, the decoded frame's sample
// interval, whether a bus was found and the decimated record's look.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) detectOn(auto AutoSource) (decode.Result, float64, bool, look) {
	// A frame spanning the screen of the timebase set, not one still in
	// flight from a faster one before: AUTO's search at 500us/div read its
	// sweep's 5us/div frame (54 us, one SWD transfer cut) and named SPI, one
	// search in ten.
	set := c.snap().st.TdivSet()
	all := func(f *engine.Frame, _ bool) bool {
		return set <= 0 || !(f.SampleS > 0) || float64(f.Valid)*f.SampleS >= 5*set
	}
	dec := c.detectWith(true, false, all, auto, decimatedWait)
	res, sampleS, found := dec.res, dec.sampleS, dec.found
	if c.weighEnvelope(&dec, all) {
		return res, sampleS, true, dec
	}
	// A short screen fits the sample lease's budget undecimated: that record
	// was the full-rate one already, and reading it again finds nothing new.
	if full := c.fullRateS(); full > 0 && sampleS > 0 && sampleS <= 1.25*full {
		return res, sampleS, found, dec
	}
	// A record that resolves its every edge shows at the full rate what it
	// showed (a UART's bits at 10us/div); a long one costs more than the
	// search at the full-rate timebase.
	if w := c.windowTdiv(); dec.resolved || (w > 0 && c.snap().st.TdivSet() > w*1.001) {
		return res, sampleS, false, dec
	}
	// the full-rate record, not a decimated one still in flight
	full := c.fullRateS()
	raw := func(f *engine.Frame, decimated bool) bool { return decimated || full <= 0 || f.SampleS <= 1.25*full }
	l := c.detectWith(false, false, raw, auto, detectWait)
	return l.res, l.sampleS, l.found, dec
}

// decimatedWait bounds the wait for a decimated record: a scope waiting for
// a trigger captures none.
const decimatedWait = 1500 * time.Millisecond

// decimatedFeature is the samples of a decimated record a bus's shortest
// feature must span for a detection on it to stand (a 115200 Bd UART at
// 200us/div spans 35); a faster bus is looked at at the full rate.
// decimatedCheckedFeature is the bound for a bus found by its frames' checks
// (CAN's CRC-15, FlexRay's header CRC-11, Modbus's CRC-16, J1850's CRC-8,
// USB PD's CRC-32, the LIN and DHT checksums, Wiegand's parities; an SD
// card's CRC-7, PJON's CRC-8, Oregon's and Qi's checksums, the parities of
// the RFID tags, DCF77, PDI and SWIM, Maple's check byte) or by a
// handshake (1-Wire's reset and presence pulse), which a misread record does
// not pass: a 500 kbit/s CAN bus at 200us/div spans 7.8, a 1-Wire slot's 15
// us at 500us/div 6.1, a Wiegand pulse at 20ms/div 6.1.
const (
	decimatedFeature        = 8.0
	decimatedCheckedFeature = 6.0
)

// decimatedStands reports whether a bus found on a decimated record stands
// (decimatedFeature, decimatedCheckedFeature). A UART at a rate no UART uses
// does not: a faster bus the record aliases reads as one (1 Mbit/s
// Manchester sampled every 1 us: "UART 32917 8N1").
// TRLC-LINKS: REQ-SDS-214
func decimatedStands(res decode.Result) bool {
	fs := decode.FeatureSamples(res)
	switch {
	case checkedFind(res):
		return fs >= decimatedCheckedFeature
	case res.Proto == "uart":
		return fs >= decimatedFeature && decode.StandardBaud(float64(res.Baud))
	}
	return fs >= decimatedFeature
}

// checkedFind reports a bus found by its frames' checks (CAN's CRC-15,
// FlexRay's header CRC-11, Modbus's CRC-16, J1850's CRC-8, USB PD's CRC-32,
// the LIN and DHT checksums, Wiegand's parities) or by a handshake (1-Wire's
// reset and presence pulse), which a misread record does not pass.
// TRLC-LINKS: REQ-SDS-214
func checkedFind(res decode.Result) bool {
	switch res.Proto {
	case "canfd", "flexray", "modbus", "j1850", "usbpd", "lin", "dht", "wiegand", "onewire", "sdcard", "pjdl", "ook",
		"em4100", "em4305", "maple", "dcf77", "qi", "pdi", "swim":
		return true
	}
	return false
}

// detectWith is one phase of detectOn: a decimated record (LeaseSampleView)
// or a full-rate one (LeaseDecodeView), within wait. The lease ends with the
// phase: a full-rate lease left running outranks the next phase's (the
// search at 200us/div waited 4 s for a megasample instead of a decimated
// record), and the live view comes back at once. stepped says accept takes
// only frames of a timebase the search stepped to, captured after it: no
// frame in flight need be waited out.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) detectWith(decimated, stepped bool, accept func(*engine.Frame, bool) bool, auto AutoSource, wait time.Duration) look {
	t0 := c.now()
	kind := "full-rate"
	if decimated {
		kind = "decimated"
	}
	deadline := t0.Add(wait)
	loadEnd := deadline.Add(imageWait) // the longest an image load holds the wait
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
	fresh := func(f *engine.Frame) bool { return stepped || start == 0 || f.Seq >= start+2 }
	lease := func(d time.Duration) {
		if decimated {
			if e, ok := c.eng.(interface{ LeaseSampleView(time.Duration) }); ok {
				e.LeaseSampleView(d)
			}
		} else {
			c.leaseRaw(rawForSearch, d)
		}
	}
	defer lease(0)
	var envRun, envBusy [2]float64
	envSeen := false
	if decimated && !stepped { // the frame on screen, no wait (a find that needs one reads it, weighEnvelope)
		envRun, envBusy, envSeen = c.envelopeRuns(false, accept, deadline)
	}
	for {
		lease(3 * time.Second)
		got, usable, resolved := false, false, false
		var runs [2]int
		if c.frameFn != nil {
			c.frameFn(func(f *engine.Frame) {
				usable = f != nil && !f.IsEnv && !(f.PeakDetect && !f.Ordered) && f.Valid >= 32 && len(f.C1) >= f.Valid
				got = usable && !f.PeakDetect && accept(f, decimated) && fresh(f)
				if got && decimated && len(f.C2) >= f.Valid {
					runs = [2]int{decode.ShortestRun(f.C1[:f.Valid]), decode.ShortestRun(f.C2[:f.Valid])}
					resolved = runsResolved(runs[0]) && runsResolved(runs[1])
				}
			})
		}
		// No capture comes while an FPGA image loads (four seconds after DEFAULT
		// turned a pulse or serial trigger back to an edge), and the frame on
		// screen from before it is no sample of the bus now: the wait starts
		// after the load (a UART streamed before was found on an I2C bus).
		if !got && strings.HasPrefix(c.eng.Snapshot().Busy, "loading") {
			if d := c.now().Add(wait); d.After(deadline) {
				deadline = minTime(d, loadEnd)
			}
		}
		if c.searchDropped() {
			return look{}
		}
		last := c.detectSync || !c.now().Before(deadline)
		if got || (usable && last && !decimated) {
			t1 := c.now()
			res, sampleS, found := auto()
			// The search's steps in the log: where AUTO's seconds went.
			c.logf("panel: bus search: %s look at %s/div waited %v, searched %v: %s", kind, si(c.snap().st.TdivSet(), "s"),
				t1.Sub(t0).Round(time.Millisecond), c.now().Sub(t1).Round(time.Millisecond), map[bool]string{true: res.Proto, false: "nothing"}[found])
			return look{res, sampleS, found, resolved, runs, envRun, envBusy, envSeen}
		}
		if last {
			c.logf("panel: bus search: %s look: no capture in %v", kind, c.now().Sub(t0).Round(time.Millisecond))
			return look{}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// envWait bounds the wait for the live envelope of a timebase the search
// stepped to: its first capture there, tens of milliseconds.
const envWait = 400 * time.Millisecond

// envelopeRuns reads the live envelope of a timebase (decode.EnvelopeRun per
// channel: the bound on its busy runs, and the share of buckets it is busy
// in): the edges of a line faster than a decimated record's samples show
// there, and nowhere in the record. wait waits (bounded by envWait and
// deadline) for the timebase's first envelope, a capture with no sample
// lease; else only the frame on screen is looked at. seen is false (and the
// figures zeros) when none is seen.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) envelopeRuns(wait bool, accept func(*engine.Frame, bool) bool, deadline time.Time) (runs, busy [2]float64, seen bool) {
	if c.frameFn == nil {
		return runs, busy, false
	}
	end := minTime(deadline, c.now().Add(envWait))
	for {
		c.frameFn(func(f *engine.Frame) {
			if f == nil || !f.PeakDetect || !f.Ordered || f.Valid < 64 || len(f.C1) < f.Valid || len(f.C2) < f.Valid || !accept(f, true) {
				return
			}
			seen = true
			for ch, codes := range [2][]uint8{f.C1[:f.Valid], f.C2[:f.Valid]} {
				runs[ch], busy[ch] = decode.EnvelopeRun(codes, f.SampleS)
			}
		})
		if seen || !wait || c.detectSync || !c.now().Before(end) {
			return runs, busy, seen
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// shortRunShare is the share of a find's shortest feature a run of its bus
// lines must stay under for shortRuns: the flash read as I2C held runs of 1
// or 2 samples on its data and 7 on its clock against a 14-sample half clock.
const shortRunShare = 0.75

// shortRuns reports a find on a decimated record with a line of its bus
// holding runs under shortRunShare of its shortest feature
// (decode.ShortestRun): a glitch, or a faster bus the record averaged.
// TRLC-LINKS: REQ-SDS-214
func shortRuns(l look) bool {
	fs := decode.FeatureSamples(l.res)
	for _, ch := range busLines(l.res) {
		if r := l.runs[ch]; r > 0 && float64(r) < shortRunShare*fs {
			return true
		}
	}
	return false
}

// clockFromRecord reports a bus whose decoder takes its clock from the
// record (SPI, I2C): a decimated record of a faster bus reads as one at a
// clock the record aliases.
// TRLC-LINKS: REQ-SDS-214
func clockFromRecord(res decode.Result) bool {
	return res.Proto == "spi" || res.Proto == "i2c"
}

// slowAlias reports a slow look's find (res, on a record sampled every
// sampleS) that is the alias of a faster find whose shortest feature was
// priorFeat seconds (0: none): a clocked bus (clockFromRecord) over
// aliasRatio times coarser.
// TRLC-LINKS: REQ-SDS-214
func slowAlias(res decode.Result, sampleS, priorFeat float64) bool {
	return priorFeat > 0 && clockFromRecord(res) && decode.FeatureSamples(res)*sampleS > aliasRatio*priorFeat
}

// aliasRatio is how much coarser than a faster find's shortest feature a
// slow look's clocked find may be before it is taken for that find's alias.
const aliasRatio = 4.0

// busLines are the channels a found bus uses: its line, and SDA or data.
// TRLC-LINKS: REQ-SDS-214
func busLines(res decode.Result) []int {
	lines := []int{res.Roles.ChA & 1}
	if res.Proto == "i2c" || res.Proto == "spi" {
		lines = append(lines, res.Roles.ChB&1)
	}
	return lines
}

// weighEnvelope is whether a find on a decimated record stands once a line of
// its bus holds runs under its shortest feature (shortRuns): weighed
// against the live envelope (envHides), read now when the look holds none.
// A find no envelope could be read for does not stand: the search goes on
// at the full rate (the 104 MHz flash read as I2C, with no envelope after
// AUTO's sweep). A checked find (checkedFind) stands without: no faster bus
// averaged passes its frames' checks, and a USB PD bus, its half bits a run
// or two under the feature now and then, went on to a full-rate record
// read as Manchester.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) weighEnvelope(l *look, accept func(*engine.Frame, bool) bool) bool {
	if !stands(*l) || !shortRuns(*l) || checkedFind(l.res) {
		return stands(*l)
	}
	if !l.envSeen {
		l.envRun, l.envBusy, l.envSeen = c.envelopeRuns(true, accept, c.now().Add(envWait))
	}
	return l.envSeen && stands(*l)
}

// stands reports whether a bus found on a decimated record stands: enough
// samples for its shortest feature (decimatedStands), and no line it uses
// showing faster edges in the record (hidesFaster) or, unless its frames'
// checks passed (checkedFind), the live envelope (envHides).
// TRLC-LINKS: REQ-SDS-214
func stands(l look) bool {
	return l.found && decimatedStands(l.res) && !hidesFaster(l) && (checkedFind(l.res) || !envHides(l))
}

// envHidesRatio is how much shorter than a found bus's shortest feature the
// live envelope's run (decode.EnvelopeRun) must be for the bus not to stand:
// a line of that bus changes no faster than its feature, so the mean spacing
// of its edges where busy is at least the feature (a 115200 Bd UART at
// 1ms/div: 9.7 us buckets, 8.7 us bits).
const envHidesRatio = 0.5

// envHides reports a bus found on a decimated record on a line the live
// envelope shows changing far faster than the bus's shortest feature: the
// record averaged a faster bus into one it is not (a 104 MHz SPI flash at
// 200us/div read as I2C, its data swinging fully in every 0.98 us bucket of
// a burst - edges 1.3 us apart at most - against the I2C's 3.6 us half
// clock).
// TRLC-LINKS: REQ-SDS-214
func envHides(l look) bool {
	feature := decode.FeatureSamples(l.res) * l.sampleS
	if !(feature > 0) {
		return false
	}
	for _, ch := range busLines(l.res) {
		if r := l.envRun[ch]; r > 0 && r < envHidesRatio*feature {
			return true
		}
	}
	return false
}

// minTime is the earlier of two times.
// TRLC-LINKS: REQ-SDS-214
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// runsResolved reports whether a decimated record's channel resolves every
// edge it holds, from its shortest run (decode.ShortestRun): no run between
// two of them under twice decimatedFeature samples (or under three edges at
// all).
// TRLC-LINKS: REQ-SDS-214
func runsResolved(run int) bool {
	return run == 0 || run >= 2*decimatedFeature
}

// hidesFaster reports a UART found on a decimated record whose line holds
// runs well under its bit: a faster bus the record reads in part (a 500
// kbit/s CAN frame at 500us/div filled in as "UART 113413 8N1": its 2 us
// bits ran 3 samples, the UART's 14). A UART's line never changes within a
// bit.
// TRLC-LINKS: REQ-SDS-214
func hidesFaster(l look) bool {
	if l.res.Proto != "uart" || !(l.res.SPB > 0) {
		return false
	}
	run := l.runs[l.res.Roles.ChA&1]
	return run > 0 && float64(run) < 0.4*l.res.SPB
}

// findBus searches for a bus as BUS's auto-detect does: auto on a sampled
// frame and, when that record hid the bus, again at the slowest timebase
// sampled at the converters' full rate (findAtFullRate). A record hides a bus
// when it is decimated (a 10 Mbit/s FlexRay at 500us/div's 32 ns) or too
// short (Autoset's 10us/div holds about one UART byte). The timebase found
// there stays only while wanted (the caller still needs the bus: Auto still
// picked, a stream still asked for).
// One search runs at a time. It returns auto's result, the decoded frame's
// sample interval, whether a bus was found and the timebase it stepped to
// (0: none).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) findBus(auto AutoSource, wanted func() bool) (decode.Result, float64, bool, float64) {
	c.searchMu.Lock()
	defer c.searchMu.Unlock()
	c.searchOwn = c.autoSearch.Load()
	res, sampleS, found, dec := c.detectOn(auto)
	at := 0.0 // the timebase the search stepped to
	// A UART of a byte or two, or at a rate no UART uses, on a short record is
	// as likely part of another bus's frame (a CAN frame at 5us/div read as
	// "UART 500000 8N1", USB at 2us/div as "UART 1500000 8N1", a FlexRay frame
	// failing its CRC as "UART 10002156 8N1"): the search looks on, and names
	// it only when it finds nothing else.
	scant := found && scantFind(res)
	// a record resolving its edges is weighed against the live envelope (slowFirst)
	if !found && dec.resolved && !dec.envSeen {
		dec.envRun, dec.envBusy, dec.envSeen = c.envelopeRuns(true, func(*engine.Frame, bool) bool { return true }, c.now().Add(envWait))
	}
	// A record of the timebase on screen with no edges on either line (an IR
	// remote's 67 ms frame among its idle, at 500us/div), or one resolving
	// every edge it holds (a CEC bit of 2.4 ms), is looked for slower first:
	// the full-rate search reads seconds of the same idle. Not when the live
	// envelope shows a line busy faster than the record (a CAN or USB bus's
	// bursts, averaged away in the record).
	slowFirst := !found && dec.resolved && dec.envRun == [2]float64{} && !c.waitingForTrigger(c.snap())
	if slowFirst {
		if r, s, ok, stepped, _ := c.findSlowBus(auto, wanted, "", 0); ok {
			return r, s, true, stepped
		}
	}
	// A UART of a few bytes at a rate UARTs use, on a record resolving its bits,
	// is a frame of a slow bus read in part as often (a LIN frame, a Modbus
	// message): it is looked for slower first, and stands when the slow look
	// finds it again. The full-rate records hold no more of a bus that slow (a
	// 19200 Bd LIN bus read 5 s of them before the slow look named it).
	slowDone := false
	if found && fewUART(res) && !scant && !c.waitingForTrigger(c.snap()) {
		r, s, ok, stepped, again := c.findSlowBus(auto, wanted, res.Proto, decode.FeatureSamples(res)*sampleS)
		if ok {
			return r, s, true, stepped
		}
		if again {
			return res, sampleS, true, at
		}
		slowDone = true
	}
	// a scope waiting for a trigger captures nothing at the full rate either
	if full, t := c.fullRateS(), c.fullRateTdiv(); full > 0 && t > 0 && (!found || scant || weakFind(res)) && !c.waitingForTrigger(c.snap()) {
		var first *look // the timebase on screen is the full-rate one: its decimated record is read
		if math.Abs(c.snap().st.TdivSet()-t) <= 1e-3*t {
			first = &dec
		}
		r, s, ok, stepped := c.findAtFullRate(full, t, first, auto, wanted)
		if ok && !weakFind(r) {
			return r, s, true, stepped
		}
		if ok {
			res, sampleS, found, at = r, s, true, stepped
		}
	}
	// A UART of a few bytes may be a frame of a slow bus built on one (a LIN
	// frame of 10 ms, a Modbus RTU message at 9600 Bd) that a record of
	// milliseconds holds a part of, and an SPI byte or two part of a PS/2
	// frame the full-rate record cut: the slow look names it when it finds
	// another bus there, and keeps the first find otherwise.
	if (!found && !slowFirst || weakFind(res)) && !slowDone && !c.waitingForTrigger(c.snap()) {
		prior, priorFeat := "", 0.0
		if found {
			prior, priorFeat = res.Proto, decode.FeatureSamples(res)*sampleS
		}
		if r, s, ok, stepped, _ := c.findSlowBus(auto, wanted, prior, priorFeat); ok {
			return r, s, true, stepped
		}
	}
	return res, sampleS, found, at
}

// fewUARTBytes is the bytes a UART find must hold for the search to end on
// it (fewUART): a record of 5 ms holds four bytes of a 9600 Bd Modbus
// message, read as "UART 9600 8E1"; one of 50 ms holds whole messages.
const fewUARTBytes = 16

// searchDropped reports a bus search that gives way to an AUTO running, not
// AUTO's own: BUS entry's search on a front end Default setup left unfitted
// found nothing in nine seconds, and AUTO waited for it before fitting the
// vertical - also when it began just after the press. Read by the search
// itself (under searchMu).
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223
func (c *Controller) searchDropped() bool {
	if c.searchOwn {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.autosetBusy
}

// weakFind reports a find the search looks on from, slower too: a UART of a
// few bytes (fewUART) or a scant find (scantFind).
// TRLC-LINKS: REQ-SDS-214
func weakFind(res decode.Result) bool {
	return fewUART(res) || scantFind(res)
}

// fewUART reports a UART find of under fewUARTBytes bytes.
// TRLC-LINKS: REQ-SDS-214
func fewUART(res decode.Result) bool {
	return res.Proto == "uart" && len(res.Bytes) < fewUARTBytes
}

// slowTdivs are the timebases a bus found nowhere faster is looked for at:
// the buses whose frames last tens of milliseconds (IR remotes, HDMI-CEC,
// DALI, Wiegand, a DHT reading, LIN and Modbus frames) show whole frames
// there, in the decimated record of a search (LeaseSearchView) whose samples
// still resolve their bits: every 2 us at 5ms/div, every 8 us at 20ms/div,
// every 20 us at 50ms/div - whose 500 ms hold a message of over 100 ms
// whole wherever the trigger fell in it (an IR Denon pair of 133 ms, its
// second frame triggered on in every 20ms/div record of a search, the
// captures' cadence locked to the pair's).
var slowTdivs = []float64{5e-3, 20e-3, 50e-3}

// findSlowBus is findBus's last look: the decimated record at each of
// slowTdivs, read as a search reads (LeaseSearchView: a deeper screen than
// the look on screen had, and whole records where the live view rolled).
// prior is the protocol of a weak find made faster ("" for none): found
// again here it does not count, and the look ends (again). priorFeat is
// that find's shortest feature in seconds (0 none): a bus taking its clock
// from the record (clockFromRecord) found here with a feature over
// aliasRatio times coarser is the record's alias of those faster edges (a
// 400 kHz I2C bus, found at the full rate, read at 20ms/div as "SPI" of a
// 223 us half clock). The timebase a bus is found at stays while wanted; with none
// found the timebase goes back.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) findSlowBus(auto AutoSource, wanted func() bool, prior string, priorFeat float64) (r decode.Result, sampleS float64, found bool, stepped float64, again bool) {
	prev := c.snap().st.TdivSet()
	e, searching := c.eng.(interface{ LeaseSearchView(time.Duration) })
	if searching {
		e.LeaseSearchView(10 * time.Second)
		defer e.LeaseSearchView(0)
	}
	// a Manchester line found faster may be a slower burst's middle (an
	// Oregon sensor's 100 ms message at 5 ms/div reads as Manchester or
	// RC-5), and a PWM line a DCF77 code's seconds without its minute
	// marker: held while the slowest look sees whether it is one
	var held *look
	heldAt := 0.0
	keep := func(l look, at float64) (decode.Result, float64, bool, float64, bool) {
		if !wanted() {
			c.setTdivNearest(prev)
			return l.res, l.sampleS, true, 0, false
		}
		c.setTdivNearest(at)
		return l.res, l.sampleS, true, at, false
	}
	for i, t := range slowTdivs {
		if len(c.tdivs) == 0 || slices.Max(c.tdivs) < t*0.999 {
			continue
		}
		c.setTdivNearest(t)
		at := c.snap().st.TdivSet()
		var seen uint64 // the newest frame a look here was offered
		for look := 0; look < slowLooks(i); look++ {
			after := seen
			// a frame of the search's own screen, not one the live view or
			// the look on screen read at the timebase (four times coarser),
			// and newer than the look before read
			accept := func(f *engine.Frame, decimated bool) bool {
				ok := (f.TdivS == 0 || math.Abs(f.TdivS-at) <= 1e-3*at) && (after == 0 || f.Seq > after) &&
					(!searching || f.SampleS <= 2*10*at/engine.SearchLiveSamples)
				if ok {
					seen = max(seen, f.Seq)
				}
				return ok
			}
			// a screen of slow frames takes its own time to capture
			l := c.detectWith(true, true, accept, auto, decimatedWait+time.Duration(20*at*float64(time.Second)))
			if l.found && decimatedStands(l.res) && l.res.Proto == prior {
				c.setTdivNearest(prev)
				return decode.Result{}, 0, false, 0, true // the bus found faster, again
			}
			if l.found && slowAlias(l.res, l.sampleS, priorFeat) {
				continue // the faster find's edges, aliased
			}
			if l.found && decimatedStands(l.res) {
				if held != nil && !slices.Contains(heldFor(held.res.Proto), l.res.Proto) {
					return keep(*held, heldAt)
				}
				if held == nil && i < len(slowTdivs)-1 && len(heldFor(l.res.Proto)) > 0 {
					held, heldAt = &l, at
					break
				}
				return keep(l, at)
			}
		}
	}
	if held != nil {
		return keep(*held, heldAt)
	}
	c.setTdivNearest(prev)
	return decode.Result{}, 0, false, 0, false
}

// heldFor is the slower buses a find may be part of (findSlowBus): an
// Oregon burst's middle, or an EM4100 tag's 64-bit frame cut, reads as a
// Manchester line or RC-5, a DCF77 code's seconds as PWM; nil for none.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-256
func heldFor(proto string) []string {
	switch proto {
	case "manchester", "irrc5", "irrc6":
		return []string{"ook", "em4100"}
	case "pwm":
		return []string{"dcf77"}
	}
	return nil
}

// slowLooks is how many records findSlowBus reads at the i-th of slowTdivs:
// two at the slower ones, where a bus that sends a frame now and then (an
// IR remote's 67 ms frame among repeat codes, a card read) can fall across
// the record's end on one record and whole in the next.
// TRLC-LINKS: REQ-SDS-214
func slowLooks(i int) int {
	if i > 0 {
		return 2
	}
	return 1
}

// findAtFullRate is findBus's search at the slowest full-rate timebase t
// (full the full rate's sample interval): its record decimated (first, when
// read already), a short full-rate record (windowTdiv), then its whole record
// at the full rate. The timebase the bus is found at stays while wanted, and
// is returned (0: the timebase went back); with no bus found it goes back.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) findAtFullRate(full, t float64, first *look, auto AutoSource, wanted func() bool) (decode.Result, float64, bool, float64) {
	prev := c.snap().st.TdivSet()
	at := func(t float64) func(*engine.Frame, bool) bool {
		return func(f *engine.Frame, decimated bool) bool {
			return (decimated || f.SampleS <= 1.25*full) && (f.TdivS == 0 || math.Abs(f.TdivS-t) <= 1e-3*t)
		}
	}
	keep := func(res decode.Result, sampleS, stepped float64) (decode.Result, float64, bool, float64) {
		if wanted() {
			if math.Abs(stepped-prev) <= 1e-3*stepped {
				stepped = 0 // the timebase on screen
			}
			return res, sampleS, true, stepped
		}
		c.setTdivNearest(prev)
		return res, sampleS, true, 0
	}
	// the whole record decimated: a slow bus, or one with long frames
	var dec look
	if first != nil {
		dec = *first
	} else {
		c.setTdivNearest(t)
		// A find with a line holding runs under its shortest feature is
		// weighed against the live envelope first, one capture more (the
		// 104 MHz flash read as I2C at 200us/div, its data's runs a sample or
		// two against a 14-sample half clock).
		if dec = c.detectWith(true, true, at(t), auto, decimatedWait); c.weighEnvelope(&dec, at(t)) {
			return keep(dec.res, dec.sampleS, t)
		}
	}
	// A bus the live envelope shows busy over much of the screen, faster than
	// the record resolves, is looked for on a shorter record still: the
	// 104 MHz flash's 2 ms screen holds hundreds of bursts, and its record at
	// windowTdiv took 8 s to read and process. The envelope is read for a
	// look that found nothing too (the flash's decimated record names no
	// bus: 16 s to find it on the window's record).
	if !dec.envSeen {
		dec.envRun, dec.envBusy, dec.envSeen = c.envelopeRuns(true, at(t), c.now().Add(envWait))
	}
	var dense *look // a few SPI bytes on the dense record: kept, should the window's show nothing
	denseAt := 0.0
	if d := c.denseTdiv(); d > 0 && denseFast(dec) {
		c.setTdivNearest(d)
		if l := c.detectWith(false, true, at(d), auto, detectWait); l.found && !fewDenseSPI(l.res) {
			return keep(l.res, l.sampleS, d)
		} else if l.found {
			dense, denseAt = &l, d
		}
		c.setTdivNearest(t)
	}
	// A bus too fast for it is looked at at the full rate on a short record
	// first: its frames are short, and the whole record takes seconds to
	// read.
	var cut *look // a find the short record may have cut a frame of: kept, should the whole record show nothing else
	if w := c.windowTdiv(); w > 0 && w < t {
		c.setTdivNearest(w)
		if l := c.detectWith(false, true, at(w), auto, detectWait); l.found && !usbPDRateManchester(l.res) {
			return keep(l.res, l.sampleS, w)
		} else if l.found {
			cut = &l
		}
		c.setTdivNearest(t)
	}
	if dense != nil {
		c.setTdivNearest(denseAt)
		return keep(dense.res, dense.sampleS, denseAt)
	}
	// a sparse bus: no frame on the short record
	l := c.detectWith(false, true, at(t), auto, detectWait)
	if l.found {
		return keep(l.res, l.sampleS, t)
	}
	if cut != nil {
		c.setTdivNearest(c.windowTdiv())
		return keep(cut.res, cut.sampleS, c.windowTdiv())
	}
	c.setTdivNearest(prev)
	return l.res, l.sampleS, false, 0
}

// scantUART reports a UART detection of under scantBytes bytes or at a rate
// no UART uses.
// TRLC-LINKS: REQ-SDS-214
func scantUART(res decode.Result) bool {
	return res.Proto == "uart" && (len(res.Bytes) < scantBytes || !decode.StandardBaud(float64(res.Baud)))
}

// scantFind reports a first find to look on from: a scant UART, or an SPI or
// I2C bus of under scantBytes bytes (Autoset's 5us/div holds one byte of the
// bench's 200 kHz SPI: BUS stayed there listing a single byte, where the
// search at the full-rate timebase shows its messages), or a single PS/2
// frame (a caliper's 24-bit reading at 200us/div reads as one).
// TRLC-LINKS: REQ-SDS-214
func scantFind(res decode.Result) bool {
	return scantUART(res) || (res.Proto == "spi" || res.Proto == "i2c") && len(res.Bytes) < scantBytes ||
		res.Proto == "ps2" && frameStarts(res) <= 1 || slowManchester(res) || usbPDRateManchester(res)
}

// usbPDRateManchester reports a Manchester line at USB Power Delivery's
// 300 kbit/s (within 10 %): a short record that cuts a PD message's CRC
// reads its biphase mark code as plain Manchester (a scant find, and held
// in findAtFullRate while the whole record is read).
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-256
func usbPDRateManchester(res decode.Result) bool {
	return res.Proto == "manchester" && res.Baud >= 270000 && res.Baud <= 330000
}

// slowManchester reports a Manchester line under 10 kbit/s: a tag's or a
// sensor's code (EM4100 at RF/64 is 1.95 kbit/s, an Oregon burst 1 kbit/s)
// the slow look may show whole.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-256
func slowManchester(res decode.Result) bool {
	return res.Proto == "manchester" && res.Baud > 0 && res.Baud < 10000
}

// frameStarts is the frames a decode opened ("sof" spans).
// TRLC-LINKS: REQ-SDS-214
func frameStarts(res decode.Result) int {
	n := 0
	for _, s := range res.Spans {
		if s.Kind == "sof" {
			n++
		}
	}
	return n
}

// scantBytes is the bytes a UART found on the search's first record must
// hold to end the search.
const scantBytes = 4

// windowDivs is how much shorter than the slowest full-rate timebase's the
// record a fast bus is first looked for on is: a quarter (50us/div: 500 us,
// read in under a second against 2 ms in about four). A tenth held too few
// frames: a FlexRay bus sending a pair every 240 us, one failing its CRC, read
// as a 10 MBd UART on one record in six.
const windowDivs = 4

// denseDivs is how much shorter than the slowest full-rate timebase's the
// record a dense fast bus is first looked for on is (denseFast): a
// twentieth (10us/div: 100 us, 50 k samples). denseBusy is the share of
// the envelope's buckets a line must be busy in for it: the flash's 0.85 and
// MIL-1553's 0.7 at 200us/div, not a USB LS bus's 0.45, whose frames the
// short record holds too few of (one read as a UART).
const (
	denseDivs = 20
	denseBusy = 0.6
)

// denseFast reports a decimated look whose live envelope shows a line both
// faster than the record resolves (decode.EnvelopeRun) and busy in at least
// denseBusy of its buckets: a short full-rate record holds its frames.
// TRLC-LINKS: REQ-SDS-214
func denseFast(l look) bool {
	for ch := range l.envRun {
		if l.envRun[ch] > 0 && l.envBusy[ch] >= denseBusy {
			return true
		}
	}
	return false
}

// denseSPIBytes is the SPI bytes a find on the dense record must hold to end
// the search there: an SWD bus's 100 us read as "SPI, 14 bytes" (its frames
// are whole on the window's record), the 104 MHz flash's as hundreds.
const denseSPIBytes = 32

// fewDenseSPI reports an SPI find of under denseSPIBytes bytes.
// TRLC-LINKS: REQ-SDS-214
func fewDenseSPI(res decode.Result) bool {
	return res.Proto == "spi" && len(res.Bytes) < denseSPIBytes
}

// denseTdiv is the timebase of the record a dense fast bus is first looked
// for on: the slowest at most a denseDivs-th of the full-rate timebase (0
// for none).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) denseTdiv() float64 {
	t := c.fullRateTdiv()
	best := 0.0
	for _, d := range c.tdivs {
		if d <= t/denseDivs*1.001 && d > best {
			best = d
		}
	}
	return best
}

// windowTdiv is the timebase of the short full-rate record a fast bus is
// first looked for on: the slowest at most a windowDivs-th of the full-rate
// timebase (0 for none).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) windowTdiv() float64 {
	t := c.fullRateTdiv()
	best := 0.0
	for _, d := range c.tdivs {
		if d <= t/windowDivs*1.001 && d > best {
			best = d
		}
	}
	return best
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

// smallSwingCodes is the least swing (codes) the detection slices
// (decode's minimum amplitude); smallSwingSeen the least it calls a signal.
const (
	smallSwingCodes = 20
	smallSwingSeen  = 4
)

// smallSwing names a channel whose signal swings too little at its V/div for
// the detection to slice it (a 3.3 V bus through a 10x probe set as 1x, as
// Default setup leaves it): "" when none does. The search found no bus, but
// "check the connection" would send the owner the wrong way.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) smallSwing() string {
	if c.frameFn == nil {
		return ""
	}
	note := ""
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.Valid <= 0 {
			return
		}
		for ch, codes := range [2][]uint8{f.C1, f.C2} {
			n := min(f.Valid, len(codes))
			if n == 0 {
				continue
			}
			var h [256]int
			for _, v := range codes[:n] {
				h[v]++
			}
			lo, hi := codePercentile(&h, n, 0.02), codePercentile(&h, n, 0.98)
			if sw := hi - lo; sw >= smallSwingSeen && sw < smallSwingCodes {
				note = fmt.Sprintf("no bus found: CH%d swings only %.1f div - AUTO (or V/DIV) brings it up", ch+1, float64(sw)/25)
				return
			}
		}
	})
	return note
}

// codePercentile is the code below which a share q of a histogram's n counts lie.
// TRLC-LINKS: REQ-SDS-214
func codePercentile(h *[256]int, n int, q float64) int {
	want, acc := int(q*float64(n)), 0
	for v := 0; v < 256; v++ {
		acc += h[v]
		if acc > want {
			return v
		}
	}
	return 255
}

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

// readTimebase steps to the timebase that reads a bus findBus found live
// (readableTimebase), and says where the timebase went: the step, or the
// full-rate timebase the search kept. arming: a serial trigger is armed next
// (AUTO's frame start), and its wider window counts already.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) readTimebase(res decode.Result, sampleS, stepped float64, arming bool) string {
	within := 0.0
	if t := c.fullRateTdiv(); stepped > 0 && stepped < t {
		within = t // found on the short full-rate record
	}
	if note := c.readableTimebase(res, sampleS, within, arming); note != "" || stepped == 0 {
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
	case framecalc.ProtoLIN, framecalc.ProtoModbus:
		return fmt.Sprintf("%s %d on C%d", protoName(proto), baud, chA+1)
	}
	if framecalc.TwoLine(proto) {
		return fmt.Sprintf("%s on C%d/C%d", protoName(proto), chA+1, chB+1)
	}
	if proto > framecalc.ProtoFlexRay {
		return fmt.Sprintf("%s on C%d", protoName(proto), chA+1)
	}
	if rateProto(proto) {
		return fmt.Sprintf("%s %s on C%d", protoName(proto), si(float64(baud), "b/s"), chA+1)
	}
	return protoName(proto)
}

// autoDecode is Auto: it detects the bus once and fills in the decoder
// settings (fillIn), which then decode it. It runs when Protocol is set to
// Auto (the LCD, the web, the phone, a recalled setup that holds Auto), when
// F2 enters BUS with decode off, on the Adjust push when nothing was found,
// and once by itself when the bus it found stops decoding (watchBus). The
// engine first sends sampled frames (detection cannot read an envelope), so
// the search runs off the panel goroutine.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
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
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (c *Controller) runDetect() {
	c.mu.Lock()
	gen, prev := c.decGen, c.decProto
	c.mu.Unlock()
	// A protocol set while the search ran (seconds at slow timebases) stands:
	// the search's lock, or its "decode stays off", must not undo a UART picked
	// on the phone meanwhile.
	mine := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.decGen == gen
	}
	res, sampleS, found, stepped := c.findBus(c.detectOnScreen, mine)
	if !mine() {
		auto := c.takeBusFrame() == busFrameAuto
		c.mu.Lock()
		c.ui.busDetecting = false
		if auto {
			c.ui.busNote = protoName(c.decProto) + " set during the search stays"
			c.toastLocked("AUTO: "+c.ui.busNote, busAutoNoticeFor)
		}
		c.mu.Unlock()
		c.syncStreamView()
		c.publish()
		return
	}
	locked := found && c.fillIn(res)
	tdivNote, none := "", ""
	frame := c.takeBusFrame()
	if locked {
		tdivNote = c.readTimebase(res, sampleS, stepped, frame != 0)
		if l := c.detectLayer(res, 0); l != "" { // a layer the packets prove
			tdivNote += "; layer " + l
		}
		if frame != 0 {
			tdivNote += "; " + c.frameBus(decode.EnvelopeFeatureSamples(res)*sampleS, frame == busFrameAuto)
		}
	} else if small := c.smallSwing(); small != "" {
		none = c.noBusNote(small)
	} else if prev == framecalc.ProtoOff {
		none = c.noBusNote("no bus found on CH1 or CH2 - decode stays off")
	} else { // the settings in use stay: a bus found again decodes with them
		none = c.noBusNote("no bus found on CH1 or CH2 - decode stays " + protoName(prev))
	}
	// AUTO in BUS says what it did for longer than BUS entry's detection: its
	// frame-start trigger may load an image after the search
	foundFor, noneFor := 2200*time.Millisecond, 2400*time.Millisecond
	if frame == busFrameAuto {
		foundFor, noneFor = busAutoNoticeFor, busAutoNoticeFor
	}
	c.mu.Lock()
	c.ui.busDetecting = false
	c.ui.busFailed = !locked && prev == framecalc.ProtoOff
	if locked {
		c.ui.busNote = c.busNameLocked() + tdivNote
		c.toastLocked("AUTO: "+c.ui.busNote+", decode on", foundFor)
	} else {
		c.ui.busNote = none
		c.toastLocked("AUTO: "+none, noneFor)
	}
	c.mu.Unlock()
	if !locked && prev == framecalc.ProtoOff {
		c.syncDecodeView(false)
	}
	c.amu.Lock()
	c.ana.list.Reset()
	c.ana.listKey = ""
	c.amu.Unlock()
	c.syncStreamView() // BUS's Stream view streams the bus found
	c.publish()
}

// ---- pickers ----

// packetToks are a packet's bytes as picker tokens.
// TRLC-LINKS: REQ-SDS-214
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
	c.ui.pick = pickFrom(toks, starts, sel, pickUnitOf(c.ana.res.Proto))
	c.ui.wpick, c.ui.posJob = true, false
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
		if f := c.frameCopy(func(f *engine.Frame) bool { return f != nil && len(f.C1) > 0 }); f != nil {
			res = framecalc.Decode(f, decodeParamsOf(s))
			valid = framecalc.Valid(f)
		}
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
	c.ui.pick = pickFrom(toks, starts, max(first, 0), pickUnitOf(res.Proto))
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
	return ch, bit, s.decProto == framecalc.ProtoSPI || s.decProto == framecalc.ProtoI2C, why
}

// eyeConfigOf is eyeConfig's data line and bit time.
// TRLC-LINKS: REQ-SDS-215
func eyeConfigOf(s *sv, a *anaState) (ch int, bit float64, why string) {
	if !busDecoding(s) {
		return 0, 0, "No bus decoded: F2 again for Packets, then push Adjust to find it"
	}
	res := a.res
	if !res.OK {
		return 0, 0, res.Error
	}
	proto, baud, chA, chB := s.decProto, s.decBaud, s.decChA, s.decChB
	switch {
	case proto == framecalc.ProtoUART || rateProto(proto) || baudProto(proto) || proto == framecalc.ProtoDMX || proto == framecalc.ProtoMIDI:
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
// eyeFeedSPU samples per UI and at most eyeMaxFed samples a capture. It folds
// full-rate raw frames only (Frame.FullRate), and leases them as the web's eye
// does: the live envelope, a decimated record or a filtered acquisition
// between them would fold another picture (a 200us/div envelope has 17.6
// samples per 115200 Bd UI, the raw record 4340) and start the eye over on
// each change. Any other frame leaves the eye as it is: it holds what it
// folded.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeFrame(a *anaState, s *sv, f *engine.Frame) {
	if !f.FullRate {
		// Not even its decode counts: the bit time of an envelope too coarse to
		// decode would take the eye away.
		switch {
		case !busDecoding(s):
			_, _, _, a.eyeWhy = eyeConfig(s, a)
			a.eyeOK = false
		case !a.eyeOK && (a.eyeFed == (frameKey{}) || a.eyeRes.LastErr == ""): // nothing folded since the eye started
			c.leaseEyeRaw()
			a.eyeWhy = c.eyeWaitWhy(s)
		default:
			c.leaseEyeRaw()
		}
		return
	}
	ch, bit, measured, why := eyeConfig(s, a)
	if why != "" {
		a.eyeWhy, a.eyeOK = why, false
		return
	}
	c.leaseEyeRaw()
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
	a.eyeAt = c.now()
	if !a.eyeOK {
		a.eyeWhy = eyeWhyText(r.LastErr, ch)
	}
}

// eyeHeldAfter is how long the eye goes without a full-rate capture, while
// running, before it says it is held: a raw 200us/div record takes about 3 s.
const eyeHeldAfter = 6 * time.Second

// The raw lease holders (leaseRaw): each lease is its holder's own.
const (
	rawForEye      = "eye"
	rawForSearch   = "bus search"
	rawForSuperres = "super-res"
	rawForBus      = "bus decode"
)

// busRawFrames trades the frame rate for the decode in BUS: a live view too
// coarse for the bus - an envelope (framecalc.Unresolved) or a roll view
// with too few samples a bit (a 115200 Bd UART at 10ms/div) - leases records
// sampled finely enough for the bus's settings (no envelope, no roll),
// renewed by each frame while they stay. Records sampled at a quarter to an
// eighth of the bus's shortest feature, as its last decode measured it,
// keep the frames as fast as they can be (the UART at 10ms/div: a quarter
// of the full rate's words); the full rate where that is unknown or too
// coarse. A change of the
// settings ends the lease, so their own live view is tried first; a
// full-rate record too coarse as well ends it for those settings, and the
// faster timebase is hinted.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) busRawFrames(a *anaState, s *sv, f *engine.Frame) {
	key, bus := busListKey(s), busFeatureKey(s)
	// The bus's feature as a decode measures it on a frame that resolves it
	// (readableFeature samples, as an envelope holds the feature on one), the
	// shortest measured for these settings: an alias is slower than the bus,
	// never faster - a coarse envelope's decode of a 10 MHz SPI bus held 'OK'
	// a feature microseconds long, and BUS sampled its records 4 us apart.
	resolved := decode.FeatureSamples(a.res)
	if f.PeakDetect {
		resolved = decode.EnvelopeFeatureSamples(a.res) // a Wiegand pulse: its bit interval
	}
	if a.res.OK && f.SampleS > 0 && resolved >= readableFeature {
		if fs := decode.FeatureSamples(a.res) * f.SampleS; fs > 0 && (a.featKey != bus || fs < a.featS) {
			a.featKey, a.featS, a.featEnv = bus, fs, decode.EnvelopeFeatureSamples(a.res)*f.SampleS
		}
	}
	// A clocked bus decoded at a clock over twice as slow as its measured one
	// read an alias: a 10 MHz SPI bus's record at 1ms/div (32 ns, a
	// clock of 3.1 samples, filtered) "decoded" at a slower clock. Refused as
	// the decoder refuses a record too coarse, so the lease and the hint
	// follow as for one.
	if aliasedClock(a, bus, f) {
		a.res = decode.Result{Proto: a.res.Proto, Error: fmt.Sprintf("%.1f cols/clock; too few samples/bit", 2*a.featS/f.SampleS)}
	}
	live := f.PeakDetect || s.st.Roll // a view the engine reads coarser than its record
	// Records sampled at a quarter of the feature, where the capture can
	// decimate that finely (busFinestDecimated); else the full rate.
	want := 0.0
	if a.featKey == bus && a.rawFull != key && a.featS/busSamplesPerFeature >= busFinestDecimated {
		want = a.featS / busSamplesPerFeature
	}
	// A sampled record judges the sampling it came with (one read under the
	// lease before can still arrive): one decimated past the timebase's own
	// record (the sampled lease's) too coarse asks for that record, the
	// timebase's own record too coarse ends the lease for these settings.
	own := uint32(1) << engine.PlanSRAM(s.st.TdivS).Log
	before := [3]string{a.rawKey, a.rawFull, a.rawNo}
	defer func() {
		// BUS's lease decisions in the log, as they change
		if now := [3]string{a.rawKey, a.rawFull, a.rawNo}; now != before {
			c.logf("panel: bus lease: sampled %v full-rate %v refused %v (frame %s a sample, decimation %d, live %v: %s) key %q was %q",
				a.rawKey == key, a.rawFull == key, a.rawNo == key, si(f.SampleS, "s"), f.Decimation, live, a.res.Error, key, before[0])
		}
	}()
	switch {
	case a.rawNo == key:
		a.rawKey = ""
	case live && (framecalc.Unresolved(a.res) || tooFewSamples(a.res) || coarseFor(a, bus, f)):
		a.rawKey = key
		a.res.Error = decode.Names[a.res.Proto] + ": the live view is too coarse - reading sampled records (slower frames)"
	case !live && a.rawKey == key && tooFewSamples(a.res) && f.Decimation > own:
		a.rawFull, want = key, 0 // sampled too coarsely: the full rate
	case !live && a.rawKey == key && tooFewSamples(a.res):
		a.rawKey, a.rawNo = "", key
	case a.rawKey != key:
		a.rawKey = ""
	}
	if a.rawKey == "" || !s.st.Running {
		c.endBusRaw()
		return
	}
	// The full rate where the timebase cannot record it is its own record,
	// asked for as sampled records: a raw lease read it whole and bucketed
	// (a 10 MHz SPI bus at 1ms/div: an envelope of 388k words every 17 s,
	// never judged), sampled records come as samples and are.
	if p := engine.PlanSRAM(s.st.TdivS); want == 0 && p.Log != 0 {
		want = p.SampleS
	}
	c.holdBusLease(want)
}

// aliasedClock reports a clocked bus's decode (I2C, SPI: their decoders
// take the clock from the record) whose clock is over twice as slow as the
// shortest this bus's settings measured: the record aliases it.
// TRLC-LINKS: REQ-SDS-214
func aliasedClock(a *anaState, bus string, f *engine.Frame) bool {
	return a.res.OK && (a.res.Proto == "spi" || a.res.Proto == "i2c") && a.featKey == bus && a.featS > 0 && f.SampleS > 0 &&
		decode.FeatureSamples(a.res)*f.SampleS > 2*a.featS
}

// busFinestDecimated is the finest decimated sample, in seconds (/16 of the
// 2 ns full rate): finer wants take the full rate.
const busFinestDecimated = 32e-9

// busSamplesPerFeature is the samples a bus decode's sampled records take
// over the bus's shortest feature at the least (the decimation steps by two:
// up to twice as many): its decoders want three a bit, and the decimation
// filters the edges.
const busSamplesPerFeature = 4

// busFeatureKey is what a bus's measured feature belongs to: the protocol,
// its rate and lines.
// TRLC-LINKS: REQ-SDS-214
func busFeatureKey(s *sv) string {
	return fmt.Sprint(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA)
}

// holdBusLease renews BUS's lease for its decode: records sampled every
// sampleS at the coarsest (engine LeaseBusSamples), or full-rate ones
// (sampleS 0, LeaseRawFor), ending the other.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) holdBusLease(sampleS float64) {
	e, ok := c.eng.(interface{ LeaseBusSamples(float64, time.Duration) })
	if sampleS > 0 && ok {
		if c.busRawHeld.Swap(false) {
			c.leaseRaw(rawForBus, 0)
		}
		c.busSampHeld.Store(true)
		e.LeaseBusSamples(sampleS, 10*time.Second)
		return
	}
	if ok && c.busSampHeld.Swap(false) {
		e.LeaseBusSamples(0, 0)
	}
	c.busRawHeld.Store(true)
	c.leaseRaw(rawForBus, 10*time.Second)
}

// coarseFor reports a live frame that did not decode and whose points are
// too far apart for the bus as its last decode measured it (under
// coarseFeature points in its shortest feature as an envelope holds it,
// featEnv), whatever the decoder said: a SENT frame at 2ms/div read "no
// SENT frame", not its resolution.
// TRLC-LINKS: REQ-SDS-214
func coarseFor(a *anaState, bus string, f *engine.Frame) bool {
	return !a.res.OK && a.featKey == bus && a.featEnv > 0 && f.SampleS > 0 && a.featEnv/f.SampleS < coarseFeature
}

// coarseFeature is the points of a live envelope in the bus's shortest
// feature under which a decode that fails reads sampled records instead:
// twice readableFeature, as the decoders that check their pulses' widths
// fail on a bucket's misplaced edges well over it (Sony SIRC at 20ms/div,
// 5.6 points a 600 us unit, decoded 5 frames in 8; PJON at 1ms/div).
const coarseFeature = 2 * readableFeature

// tooFewSamples reports a decode refused for too few samples a bit: the
// line codes' "n samples/bit; need >= m", the clocked buses' (I2C, SPI) "n
// cols/clock; too few samples/bit" - unrecognised, a 10 MHz SPI bus at
// 1ms/div read records too coarse for it on and on.
// TRLC-LINKS: REQ-SDS-214
func tooFewSamples(res decode.Result) bool {
	return !res.OK && (strings.Contains(res.Error, "samples/bit; need") || strings.Contains(res.Error, "too few samples/bit"))
}

// endBusRaw ends BUS's leases for the decode (busRawFrames): BUS left,
// decode off, or the bus's settings changed.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) endBusRaw() {
	if c.busRawHeld.Swap(false) {
		c.leaseRaw(rawForBus, 0)
	}
	if c.busSampHeld.Swap(false) {
		if e, ok := c.eng.(interface{ LeaseBusSamples(float64, time.Duration) }); ok {
			e.LeaseBusSamples(0, 0)
		}
	}
}

// leaseRaw asks the engine for full-rate raw live frames for d from now as
// holder, or ends holder's lease (d <= 0): ending the last abandons a raw
// read nobody wants any more (engine LeaseRawFor). An engine without named
// leases has the shared one.
// TRLC-LINKS: REQ-SDS-215, REQ-SDS-214
func (c *Controller) leaseRaw(holder string, d time.Duration) {
	if e, ok := c.eng.(interface{ LeaseRawFor(string, time.Duration) }); ok {
		e.LeaseRawFor(holder, d)
	} else if e, ok := c.eng.(interface{ LeaseDecodeView(time.Duration) }); ok {
		e.LeaseDecodeView(d)
	}
}

// leaseEyeRaw asks the engine for full-rate raw live frames for the eye while
// BUS shows it. It is renewed by each capture and each redraw of the eye: a
// deep raw record (3 s at 200us/div) outlasts a lease renewed per capture.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) leaseEyeRaw() {
	c.mu.Lock()
	on := c.ui.ws == wsview.Bus && c.ui.busEye // a frame analysed as the eye closed renews nothing
	c.mu.Unlock()
	if !on {
		return
	}
	c.eyeLeased.Store(true)
	c.leaseRaw(rawForEye, 3*time.Second)
}

// endEyeRaw ends the eye's raw lease when the eye is left (F2 back to the
// packets, another workspace, a recalled setup): the raw read in flight for
// it is abandoned and the live capture resumes at once, as the packets want.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) endEyeRaw() {
	if c.eyeLeased.Swap(false) {
		c.leaseRaw(rawForEye, 0)
	}
}

// eyeWaitWhy is why the eye has no full-rate capture to fold: the timebase
// records decimated, the acquisition filters, or the next raw frame is on
// its way.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeWaitWhy(s *sv) string {
	if fix := c.eyeRawFix(s); fix != "" {
		return "The eye folds full-rate captures: " + fix
	}
	return "Waiting for a full-rate capture..."
}

// eyeRawFix is the setting that keeps full-rate captures from the eye, as
// what to set ("" when none does).
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeRawFix(s *sv) string {
	switch s.st.AcqMode {
	case engine.AcqAverage, engine.AcqEres, engine.AcqPrecision:
		return "set ACQ to Normal"
	}
	if full := c.fullRateTdiv(); full > 0 && s.st.TdivS > full*1.001 {
		return "S/DIV " + si(full, "s") + " or faster"
	}
	return ""
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

// eyeClear is the Adjust push in the eye: start over, folding again from
// the next capture with the sample point at the eye's centre and the
// threshold on the crossing.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeClear() {
	c.amu.Lock()
	if c.ana.eye != nil {
		c.ana.eye.Reset()
	}
	c.ana.eyeRes, c.ana.eyeOK, c.ana.eyeFed = EyeResult{}, false, frameKey{}
	c.amu.Unlock()
	c.mu.Lock()
	c.ui.eyeX, c.ui.eyeThSet = 0, false
	c.mu.Unlock()
	c.toast("Eye cleared: folding again; sample point centred, threshold on the crossing", 1600*time.Millisecond)
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
