// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// Task workspaces (ADR-TASK-WORKSPACES). With no menu open, F1..F5 switch the
// SCOPE, BUS, SPECTRUM, MEASURE and COMPARE workspaces; the active one's key
// again shows its next view. Each workspace re-lays the LCD for its job (a
// task pane above the trace) and gives Adjust and Horizontal Position a tool.
// A settings sheet or the BUS byte picker gives F1..F5 to the menu.

// Display views (the instrument's view mode, also the VIEW sheet's View row).
const (
	viewYT = iota
	viewXY
	viewFFT
	viewBode
	viewSpectrogram
	numViews
)

// viewName names a view mode.
// TRLC-LINKS: REQ-SDS-211
func viewName(v int) string {
	switch v {
	case viewXY:
		return "X-Y"
	case viewFFT:
		return "FFT"
	case viewBode:
		return "Bode"
	case viewSpectrogram:
		return "Spectrogram"
	}
	return "Y-T"
}

// wsDef is a workspace's fixed layout.
type wsDef struct {
	tabs    []string // its own sheet tabs, first in its sheet
	plotTop int      // first plot row below its pane (0: no pane)
	view    int      // the view it starts on
	cycle   string   // what its key again cycles (rail label)
}

// wsDefs are the five workspaces (REQ-SDS-211).
var wsDefs = [wsview.Count]wsDef{
	{tabs: []string{"trig", "ch1", "ch2", "time", "acq"}, plotTop: 0, view: viewYT, cycle: "Y-T / X-Y"},
	{tabs: []string{"bus", "trig", "time"}, plotTop: 136, view: viewYT, cycle: "Packets / Eye diagram"},
	{tabs: []string{"view", "acq", "time"}, plotTop: 150, view: viewFFT, cycle: "FFT / Spectrogram / Bode"},
	{tabs: []string{"meas", "curs", "acq"}, plotTop: 142, view: viewYT, cycle: "statistics / all values"},
	{tabs: []string{"cmp", "ch1", "ch2"}, plotTop: 104, view: viewYT, cycle: "stack a master"},
}

// wsKeys name the F-key of each workspace.
var wsKeys = [wsview.Count]string{"F1", "F2", "F3", "F4", "F5"}

// Sheet sub-modes on the BUS tab's Match bytes row.
const (
	subNone  = iota
	subMatch // the match editor (keyboard)
	subPick  // pick bytes from the screen
)

// pickState is the byte picker: the bytes of the listed packets.
type pickState struct {
	toks  []wsview.PickTok
	start int // the first byte the F-keys trigger on
	first int // where it opened
}

// uiState is the workspace UI (guarded by Controller.mu).
type uiState struct {
	ws      wsview.WS
	views   [wsview.Count]int // the display view each workspace remembers
	busEye  bool              // BUS shows the eye diagram (else the packets)
	measAll bool              // MEASURE shows all values (else statistics)

	open  bool              // a settings sheet is open
	tab   int               // the sheet's tab (index into sheetTabs)
	via   string            // the key that opened it
	rowAt [numTabs]int      // the remembered row of each tab
	wsTab [wsview.Count]int // where the sheet was left per workspace (-1: never)
	edit  bool              // Adjust-push edit mode on the row
	sub   int               // subNone / subMatch / subPick
	pick  pickState
	wpick bool // the workspace picker (BUS pane) is open

	flashRow   string // the F-key flash, tied to its row
	flashI     int
	flashUntil time.Time

	adj      string // SCOPE: the number row Adjust tunes (the last one set in a sheet)
	adjT     time.Time
	zoomLast int
	guide    int // the Help page: 0 workspace, 1 sheets, 2 knobs and lamps

	noteWS    wsview.WS // the rail label beside a workspace key
	noteHead  string
	noteText  string
	noteUntil time.Time
	notice    string // a short notice over the plot
	noticeUnt time.Time
	remote    string // the last remote change ("REMOTE  TRIG level 1.25 V")
	remoteRow string
	remoteUnt time.Time

	cmpRow       int  // COMPARE: the chosen row
	busFailed    bool // BUS: the last auto-detection found no bus
	busDetecting bool
	eyeX         float64 // BUS eye: the sample point, UI from the centre
	eyeTh        float64 // the threshold, codes
	eyeThSet     bool
	specF        float64 // SPECTRUM: the marker (Hz); unset follows the largest peak
	specFSet     bool
	specRef      float64 // the delta reference (Hz) and its level
	specRefDB    float64
	specRefSet   bool
	bodeIdx      int // Bode: the marked point; -1 follows the sweep
	bodeRef      int // the delta reference point; -1 none
	statT        time.Time
	masterN      int // the last master: depth, source, when
	masterSrc    int
	masterT      time.Time
	hasMaster    bool
}

// Notice durations.
const (
	railNoteFor   = 1600 * time.Millisecond
	noticeFor     = 1500 * time.Millisecond
	remoteNoteFor = 1500 * time.Millisecond
)

// SetClock replaces the controller's clock (tests drive notices and the
// rail label with it instead of sleeping).
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) SetClock(now func() time.Time) {
	c.mu.Lock()
	c.clock = now
	c.mu.Unlock()
}

// now reads the controller's clock (c.mu held or not: clock is set once).
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// toast shows a short notice over the plot for d (c.mu held).
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) toastLocked(msg string, d time.Duration) {
	c.ui.notice, c.ui.noticeUnt = msg, c.now().Add(d)
}

// toast shows a short notice over the plot for d.
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) toast(msg string, d time.Duration) {
	c.mu.Lock()
	c.toastLocked(msg, d)
	c.mu.Unlock()
}

