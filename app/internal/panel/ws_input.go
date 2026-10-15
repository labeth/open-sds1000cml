// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"time"

	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// Input routing (REQ-SDS-211, REQ-SDS-213). F1..F5 switch workspaces with
// no menu open and pick choices with one open. Adjust and its push are each
// workspace's tool. Horizontal Position moves the trigger position in every
// workspace; where a workspace has a second Position job (BUS: next match,
// the eye's sample point or the stream's transcript, SPECTRUM: the fine marker, MEASURE: cursor B) its
// push switches Position to that job and back, else the push centres the
// trigger. V/DIV, vertical Position, Level and S/DIV keep their printed
// jobs, as do the pushes Variable, Zero and Zoom.

// switchWS is F1..F5 with no menu open: that workspace, or its next view
// when it is the active one.
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) switchWS(i wsview.WS) {
	c.mu.Lock()
	if i == c.ui.ws {
		c.mu.Unlock()
		c.nextView()
		return
	}
	c.ui.views[c.ui.ws] = c.viewMode
	c.ui.ws, c.ui.posJob = i, false
	c.viewMode = c.ui.views[i]
	dec := c.decProto
	c.mu.Unlock()
	if i != wsview.Bus {
		c.endEyeRaw() // the eye left with BUS
	}
	c.syncStreamView() // the stream ends with BUS, and starts again on its Stream view
	switch i {
	case wsview.Measure:
		c.seedMeas()
	case wsview.Bus:
		if dec == framecalc.ProtoOff {
			c.autoDecodeBus()
		}
	}
	s := c.snap()
	again := wsDefs[i].cycle
	if i == wsview.Compare {
		again = c.cmpAgainText(s)
	}
	c.mu.Lock()
	c.railNoteLocked(wsview.Names[i], "again: "+again)
	c.mu.Unlock()
}

// nextView is the active workspace's key again: its next view (COMPARE:
// stack a master, or cancel one).
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) nextView() {
	c.mu.Lock()
	ws := c.ui.ws
	c.ui.posJob = false // the next view's Position starts on the trigger
	var name, again string
	switch ws {
	case wsview.Scope:
		if c.viewMode == viewYT {
			c.viewMode = viewXY
		} else {
			c.viewMode = viewYT
		}
		c.ui.views[ws] = c.viewMode
		name, again = viewName(c.viewMode), viewName(map[bool]int{true: viewXY, false: viewYT}[c.viewMode == viewYT])
	case wsview.Bus:
		// Packets -> Eye diagram -> Stream -> Packets
		eye, stream := !c.ui.busEye && !c.ui.busStream, c.ui.busEye
		c.ui.busEye, c.ui.busStream = eye, stream
		if eye || stream {
			c.closePickLocked()
		}
		name, again = busViewName(&c.ui), busViewNext(&c.ui)
		if stream {
			again += " (the stream ends)"
		}
	case wsview.Spectrum:
		c.viewMode = nextSpecView(c.viewMode)
		c.ui.views[ws] = c.viewMode
		c.ui.bodeIdx, c.ui.bodeRef = -1, -1
		name, again = viewName(c.viewMode), viewName(nextSpecView(c.viewMode))
	case wsview.Measure:
		c.ui.measAll = !c.ui.measAll
		name, again = map[bool]string{true: "All values", false: "Statistics"}[c.ui.measAll], map[bool]string{true: "Statistics", false: "All values"}[c.ui.measAll]
	case wsview.Compare:
		stacking := c.stack != nil
		c.mu.Unlock()
		if stacking {
			c.stackCancel("", false)
		} else {
			c.stackStart()
		}
		return
	}
	left := ws == wsview.Bus && !c.ui.busEye
	c.railNoteLocked(wsview.Names[ws]+": "+name, "again: "+again)
	c.mu.Unlock()
	if left {
		c.endEyeRaw() // the packets want the live capture now, not the eye's raw record
	}
	if ws == wsview.Bus {
		c.syncStreamView()
	}
}

