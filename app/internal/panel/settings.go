// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"

	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/settings"
)

// SettingsView reports the controller-owned slice of the persisted setup
// (settings.Panel surface): the decode setup, the display state, the
// workspace UI and the screen saver's times, so a restart or a recalled setup
// returns where it was (a setup memory leaves the screen saver out).
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211, REQ-SDS-221
func (c *Controller) SettingsView() settings.ViewState {
	ser := c.serialSettings() // engine state: read before the panel lock
	s := c.snap()
	c.mu.Lock()
	defer c.mu.Unlock()
	ui := settings.UI{Set: true, Workspace: int(c.ui.ws), BusEye: c.ui.busEye, MeasAll: c.ui.measAll,
		FFTSource: c.fftSrc, MasterDepth: c.stackN, RemoteNotices: c.remoteNotices,
		MaskFrames: c.maskN, MaskTol: c.maskTol + 1, SrCh: c.srCh + 1, SrGrid: c.srK, SrStop: c.srStopMode + 1, SrTarget: c.srStopVal}
	for i := range ui.Views {
		ui.Views[i] = c.ui.views[i]
	}
	ui.Views[c.ui.ws] = c.viewMode
	for i, it := range c.meas {
		if i < len(ui.Meas) {
			ui.Meas[i] = it.ch*32 + it.param + 1
		}
	}
	disp := settings.Display{MathMode: c.mathMode, ShowMeas: c.showMeas, Persist: c.persist,
		Hidden: [2]bool{!c.chDisp[0], !c.chDisp[1]},
		Set:    true, CurX: s.curX, CurY: s.curY, CurAt: true, CurT: c.curT, CurV: c.curV, CurVCh: c.curVCh,
		CurSel: c.curSel, Zoom: c.zoom, ZoomOff: c.zoomOff}
	if c.curOn {
		disp.CurMode = 1 + c.curType
	}
	if c.inverter != nil {
		disp.Invert = c.inverter.Inverted()
	}
	return settings.ViewState{
		ViewMode: c.viewMode,
		// Math, measurements, persistence, channel visibility, invert, cursors
		// and zoom: part of a setup, so a restart and a recall keep them.
		Display: disp,
		Decode: settings.Decode{
			Proto: c.decProto, Baud: c.decBaud,
			ChA: c.decChA, ChB: c.decChB,
			CPOL: c.decCPOL, CPHA: c.decCPHA, LSB: c.decLSB,
			DelayNs: c.decDelayNs, DelayAuto: c.decDelayAuto, Fine: c.decFine,
			Format:     c.decFormat,
			StreamTrig: c.decTrig, StreamValue: c.decValue,
		},
		UI:     ui,
		Serial: ser,
		Screen: settings.Screen{Set: true, SaverMin: c.saverMin, OffMin: c.offMin},
	}
}

// serialSettings is the serial trigger as the settings file keeps it.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-089
func (c *Controller) serialSettings() settings.Serial {
	se := c.serialTrig()
	if se == nil {
		return settings.Serial{}
	}
	p := se.SerialConfig()
	return settings.Serial{Set: true, Armed: c.eng.Snapshot().SerialMode != 0,
		Match: strings.Join(tokensOf(p), " "), Addr: p.Addr, RW: p.RW}
}

// restoreSerial puts back a saved serial trigger: the match, the I2C address
// and direction, then arms it through the TRIG tab's own path, which needs
// the restored bus. A match that no longer parses is any byte; a trigger
// that cannot arm stays off.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-089
func (c *Controller) restoreSerial(s settings.Serial) {
	se := c.serialTrig()
	if se == nil || !s.Set {
		return
	}
	toks, err := parseMatchText(s.Match)
	if err != nil {
		c.logf("settings: serial match %q: %v - any byte", s.Match, err)
		toks = nil
	}
	b, pat, err := compileTokens(toks)
	if err != nil {
		b, pat = nil, nil
	}
	p := se.SerialConfig()
	p.Bytes, p.Pattern = b, pat
	p.Addr, p.RW = clampInt(s.Addr, -1, 127), clampInt(s.RW, 0, 2)
	se.SetSerialParams(c.serialBus(p))
	if st := c.eng.Snapshot(); s.Armed && st.SerialMode == 0 {
		if err := c.armSerial(); err != nil {
			c.logf("settings: serial trigger not re-armed: %v", err)
		}
	} else if !s.Armed && st.SerialMode != 0 {
		c.setTrigTypeIndex(st, 0)
	}
}

