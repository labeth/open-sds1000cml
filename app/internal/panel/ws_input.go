// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"time"

	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// Input routing (REQ-SDS-211, REQ-SDS-213). F1..F5 switch workspaces with
// no menu open and pick choices with one open. Adjust and Horizontal
// Position (and their pushes) are each workspace's tool; V/DIV, vertical
// Position, Level and S/DIV keep their printed jobs, as do the pushes
// Variable, Zero and Zoom.

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
	c.ui.ws = i
	c.viewMode = c.ui.views[i]
	dec := c.decProto
	c.mu.Unlock()
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
		c.ui.busEye = !c.ui.busEye
		if c.ui.busEye {
			c.closePickLocked()
		}
		name, again = map[bool]string{true: "Eye diagram", false: "Packets"}[c.ui.busEye], map[bool]string{true: "Packets", false: "Eye diagram"}[c.ui.busEye]
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
	c.railNoteLocked(wsview.Names[ws]+": "+name, "again: "+again)
	c.mu.Unlock()
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

// menuKey is Menu On/Off: close the picker or the sheet, or open this
// workspace's sheet.
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
	wpick, open, ws, eye := c.ui.wpick, c.ui.open, c.ui.ws, c.ui.busEye
	c.mu.Unlock()
	switch {
	case wpick:
		c.pickMove(dir)
	case open:
		c.sheetAdjust(dir, steps)
	case ws == wsview.Bus && eye:
		c.eyeThresholdTurn(dir * steps)
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

// positionTurn is the Horizontal Position knob.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) positionTurn(dir, steps int) {
	if c.scrollStreamList(dir * steps) { // an open stream transcript scrolls
		return
	}
	c.mu.Lock()
	wpick, open, ws, eye := c.ui.wpick, c.ui.open, c.ui.ws, c.ui.busEye
	c.mu.Unlock()
	switch {
	case wpick:
		c.pickMove(dir)
	case open:
		c.sheetPosition(dir, steps)
	case ws == wsview.Bus && eye:
		c.mu.Lock()
		c.ui.eyeX = clampF(c.ui.eyeX+float64(dir*steps)/64, -0.5, 0.5)
		c.mu.Unlock()
	case ws == wsview.Bus:
		c.busNextMatch(dir)
	case ws == wsview.Spectrum:
		c.specFine(dir, steps)
	case ws == wsview.Measure:
		c.measCursor(1, dir, steps)
	default: // SCOPE, COMPARE: as printed
		c.trigPosTurn(dir * steps)
	}
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

// adjustPush is the Adjust push.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) adjustPush() {
	c.mu.Lock()
	wpick, open, ws, eye := c.ui.wpick, c.ui.open, c.ui.ws, c.ui.busEye
	c.mu.Unlock()
	switch {
	case wpick:
		c.pickCancel()
	case open:
		c.sheetAdjustPush()
	case ws == wsview.Bus && eye:
		c.eyeClear()
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

// positionPush is the Horizontal Position push (Zero).
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) positionPush() {
	if c.streamListToTrigger() { // an open stream transcript: back to the trigger line
		return
	}
	c.mu.Lock()
	wpick, open, ws, eye := c.ui.wpick, c.ui.open, c.ui.ws, c.ui.busEye
	if wpick {
		c.ui.pick.start = c.ui.pick.first
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	switch {
	case open:
		c.sheetPositionPush()
	case ws == wsview.Bus && eye:
		c.mu.Lock()
		c.ui.eyeX, c.ui.eyeThSet = 0, false
		c.toastLocked("Sample point to the eye centre, threshold to the crossing", 1400*time.Millisecond)
		c.mu.Unlock()
	case ws == wsview.Bus:
		c.busToTrigger()
	case ws == wsview.Spectrum:
		c.specLargest()
	case ws == wsview.Measure:
		c.statsReset()
	default:
		c.centreTrigger()
		c.toast("Trigger to centre", 900*time.Millisecond)
	}
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
// selected packet.
// TRLC-LINKS: REQ-SDS-213, REQ-SDS-214
func (c *Controller) tdivPush() {
	s := c.snap()
	if !s.ui.open && !s.ui.wpick && s.ui.ws == wsview.Bus && !s.ui.busEye {
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
// remote, it leaves the LCD's menus as they are.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-211, REQ-SDS-223
func (c *Controller) runAuto() {
	c.endWebCapture()
	c.mu.Lock()
	if c.apiDepth == 0 {
		c.closeSheetLocked()
		c.closePickLocked()
	}
	ws := c.ui.ws
	c.mu.Unlock()
	if ws == wsview.Bus {
		c.busAuto() // a new bus scan, framed for reading
		return
	}
	c.autoset()
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