// railNote labels the active workspace's key for railNoteFor (c.mu held).
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) railNoteLocked(head, text string) {
	c.ui.noteWS, c.ui.noteHead, c.ui.noteText = c.ui.ws, wsKeys[c.ui.ws]+" "+head, text
	c.ui.noteUntil = c.now().Add(railNoteFor)
}

// snap reads everything the rows and panes show.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func (c *Controller) snap() *sv {
	s := &sv{now: c.now(), st: c.eng.Snapshot(), tdivs: c.tdivs}
	s.memDepth = s.st.MemDepth
	if md, ok := c.eng.(interface{ MemDepth() int }); ok {
		s.memDepth = md.MemDepth()
	}
	src := s.st.TrigSource & 1
	if c.fe != nil {
		s.fe = true
		s.vidx, _ = c.fe.Snapshot()
		for ch := 0; ch < 2; ch++ {
			s.probe[ch] = c.fe.ProbeFactor(ch)
			s.cpl[ch] = c.fe.Coupling(ch)
			s.offV[ch] = c.fe.OffsetReqV(ch)
		}
		if b, ok := c.fe.(interface{ BWL(ch int) bool }); ok {
			s.hasBWL = true
			s.bwl = [2]bool{b.BWL(0), b.BWL(1)}
		}
		s.trigLo = c.fe.TrigVolts(engine.TrigCodeMax, src)
		s.trigHi = c.fe.TrigVolts(engine.TrigCodeMin, src)
		if s.st.TrigCode != 0 {
			s.trigV = c.fe.TrigVolts(s.st.TrigCode, src)
		} else {
			s.trigV = c.fe.TrigVolts(uint16(clampInt(int(c.fe.TrigCode(0, src)+0.5), engine.TrigCodeMin, engine.TrigCodeMax)), src)
		}
	} else {
		s.probe = [2]float64{1, 1}
		s.trigLo, s.trigHi = engine.TrigLevelVolts(engine.TrigCodeMax), engine.TrigLevelVolts(engine.TrigCodeMin)
		if s.st.TrigCode != 0 {
			s.trigV = engine.TrigLevelVolts(s.st.TrigCode)
		}
		for ch := range s.vidx {
			s.vidx[ch] = 8 // 1 V/div without a front end
		}
	}
	s.trigStep = float64(c.trigStepCodes()) / 911 * s.probe[src]
	if c.inverter != nil {
		s.hasInv, s.inv = true, c.inverter.Inverted()
	}
	if se := c.serialTrig(); se != nil {
		s.hasSerial, s.serial = true, se.SerialConfig()
	}
	q := s.st.TrigQual
	c.mu.Lock()
	if q.PulseLvl <= 0 { // an engine that does not publish its qualifiers: the shadows
		q = engine.TrigQual{PulseLvl: c.pulseLvl, PulseMinNs: c.pulseMin, PulseMaxNs: c.pulseMax, PulseCond: c.pulseCond,
			SlopeLo: c.slopeLo, SlopeHi: c.slopeHi, SlopeMinNs: c.slopeMin, SlopeMaxNs: c.slopeMax, SlopeCond: c.slopeCond,
			VideoStd: c.videoStd, VideoLine: c.videoLine, VideoNeg: c.videoNeg}
	}
	s.pulse = [4]float64{q.PulseLvl, q.PulseMinNs, q.PulseMaxNs, float64(q.PulseCond)}
	s.slope = [5]float64{q.SlopeLo, q.SlopeHi, q.SlopeMinNs, q.SlopeMaxNs, float64(q.SlopeCond)}
	s.video = [3]int{q.VideoStd, q.VideoLine, b2ic(q.VideoNeg)}
	s.chDisp, s.showMeas, s.persist = c.chDisp, c.showMeas, c.persist
	s.viewMode, s.mathMode, s.zoom, s.zoomOff = c.viewMode, c.mathMode, c.zoom, c.zoomOff
	s.decProto, s.decBaud, s.decChA, s.decChB = c.decProto, c.decBaud, c.decChA, c.decChB
	s.decCPOL, s.decCPHA, s.decFormat = c.decCPOL, c.decCPHA, c.decFormat
	s.decMode, s.decTrig, s.decValue, s.streamList = c.decMode, c.decTrig, c.decValue, c.streamList
	s.hasStream = c.stream != nil
	s.curOn, s.curType, s.curSel, s.curX, s.curY = c.curOn, c.curType, c.curSel, c.curX, c.curY
	for i, r := range c.refs {
		s.refHas[i], s.refShow[i] = r.has, r.show
	}
	s.srActive, s.srFocus, s.srCh, s.srK = c.srActive, c.srFocus, c.srCh, c.srK
	s.srStopMode, s.srStopVal, s.srStatus = c.srStopMode, c.srStopVal, c.srStatus
	s.srManLo, s.srManHi, s.srFPGABusy = c.srManLo, c.srManHi, c.srFPGABusy
	if c.srStack != nil {
		s.srGateLo, s.srGateHi, s.srN = c.srStack.GateLo, c.srStack.GateHi, c.srStack.N
	}
	s.maskN, s.maskTol, s.maskBusy, s.maskMsg = c.maskN, c.maskTol, c.maskBuilding || c.masterPend != nil, c.maskMsg
	s.meas = append([]measItem(nil), c.meas...)
	s.fftSrc, s.stackN, s.stacking = c.fftSrc, c.stackN, c.stack != nil
	s.slotUsed, s.slotSum = c.slotUsed, c.slotSum
	s.remoteNotices, s.hasHardcopy = c.remoteNotices, c.hardcopy != nil
	s.ui = c.ui
	stream := c.stream
	c.mu.Unlock()
	if stream != nil && s.decProto == 1 && s.decMode == decModeStream {
		s.streamStopped = !stream.Active()
	}
	return s
}