// ApplySettingsView restores the controller-owned setup, enforcing the
// domains the BUS and VIEW tabs keep: proto a decoder, a baud the decoders
// take, clock and data on opposite channels for the two-signal protocols,
// format and view in range, a workspace and its views. Hostile values
// degrade to defaults — never a panic, never an out-of-domain field. A
// recalled workspace is entered as F1..F5 enters it (its view applied). A
// screen saver time the rows do not offer is the factory time.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211, REQ-SDS-221, REQ-SDS-222
func (c *Controller) ApplySettingsView(v settings.ViewState) {
	d := v.Decode
	if d.Proto < framecalc.ProtoOff || d.Proto >= framecalc.NumProtos {
		d.Proto = framecalc.ProtoOff
	}
	// A setup saved while Auto was a decoding mode holds it: Auto detects the
	// bus once and fills in the settings.
	detect := d.Proto == framecalc.ProtoAuto
	if detect {
		d.Proto = framecalc.ProtoOff
	}
	if d.Baud < 50 || d.Baud > maxDecBaud {
		d.Baud = 115200
	}
	d.ChA &= 1
	d.ChB &= 1
	if d.Proto == framecalc.ProtoI2C || d.Proto == framecalc.ProtoSPI || d.ChB == d.ChA {
		d.ChB = 1 - d.ChA // data on the OTHER channel
	}
	d.Format = mod3(d.Format)
	view := mod5(v.ViewMode)

	c.mu.Lock()
	c.viewMode = view
	c.mathMode = mod5(v.Display.MathMode)
	c.showMeas, c.persist = v.Display.ShowMeas, v.Display.Persist
	c.chDisp = [2]bool{!v.Display.Hidden[0], !v.Display.Hidden[1]}
	var inv *[2]bool
	if ds := v.Display; ds.Set {
		c.curOn, c.curType = ds.CurMode == 1 || ds.CurMode == 2, max(0, min(ds.CurMode-1, 1))
		// the cursors' signal times and levels; an older file's screen
		// places are anchored at the scales restored
		if ds.CurAt && finiteAll(ds.CurT[0], ds.CurT[1], ds.CurV[0], ds.CurV[1]) {
			c.curT, c.curV, c.curVCh, c.curPend = ds.CurT, ds.CurV, ds.CurVCh&1, nil
		} else {
			p := [2][2]float64{}
			for k := 0; k < 2; k++ {
				p[0][k], p[1][k] = clampF(ds.CurX[k], 0, 1), clampF(ds.CurY[k], 0, 1)
			}
			c.curPend, c.curVCh = &p, 0
		}
		c.curSel = ds.CurSel & 1
		c.zoom, c.zoomOff = 1, 0
		for _, z := range []int{2, 5, 10, 20, 50} {
			if ds.Zoom == z {
				c.zoom, c.zoomOff = z, clampF(ds.ZoomOff, -0.5, 0.5) // the pan's own range (setZoomOff)
			}
		}
		inv = &ds.Invert
	}
	inverter := c.inverter
	c.decProto, c.decBaud = d.Proto, d.Baud
	c.decGen++
	c.decChA, c.decChB = d.ChA, d.ChB
	c.decCPOL, c.decCPHA, c.decLSB = d.CPOL, d.CPHA, d.LSB
	c.decDelayNs, c.decDelayAuto, c.decFine = clampF(d.DelayNs, spiDelayMinNs, spiDelayMaxNs), d.DelayAuto, d.Fine
	c.auto = autoWatch{}
	c.decFormat = d.Format
	c.decTrig = mod3(d.StreamTrig)
	c.decValue = clampInt(d.StreamValue, 0, streamValueMax)
	if u := v.UI; u.Set {
		ws := wsview.WS(clampInt(u.Workspace, 0, wsview.Count-1))
		for i := range c.ui.views {
			c.ui.views[i] = mod5(u.Views[i])
		}
		c.ui.ws, c.ui.busEye, c.ui.measAll = ws, u.BusEye, u.MeasAll
		c.viewMode = c.ui.views[ws]
		if c.apiDepth == 0 { // a remote's recall leaves the LCD's menus as they are
			c.closeSheetLocked()
			c.closePickLocked()
		}
		c.meas = nil
		for _, m := range u.Meas {
			if m <= 0 {
				continue
			}
			ch, p := (m-1)/32, (m-1)%32
			if ch <= 1 && p < len(measParams) {
				c.meas = append(c.meas, measItem{ch: ch, param: p})
			}
		}
		c.fftSrc = u.FFTSource & 1
		c.stackN = 64
		for _, n := range stackDepths {
			if n == u.MasterDepth {
				c.stackN = n
			}
		}
		c.remoteNotices = u.RemoteNotices
		for _, n := range []int{16, 32, 64, 128} { // the rows' own values; an older file keeps them
			if u.MaskFrames == n {
				c.maskN = n
			}
		}
		if u.MaskTol > 0 {
			c.maskTol = clampInt(u.MaskTol-1, 0, len(maskTols)-1)
		}
		if u.SrCh > 0 {
			c.srCh = (u.SrCh - 1) & 1
		}
		for _, k := range []int{8, 16, 32, 64} {
			if u.SrGrid == k {
				c.srK = k
			}
		}
		if u.SrStop > 0 {
			c.srStopMode = clampInt(u.SrStop-1, 0, srStopFPGAMatch)
			lo, hi := srTargetLimits(c.srStopMode)
			c.srStopVal = []float64{4, 500, 60, 20, 20}[c.srStopMode]
			if u.SrTarget >= lo && u.SrTarget <= hi {
				c.srStopVal = u.SrTarget
			}
		}
	}
	if sc := v.Screen; sc.Set {
		c.saverMin = oneOf(sc.SaverMin, saverMins, defSaverMin)
		c.offMin = oneOf(sc.OffMin, offMins, defOffMin)
	}
	c.mu.Unlock()
	if inv != nil && inverter != nil {
		for ch := 0; ch < 2; ch++ {
			inverter.SetInverted(ch, inv[ch])
		}
	}
	c.syncDecodeView(d.Proto != framecalc.ProtoOff)
	c.restoreSerial(v.Serial)
	if detect {
		c.autoDecode()
	}
}

// syncDecodeView tells the engine whether the display decodes the frame, so
// it sends samples rather than peak-detect pairs while it does.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) syncDecodeView(on bool) {
	if e, ok := c.eng.(interface{ SetDecodeView(bool) }); ok {
		e.SetDecodeView(on)
	}
}

// finiteAll reports whether every value is a number (not NaN or infinite).
// TRLC-LINKS: REQ-SDS-136
func finiteAll(v ...float64) bool {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}