// busViewName names BUS's view: Packets, Eye diagram or Stream.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-228
func busViewName(u *uiState) string {
	switch {
	case u.busStream:
		return "Stream"
	case u.busEye:
		return "Eye diagram"
	}
	return "Packets"
}

// busViewNext names the view BUS's key again shows.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-228
func busViewNext(u *uiState) string {
	switch {
	case u.busStream:
		return "Packets"
	case u.busEye:
		return "Stream"
	}
	return "Eye diagram"
}

// fKey is F1..F5: the picker's choice, the sheet's choice, or a workspace.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func (c *Controller) fKey(i int) {
	c.mu.Lock()
	wpick, open := c.ui.wpick, c.ui.open
	c.mu.Unlock()
	switch {
	case wpick:
		keys := c.pickKeys(c.snap())
		c.mu.Lock()
		c.ui.flashRow, c.ui.flashI, c.ui.flashUntil = "wpick", i, c.now().Add(350*time.Millisecond)
		c.mu.Unlock()
		if i < len(keys) {
			if err := keys[i].run(); err != nil {
				c.toast(err.Error(), 2000*time.Millisecond)
			}
		}
	case open:
		c.sheetPressF(i)
	default:
		c.switchWS(wsview.WS(i))
	}
}

// menuKey is Menu On/Off: close the picker (a notice says it is cancelled)
// or the sheet, or open this workspace's sheet.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) menuKey() {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.ui.wpick:
		c.closePickLocked()
	case c.ui.open:
		c.closeSheetLocked()
	default:
		c.openWsSheetLocked()
	}
}

// helpKey opens the SYS tab on Help: the guide to this workspace; again
// closes it.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) helpKey() {
	c.mu.Lock()
	c.closePickLocked()
	c.mu.Unlock()
	c.keyTab("help")
	c.mu.Lock()
	if c.ui.open {
		c.ui.guide = 0
	}
	c.mu.Unlock()
}

// adjustTurn is the Adjust knob.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) adjustTurn(dir, steps int) {
	c.mu.Lock()
	wpick, open, ws, eye, stream := c.ui.wpick, c.ui.open, c.ui.ws, c.ui.busEye, c.ui.busStream
	c.mu.Unlock()
	switch {
	case wpick:
		c.pickMove(dir)
	case open:
		c.sheetAdjust(dir, steps)
	case ws == wsview.Bus && eye:
		c.eyeThresholdTurn(dir * steps)
	case ws == wsview.Bus && stream:
		c.streamStopsTurn(dir)
	case ws == wsview.Bus:
		c.busAdjust(dir)
	case ws == wsview.Spectrum:
		c.specAdjust(dir)
	case ws == wsview.Measure:
		c.measCursor(0, dir, steps)
	case ws == wsview.Compare:
		c.cmpAdjust(dir)
	default:
		c.adjustScope(dir, steps)
	}
}

// positionTurn is the Horizontal Position knob: the trigger position, or
// the workspace's second job while the push has chosen it.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) positionTurn(dir, steps int) {
	s := c.snap()
	switch {
	case s.ui.wpick:
		c.pickMove(dir)
	case s.ui.open:
		c.sheetPosition(dir, steps)
	case s.ui.posJob && posJob2(s) != "":
		c.posJobTurn(s, dir, steps)
	default:
		c.trigPosTurn(dir * steps)
	}
}

// posJob2 is the workspace's second Position job ("" none: SCOPE, COMPARE,
// and BUS with no bus decoded keep Position on the trigger).
// TRLC-LINKS: REQ-SDS-213
func posJob2(s *sv) string {
	switch s.ui.ws {
	case wsview.Bus:
		switch {
		case s.ui.busEye:
			return "sample point"
		case s.ui.busStream:
			if streamOn(s) {
				return "transcript"
			}
		case busDecoding(s):
			return "next match"
		}
	case wsview.Spectrum:
		if s.viewMode == viewBode {
			return "point"
		}
		return "fine marker"
	case wsview.Measure:
		return "cursor B"
	}
	return ""
}

