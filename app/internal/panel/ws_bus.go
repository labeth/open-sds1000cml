// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"strings"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/analysis"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// BUS (REQ-SDS-214): entering it with decode off finds the bus and decodes
// it; the pane lists the complete packets as a fixed-column hex dump with a
// separate string box, held still while the same messages repeat. Adjust
// selects a packet, its push picks bytes to trigger on, Position jumps match
// to match, its push goes back to the trigger, and the S/DIV push zooms onto
// the selected packet. BUS again shows the eye diagram (REQ-SDS-215).

// Eye grid size: 2 UI across 384 columns (2 px each, 768 px), the plot's
// height in 2 px rows; the eye is fed about eyeFeedSPU samples per UI and
// at most eyeMaxFed samples a capture (the fold costs about 1 ms per 1000
// samples on the device's core).
const (
	eyeCols    = 384
	eyeFeedSPU = 64
	eyeMaxFed  = 16384
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

// lockBus fixes the decoder to a detected bus (an Auto result): protocol,
// lines and rate. It reports whether the result named a bus.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) lockBus(res decode.Result) bool {
	p := map[string]int{"uart": framecalc.ProtoUART, "i2c": framecalc.ProtoI2C, "spi": framecalc.ProtoSPI,
		"manchester": framecalc.ProtoManchester, "sent": framecalc.ProtoSENT, "canfd": framecalc.ProtoCAN,
		"mil1553": framecalc.ProtoMIL1553, "arinc429": framecalc.ProtoARINC429, "usbls": framecalc.ProtoUSB,
		"flexray": framecalc.ProtoFlexRay}[res.Proto]
	if !res.OK || p == 0 {
		return false
	}
	c.mu.Lock()
	c.decProto = p
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
	if c.decMode == decModeStream {
		c.decMode = decModeView
	}
	c.mu.Unlock()
	c.syncDecodeView(true)
	c.syncSerialBus()
	return true
}

// lastAuto runs Auto on the frame on screen (the shared decode cache makes
// it free when the LCD decoded it already). Not for use inside OnFrame.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) lastAuto() (decode.Result, bool) {
	if c.frameFn == nil {
		return decode.Result{}, false
	}
	var res decode.Result
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.IsEnv || (f.PeakDetect && !f.Ordered) || len(f.C1) < 32 {
			return
		}
		res = framecalc.Decode(f, framecalc.DecodeParams{Proto: framecalc.ProtoAuto, Format: "hex"})
	})
	return res, res.OK && res.Proto != "" && res.Proto != "off"
}

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

// runDetect waits (bounded) for a sampled frame and detects the bus on it.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) runDetect() {
	var res decode.Result
	found := false
	for i := 0; i < 40 && !found; i++ {
		if e, ok := c.eng.(interface{ LeaseDecodeView(time.Duration) }); ok {
			e.LeaseDecodeView(3 * time.Second)
		}
		raw := false
		if c.frameFn != nil {
			c.frameFn(func(f *engine.Frame) {
				raw = f != nil && !f.IsEnv && !(f.PeakDetect && !f.Ordered) && f.Valid >= 32 && len(f.C1) >= f.Valid
			})
		}
		if raw {
			res, found = c.lastAuto()
			break
		}
		if c.detectSync {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	locked := found && c.lockBus(res)
	c.mu.Lock()
	c.ui.busDetecting = false
	c.ui.busFailed = !locked
	if locked {
		c.toastLocked("AUTO: "+busName(c.decProto, c.decBaud, c.decChA, c.decChB, c.decCPOL, c.decCPHA)+", decode on", 1800*time.Millisecond)
	} else {
		c.decProto = framecalc.ProtoOff
		c.toastLocked("AUTO: no bus found on CH1 or CH2 - decode stays off", 2400*time.Millisecond)
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
// time, or why there is no eye.
// TRLC-LINKS: REQ-SDS-215
func eyeConfig(s *sv, a *anaState) (ch int, bit float64, why string) {
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
// eyeFeedSPU samples per UI (a finer capture adds nothing the 192 columns
// of a UI show) and at most eyeMaxFed samples a capture.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeFrame(a *anaState, s *sv, f *engine.Frame) {
	ch, bit, why := eyeConfig(s, a)
	if why == "" && (f.IsEnv || f.PeakDetect && !f.Ordered) {
		why = "The eye needs sampled captures (not peak detect or roll)"
	}
	if why != "" {
		a.eyeWhy, a.eyeOK = why, false
		return
	}
	key := fmt.Sprint(ch, bit, s.vidx[ch], s.offV[ch], s.probe[ch], s.cpl[ch], s.inv[ch], f.SampleS, s.zoom, s.zoomOff,
		s.st.AcqMode, s.st.TdivS, s.st.TrigPosFrac)
	if a.eye == nil {
		a.eye = anaNewEye(eyeCols, eyeRows())
	}
	if key != a.eyeKey { // any change to the picture starts the eye over
		a.eye.Reset()
		a.eyeKey, a.eyeRes, a.eyeOK, a.eyeFed = key, EyeResult{}, false, frameKey{}
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
