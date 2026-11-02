// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"math"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/streamview"
	"time"
)

// buildHUD assembles the LCD/SCDP heads-up state from the engine + front end.
// The trigger-level readout is scaled by the SOURCE channel's V/div.
// TRLC-LINKS: REQ-SDS-168
func buildHUD(e *engine.Engine, fe *analog.FrontEnd) lcd.HUD {
	st := e.Snapshot()
	hud := lcd.HUD{
		C1VdivV: 1, C2VdivV: 1, TdivS: st.TdivS,
		TrigSrc: st.TrigSource, TrigRising: st.TrigRising,
		TrigType: st.TrigType, TrigQual: st.TrigQual, SerialArmed: st.SerialMode != 0, ZoneTrig: st.ZoneMode != 0,
		SerialMatch: matchText(e.SerialConfig()),
		Running:     st.Running, Norm: st.Norm, Single: st.Single, RecordS: st.RecordS,
		TrigPosFrac: st.TrigPosFrac, TwoChan: true,
		ShowC1: true, ShowC2: true,
		URL: lcd.DeviceURL(), // cached; re-enumerates at most every few seconds
	}
	if fe != nil {
		idx, _ := fe.Snapshot()
		hud.C1VdivV = analog.Detents[idx[0]].VdivV
		hud.C2VdivV = analog.Detents[idx[1]].VdivV
		hud.Zoom1 = analog.Detents[idx[0]].Zoom
		hud.Zoom2 = analog.Detents[idx[1]].Zoom
		hud.Probe1 = fe.ProbeFactor(0)
		hud.Probe2 = fe.ProbeFactor(1)
		hud.Cpl1 = fe.Coupling(0)
		hud.Cpl2 = fe.Coupling(1)
		// AC removes the DC in software (mean → centre), so the ground marker
		// belongs at centre; leave it at 0 for an AC-coupled channel.
		if st.OffC1 != 0 && hud.Cpl1 != analog.CplAC {
			hud.OffC1V = fe.OffsetVolts(0, st.OffC1)
		}
		if st.OffC2 != 0 && hud.Cpl2 != analog.CplAC {
			hud.OffC2V = fe.OffsetVolts(1, st.OffC2)
		}
	}
	// Loading indicator, after a short grace so fast frames never flash it.
	if st.Busy != "" && st.BusyMs >= 150 {
		hud.Busy, hud.BusyPct, hud.BusyPhase = st.Busy, st.BusyPct, int(st.BusyMs/250)
	}
	hud.ViewPending = st.ViewPending
	hud.Roll = st.Roll
	// A trigger the FPGA decides waits for the signal: NORMAL, and in AUTO a
	// serial match, zones or a qualifier, which the FPGA fires on only. Past
	// 1 s or 2.5 screen spans without a published frame the held frame's
	// TRIG'd state no longer describes now, and the screen says what waits.
	if st.Running {
		since := e.SincePublish()
		hud.WaitS = since.Seconds()
		decided := st.Norm || hud.SerialArmed || hud.ZoneTrig || hud.TrigType != 0
		hud.Waiting = decided && since > time.Duration(math.Max(1, 25*st.TdivS)*float64(time.Second))
	}
	srcVdiv, srcOff := hud.C1VdivV, hud.OffC1V
	if st.TrigSource == 1 {
		srcVdiv, srcOff = hud.C2VdivV, hud.OffC2V
	}
	if st.TrigCode != 0 && srcVdiv > 0 {
		// Include the source channel's offset so the level marker sits in the
		// same display frame as the (offset-shifted) trace and ground marker —
		// otherwise centring a signal with DC pushes the marker off-screen.
		hud.TrigLvlDiv = (e.TrigVoltsAt(st.TrigCode, st.TrigSource) + srcOff) / srcVdiv
		probe := hud.Probe1
		if st.TrigSource == 1 {
			probe = hud.Probe2
		}
		hud.TrigLvlV = e.TrigVoltsAt(st.TrigCode, st.TrigSource) * max(probe, 1)
	}
	if pc := uiCtrl.Load(); pc != nil { //nolint:nestif // the stream page is formatted only inside stream mode; the workspace UI + per-channel display
		if sv := streamCtrl.Load(); sv != nil && pc.StreamMode() {
			v := sv.Snapshot()
			list, top := pc.StreamList()
			hud.StreamOn, hud.Stream = true, v
			hud.StreamList = list || v.State == streamview.Streaming || v.State == streamview.Failed || v.State == streamview.Starting
			// Formatting the page holds the transcript's lock: redo it only when
			// what it shows can have changed (4 Hz while streaming).
			lines := v.Lines
			if v.State == streamview.Streaming {
				lines = 0 // the slot paces a streaming page; its line count grows every tick
			}
			key := streamPageKey{v.State, hud.StreamList, top, lines, v.TriggerLine, streamSlot(hud)}
			if cached := lastStreamPage.Load(); cached != nil && cached.key == key {
				hud.StreamPage = cached.page
			} else {
				switch {
				case v.State == streamview.Streaming:
					hud.StreamPage = sv.Window(streamview.ListRows, top) // -1 follows the newest
				case hud.StreamList:
					hud.StreamPage = sv.Window(streamview.ListRows, top)
				case v.TriggerLine >= 0: // waveform: a strip of the lines around the trigger
					hud.StreamPage = sv.Window(6, v.TriggerLine-2)
				default:
					hud.StreamPage = sv.Window(6, -1)
				}
				lastStreamPage.Store(&cachedStreamPage{key, hud.StreamPage})
			}
		}
		d, v := pc.Snapshot() // the display state and the workspace UI, read together
		hud.ShowC1, hud.ShowC2, hud.ChanExplicit = d.ShowC1, d.ShowC2, true
		hud.ShowMeas, hud.MeasItems = d.ShowMeas, d.MeasItems
		hud.ViewMode, hud.MathMode, hud.FFTSrc = d.ViewMode, d.MathMode, d.FFTSrc
		hud.AutosetBusy, hud.AutosetMsg = d.AutosetBusy, d.AutosetMsg
		hud.Notice, hud.Remote = v.Notice, d.Remote
		hud.UI = v
		hud.Match = lcd.MatchView(pc.MatchEditor())
		hud.Zoom, hud.ZoomOff = d.Zoom, d.ZoomOff
		hud.Persist = d.Persist
		hud.DecProto, hud.DecBaud = d.DecProto, d.DecBaud
		hud.DecChA, hud.DecChB = d.DecChA, d.DecChB
		hud.DecCPOL, hud.DecCPHA, hud.DecLSB = d.DecCPOL, d.DecCPHA, d.DecLSB
		hud.DecParity = d.DecParity
		hud.DecFormat, hud.DecDelayNs = d.DecFormat, d.DecDelayNs
		rv := pc.RefView()
		for i := range 2 {
			hud.RefC1[i], hud.RefC2[i], hud.RefShow[i] = rv[i].C1, rv[i].C2, rv[i].Show
			hud.RefVdiv[i], hud.RefOff[i], hud.RefProbe[i] = rv[i].Vdiv, rv[i].Off, rv[i].Probe
		}
		hud.CurOn, hud.CurType, hud.CurSel = d.CurOn, d.CurType, d.CurSel
		hud.CurX, hud.CurY = d.CurX, d.CurY
		sv := pc.SuperresView()
		hud.SRActive, hud.SRFocus, hud.SRStatus = sv.Active, sv.Focus, sv.Status
		hud.SRBits, hud.SRMean, hud.SRk = sv.Bits, sv.Mean, sv.K
		hud.SRMean2, hud.SRAlign, hud.SRSampleS = sv.Mean2, sv.Align, sv.SampleS
		hud.SRGateLo, hud.SRGateHi, hud.SRN = sv.GateLo, sv.GateHi, sv.N
		hud.SRWinLo, hud.SRWinHi, hud.SRPeriod = sv.WinLo, sv.WinHi, sv.Period
		hud.MaskMsg = pc.MaskStatus()
	}
	if sh := scpiCtrl.Load(); sh != nil { // display-level INVS: the SCPI shadow is the truth
		inv := sh.Inverted()
		hud.Inv1, hud.Inv2 = inv[0], inv[1]
	}
	// Zone/mask overlay state (engine-side test; render parity with the web).
	hud.ZoneMode, hud.MaskMode = st.ZoneMode, st.MaskMode
	hud.MaskPass, hud.MaskFail, hud.MaskSkip = st.MaskPass, st.MaskFail, st.MaskSkip
	hud.MaskStopped = st.MaskStopped
	hud.ZoneSkip = st.ZoneSkip
	if st.ZoneCount > 0 {
		hud.Zones = e.Zones()
	}
	if st.MaskMode > 0 && st.MaskSet {
		if m := e.MaskEnvelope(); m != nil {
			hud.MaskLo, hud.MaskHi, hud.MaskWin = m.Lo, m.Hi, m.WinCols
			hud.MaskTdivS, hud.MaskSampleS = m.TdivS, m.SampleS
		}
	}
	// FRA / Bode: selecting the BODE view (ViewMode 3) arms the engine's
	// accumulation on its reference and DUT channels (the VIEW tab's Bode rows
	// and the web's selectors set them; it re-armed C1 / C2 over a web choice)
	// and feeds the accumulated curve to the renderer; leaving it disarms. The
	// web can also arm FRA — both observe the same accumulated points.
	if hud.ViewMode == 3 {
		e.SetBodeMode(true, st.BodeRef, st.BodeDut)
		hud.BodeRef, hud.BodeDut = st.BodeRef, st.BodeDut
		pts := e.BodePoints()
		hud.BodeFreq = make([]float64, len(pts))
		hud.BodeGain = make([]float64, len(pts))
		hud.BodePhase = make([]float64, len(pts))
		for i, p := range pts {
			hud.BodeFreq[i], hud.BodeGain[i], hud.BodePhase[i] = p.FreqHz, p.GainDB, p.PhaseDeg
		}
	} else if st.BodeMode > 0 && lastViewWasBode.Swap(false) {
		// only the device auto-arm gets auto-disarmed; a web-armed FRA persists
		e.SetBodeMode(false, st.BodeRef, st.BodeDut)
	}
	if hud.ViewMode == 3 {
		lastViewWasBode.Store(true)
	}
	hud.Spect = lcdSpect
	return hud
}