// posJobTurn is Position on the workspace's second job.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) posJobTurn(s *sv, dir, steps int) {
	switch s.ui.ws {
	case wsview.Bus:
		switch {
		case s.ui.busEye:
			c.mu.Lock()
			c.ui.eyeX = clampF(c.ui.eyeX+float64(dir*steps)/64, -0.5, 0.5)
			c.mu.Unlock()
		case s.ui.busStream:
			c.scrollStreamList(dir * steps)
		default:
			c.busNextMatch(dir)
		}
	case wsview.Spectrum:
		c.specFine(dir, steps)
	case wsview.Measure:
		c.measCursor(1, dir, steps)
	}
}

// posTool is the tool line's Position and push segments: the job Position
// does now and the one its push switches to (or Zero's centre).
// TRLC-LINKS: REQ-SDS-213
func posTool(s *sv) []wsview.Seg {
	trig := "trigger position"
	if s.zoom > 1 {
		trig = "zoom position"
	}
	j := posJob2(s)
	switch {
	case j == "":
		return []wsview.Seg{{Key: "Position", Job: trig}, {Key: "push", Job: "centre"}}
	case s.ui.posJob:
		return []wsview.Seg{{Key: "Position", Job: j}, {Key: "push", Job: trig}}
	}
	return []wsview.Seg{{Key: "Position", Job: trig}, {Key: "push", Job: j}}
}

// trigPosTurn moves the trigger position (zoomed: the zoom window).
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) trigPosTurn(n int) {
	c.mu.Lock()
	z := c.zoom
	if z > 1 {
		c.zoomOff = clampF(c.zoomOff+float64(n)*0.02, -0.5, 0.5)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.eng.SetTrigPosFrac(clampF(c.trigPos()+float64(n)*0.01, 0.02, 1))
}

// trigPos is the engine's trigger position (0.5 when unset).
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) trigPos() float64 {
	f := c.eng.Snapshot().TrigPosFrac
	if f <= 0 {
		f = 0.5
	}
	return f
}

// adjustPush is the Adjust push (the byte picker: trigger on the
// highlighted byte).
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) adjustPush() {
	c.mu.Lock()
	wpick, open, ws, eye, stream := c.ui.wpick, c.ui.open, c.ui.ws, c.ui.busEye, c.ui.busStream
	c.mu.Unlock()
	switch {
	case wpick:
		c.pickUseKey(1)
	case open:
		c.sheetAdjustPush()
	case ws == wsview.Bus && eye:
		c.eyeClear()
	case ws == wsview.Bus && stream:
		c.streamAdjustPush()
	case ws == wsview.Bus:
		s := c.snap()
		dec := busDecoding(s)
		if dec {
			c.openPickWS()
		} else {
			c.autoDecodeBus()
		}
	case ws == wsview.Spectrum:
		c.specDelta()
	case ws == wsview.Measure:
		c.measCursorMode()
	case ws == wsview.Compare:
		c.cmpAction()
	default:
		c.scopeAdjustPush()
	}
}

// positionPush is the Horizontal Position push: Position to the
// workspace's second job and back, or (no second job) Zero: the trigger to
// the centre.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) positionPush() {
	s := c.snap()
	switch {
	case s.ui.wpick:
		c.mu.Lock()
		c.ui.pick.start = c.ui.pick.first
		c.mu.Unlock()
	case s.ui.open:
		c.sheetPositionPush()
	case posJob2(s) == "":
		c.centreTrigger()
		c.toast("Trigger to centre", 900*time.Millisecond)
	default:
		c.posJobToggle(s)
	}
}

// posJobToggle switches Position between the trigger position and the
// workspace's second job. Back from BUS's next match, the packet list (and a
// zoom window) returns to the trigger.
// TRLC-LINKS: REQ-SDS-213, REQ-SDS-214
func (c *Controller) posJobToggle(s *sv) {
	j := posJob2(s)
	trig := "trigger position"
	if s.zoom > 1 {
		trig = "zoom position"
	}
	c.mu.Lock()
	on := !c.ui.posJob
	c.ui.posJob = on
	c.mu.Unlock()
	if s.ui.ws == wsview.Bus && s.ui.busStream {
		c.streamScrollJob(on)
	}
	if on {
		c.toast("Position: "+j+"  (push: back to the "+trig+")", 1400*time.Millisecond)
		return
	}
	if s.ui.ws == wsview.Bus && !s.ui.busEye && !s.ui.busStream {
		c.busToTrigger(s)
	}
	c.toast("Position: "+trig+"  (push: "+j+")", 1400*time.Millisecond)
}

