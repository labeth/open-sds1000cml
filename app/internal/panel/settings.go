// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/settings"
)

// SettingsView reports the controller-owned slice of the persisted setup
// (settings.Panel surface): the decode setup, the display state, and the
// workspace UI, so a restart or a recalled setup returns where it was.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211
func (c *Controller) SettingsView() settings.ViewState {
	c.mu.Lock()
	defer c.mu.Unlock()
	ui := settings.UI{Set: true, Workspace: int(c.ui.ws), BusEye: c.ui.busEye, MeasAll: c.ui.measAll,
		FFTSource: c.fftSrc, MasterDepth: c.stackN, RemoteNotices: c.remoteNotices}
	for i := range ui.Views {
		ui.Views[i] = c.ui.views[i]
	}
	ui.Views[c.ui.ws] = c.viewMode
	for i, it := range c.meas {
		if i < len(ui.Meas) {
			ui.Meas[i] = it.ch*32 + it.param + 1
		}
	}
	return settings.ViewState{
		ViewMode: c.viewMode,
		// Math, measurements, persistence and channel visibility survived no
		// restart (bug hunt 2026-10-10).
		Display: settings.Display{MathMode: c.mathMode, ShowMeas: c.showMeas, Persist: c.persist,
			Hidden: [2]bool{!c.chDisp[0], !c.chDisp[1]}},
		Decode: settings.Decode{
			Proto: c.decProto, Baud: c.decBaud,
			ChA: c.decChA, ChB: c.decChB,
			CPOL: c.decCPOL, CPHA: c.decCPHA,
			Format:     c.decFormat,
			StreamTrig: c.decTrig, StreamValue: c.decValue,
		},
		UI: ui,
	}
}

// ApplySettingsView restores the controller-owned setup, enforcing the
// domains the BUS and VIEW tabs keep: proto a decoder, a baud the decoders
// take, clock and data on opposite channels for the two-signal protocols,
// format and view in range, a workspace and its views. Hostile values
// degrade to defaults — never a panic, never an out-of-domain field. A
// recalled workspace is entered as F1..F5 enters it (its view applied).
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211
func (c *Controller) ApplySettingsView(v settings.ViewState) {
	d := v.Decode
	if d.Proto < framecalc.ProtoOff || d.Proto >= framecalc.NumProtos {
		d.Proto = framecalc.ProtoOff
	}
	if d.Baud < 50 || d.Baud > 20000000 {
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
	c.decProto, c.decBaud = d.Proto, d.Baud
	c.decChA, c.decChB = d.ChA, d.ChB
	c.decCPOL, c.decCPHA = d.CPOL, d.CPHA
	c.decFormat = d.Format
	c.decTrig = mod3(d.StreamTrig)
	c.decValue = (d.StreamValue%0x10000 + 0x10000) % 0x10000
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
	}
	c.mu.Unlock()
	c.syncDecodeView(d.Proto != framecalc.ProtoOff)
}

// syncDecodeView tells the engine whether the display decodes the frame, so
// it sends samples rather than peak-detect pairs while it does.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) syncDecodeView(on bool) {
	if e, ok := c.eng.(interface{ SetDecodeView(bool) }); ok {
		e.SetDecodeView(on)
	}
}