// tdivTurn is S/DIV: the sheet's tab, the zoom factor while zoomed
// (clockwise = more zoom), else the timebase (clockwise toward nS).
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) tdivTurn(dir int) {
	c.mu.Lock()
	open, z := c.ui.open, c.zoom
	c.mu.Unlock()
	switch {
	case open:
		c.sheetTurnTab(dir)
	case z > 1:
		i := 0
		for k, f := range zoomFactors {
			if f == z {
				i = k
			}
		}
		c.setZoom(zoomFactors[clampInt(i+dir, 1, len(zoomFactors)-1)])
	default:
		c.stepTdiv(-dir)
	}
}

// tdivPush is the S/DIV push (Zoom): zoom on/off; in BUS Packets onto the
// selected packet; in BUS Stream, a stopped stream's transcript or waveform.
// TRLC-LINKS: REQ-SDS-213, REQ-SDS-214, REQ-SDS-228
func (c *Controller) tdivPush() {
	s := c.snap()
	if !s.ui.open && !s.ui.wpick && s.ui.ws == wsview.Bus && s.ui.busStream && s.streamStopped {
		c.toggleStreamList() // a stopped stream: its transcript or its record's waveform
		return
	}
	if !s.ui.open && !s.ui.wpick && s.ui.ws == wsview.Bus && !s.ui.busEye && !s.ui.busStream {
		dec := busDecoding(s)
		if dec {
			c.busZoom()
			return
		}
	}
	c.zoomToggle()
}

// variablePush is the V/DIV push (Variable). The front end has only its 12
// calibrated V/div steps, so a fine V/div is not offered: the push says so.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) variablePush(ch int) {
	c.toast(fmt.Sprintf("CH%d Variable: no fine V/div on this instrument (12 calibrated steps)", ch+1), 1800*time.Millisecond)
}

// eyeThresholdTurn moves the eye's threshold cursor n codes (0.04 div each).
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyeThresholdTurn(n int) {
	c.amu.Lock()
	r, ok := c.ana.eyeRes, c.ana.eyeOK
	c.amu.Unlock()
	if !ok {
		c.toast("The eye is still empty", 1000*time.Millisecond)
		return
	}
	c.mu.Lock()
	th := eyeThreshold(&c.ui, r)
	c.ui.eyeTh, c.ui.eyeThSet = clampF(th+float64(n), 28, 228), true
	c.mu.Unlock()
}

// ---- SCOPE ----

// adjTarget is the row SCOPE's Adjust tunes: the last number set in a sheet
// while it is in use, else the active cursor.
// TRLC-LINKS: REQ-SDS-213
func adjTarget(s *sv) *row {
	if r := rowByID[s.ui.adj]; r != nil && r.kind == wsview.Number && r.relevant(s) {
		return r
	}
	if s.curOn {
		return rowByID["curs."+string(rune('a'+s.curSel))]
	}
	return nil
}

// adjustScope is Adjust in SCOPE: tune the target row.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) adjustScope(dir, steps int) {
	s := c.snap()
	r := adjTarget(s)
	if r == nil {
		if l := rowByID[s.ui.adj]; l != nil {
			c.toast("Adjust: "+l.label+" is not in use now - set a value in a sheet", 1800*time.Millisecond)
		} else {
			c.toast("SCOPE: Adjust tunes the last value you set in a sheet (none yet)", 1800*time.Millisecond)
		}
		return
	}
	if err := r.stepN(c, dir*max(steps, 1)); err != nil {
		c.toast(err.Error(), 1500*time.Millisecond)
	}
	c.mu.Lock()
	c.ui.adjT = c.now()
	c.mu.Unlock()
}

// scopeAdjustPush opens the sheet at the row Adjust tunes (else this
// workspace's sheet).
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) scopeAdjustPush() {
	s := c.snap()
	r := adjTarget(s)
	if r == nil {
		r = rowByID[s.ui.adj]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if r == nil {
		c.openWsSheetLocked()
		return
	}
	ti := rowTab[r.id]
	c.openSheetLocked(ti, "adjust")
	c.ui.rowAt[ti] = rowIndex(r.id)
}

// ---- run control ----

// runAuto is Auto: in BUS a new bus scan framed for reading (busAuto),
// elsewhere autoset; any menu closes and the workspace stays. Run from a
// remote, it leaves the LCD's menus as they are. It frames for the LCD's
// workspace, or for the one a page names (InjectAutoFor).
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-211, REQ-SDS-223, REQ-SDS-226
func (c *Controller) runAuto() {
	c.endWebCapture()
	c.mu.Lock()
	if c.apiDepth == 0 {
		c.closeSheetLocked()
		c.closePickLocked()
	}
	ws, _ := c.autoTargetLocked()
	c.mu.Unlock()
	if ws == wsview.Bus {
		c.busAuto() // a new bus scan, framed for reading
		return
	}
	c.autoset()
}

// autoTargetLocked is the workspace AUTO frames for and its view: the one a
// page named for this press, else the LCD's. c.mu held.
// TRLC-LINKS: REQ-SDS-226
func (c *Controller) autoTargetLocked() (wsview.WS, int) {
	if c.autoFor > 0 {
		ws := wsview.WS(c.autoFor - 1)
		if ws != c.ui.ws {
			return ws, c.ui.views[ws]
		}
	}
	return c.ui.ws, c.viewMode
}

// InjectAutoFor presses AUTO for workspace id ("bus", "SPECTRUM", ...) on
// the panel goroutine: the AUTO of the workspace a page shows, whichever the
// LCD shows. An unknown id is refused.
// TRLC-LINKS: REQ-SDS-226
func (c *Controller) InjectAutoFor(id string) bool {
	ws := wsIndex(id)
	if ws < 0 {
		return false
	}
	select {
	case c.inject <- func() {
		c.mu.Lock()
		c.autoFor = ws + 1
		c.mu.Unlock()
		c.button(btnAuto)
		c.mu.Lock()
		c.autoFor = 0
		c.mu.Unlock()
	}:
		return true
	default:
		return false
	}
}

// defaultSetupUI is Default Setup: factory settings, any menu closes, the
// workspace stays and re-applies its entry behaviour (FFT in SPECTRUM, Vpp
// and Freq in MEASURE, the bus detection in BUS). Run from a remote, it
// leaves the LCD's menus as they are.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211
func (c *Controller) defaultSetupUI() {
	c.stackCancel("", false)
	c.DefaultSetup()
	c.mu.Lock()
	old := c.ui
	c.ui = freshUI()
	c.ui.ws = old.ws
	if c.apiDepth > 0 {
		u := &c.ui
		u.open, u.tab, u.via, u.rowAt, u.wsTab = old.open, old.tab, old.via, old.rowAt, old.wsTab
		u.edit, u.sub, u.pick, u.wpick = old.edit, old.sub, old.pick, old.wpick
	}
	ws := old.ws
	c.ui.statT = c.now()
	c.viewMode = c.ui.views[ws]
	c.meas, c.fftSrc, c.stackN = nil, 0, 64
	c.mu.Unlock()
	c.amu.Lock()
	c.ana = anaState{}
	c.amu.Unlock()
	switch ws {
	case wsview.Measure:
		c.seedMeas()
	case wsview.Bus:
		c.autoDecodeBus()
	}
}

// freshUI is the UI at power-on: SCOPE, every workspace on its own view.
// TRLC-LINKS: REQ-SDS-211
func freshUI() uiState {
	u := uiState{zoomLast: 10, bodeIdx: -1, bodeRef: -1}
	for i := range u.views {
		u.views[i] = wsDefs[i].view
	}
	for i := range u.wsTab {
		u.wsTab[i] = -1
	}
	for i, t := range sheetTabs {
		u.rowAt[i] = t.home
	}
	return u
}
