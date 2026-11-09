// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"math"
	"time"

	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/lcd"
)

// hud assembles the LCD/SCDP heads-up state from the engine and the front
// end: the levels and offsets are the DACs' codes read back through the
// calibration, which the core's HUD(n) over the instrument state cannot
// see until the levels are volts in the Model. The trigger-level readout
// is scaled by the source channel's V/div.
// TRLC-LINKS: REQ-SDS-168
func (c *composition) hud() render.HUD {
	e, fe := c.ctl, c.fe
	st := e.Snapshot()
	hud := render.HUD{
		Ch: [render.MaxChans]render.Channel{{VdivV: 1, Show: true}, {VdivV: 1, Show: true}}, TdivS: st.TdivS,
		TrigSrc: st.TrigSource, TrigRising: st.TrigRising,
		TrigType: st.TrigType, TrigQual: render.TrigQual(st.TrigQual), SerialArmed: st.SerialMode != 0, ZoneTrig: st.ZoneMode != 0,
		SerialMatch: matchText(e.SerialConfig()),
		Running:     st.Running, Norm: st.Norm, Single: st.Single, RecordS: st.RecordS,
		TrigPosFrac: st.TrigPosFrac, NumChans: 2,
		URL: lcd.DeviceURL(), // cached; re-enumerates at most every few seconds
	}
	if fe != nil {
		hudFrontEnd(&hud, fe, st)
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
	hudTrigLevel(&hud, c.e, st)
	if pc := c.pc; pc != nil {
		pc.FillStreamHUD(&hud)
		pc.FillHUD(&hud)
	}
	if sh := c.scpi; sh != nil { // display-level INVS: the SCPI shadow is the truth
		inv := sh.Inverted()
		hud.Ch[0].Inv, hud.Ch[1].Inv = inv[0], inv[1]
	}
	hudZonesMask(&hud, c.e, st)
	c.hudBode(&hud, st)
	return hud
}

// hudFrontEnd fills the channels' scales, probes, couplings and offsets
// from the front end.
// TRLC-LINKS: REQ-SDS-168
func hudFrontEnd(hud *render.HUD, fe *analog.FrontEnd, st engine.Stats) {
	idx, _ := fe.Snapshot()
	hud.Ch[0].VdivV = analog.Detents[idx[0]].VdivV
	hud.Ch[1].VdivV = analog.Detents[idx[1]].VdivV
	hud.Ch[0].Zoom = analog.Detents[idx[0]].Zoom
	hud.Ch[1].Zoom = analog.Detents[idx[1]].Zoom
	hud.Ch[0].Probe = fe.ProbeFactor(0)
	hud.Ch[1].Probe = fe.ProbeFactor(1)
	hud.Ch[0].Cpl = fe.Coupling(0)
	hud.Ch[1].Cpl = fe.Coupling(1)
	// AC removes the DC in software (mean → centre), so the ground marker
	// belongs at centre; leave it at 0 for an AC-coupled channel.
	if st.OffC1 != 0 && hud.Ch[0].Cpl != analog.CplAC {
		hud.Ch[0].OffV = fe.OffsetVolts(0, st.OffC1)
	}
	if st.OffC2 != 0 && hud.Ch[1].Cpl != analog.CplAC {
		hud.Ch[1].OffV = fe.OffsetVolts(1, st.OffC2)
	}
}

// hudTrigLevel fills the trigger level marker (divisions, on the source
// channel's scale and offset) and its readout (volts at the probe tip).
// TRLC-LINKS: REQ-SDS-168
func hudTrigLevel(hud *render.HUD, e *engine.Engine, st engine.Stats) {
	srcVdiv, srcOff := hud.Ch[0].VdivV, hud.Ch[0].OffV
	if st.TrigSource == 1 {
		srcVdiv, srcOff = hud.Ch[1].VdivV, hud.Ch[1].OffV
	}
	if st.TrigCode != 0 && srcVdiv > 0 {
		// Include the source channel's offset so the level marker sits in the
		// same display frame as the (offset-shifted) trace and ground marker —
		// otherwise centring a signal with DC pushes the marker off-screen.
		hud.TrigLvlDiv = (e.TrigVoltsAt(st.TrigCode, st.TrigSource) + srcOff) / srcVdiv
		probe := hud.Ch[0].Probe
		if st.TrigSource == 1 {
			probe = hud.Ch[1].Probe
		}
		hud.TrigLvlV = e.TrigVoltsAt(st.TrigCode, st.TrigSource) * max(probe, 1)
	}
}

// hudZonesMask fills the zone and mask overlays (engine-side test; render
// parity with the web).
// TRLC-LINKS: REQ-SDS-168
func hudZonesMask(hud *render.HUD, e *engine.Engine, st engine.Stats) {
	hud.ZoneMode, hud.MaskMode = st.ZoneMode, st.MaskMode
	hud.MaskPass, hud.MaskFail, hud.MaskSkip = st.MaskPass, st.MaskFail, st.MaskSkip
	hud.MaskStopped = st.MaskStopped
	hud.ZoneSkip = st.ZoneSkip
	if st.ZoneCount > 0 {
		zs := e.Zones() // the engine's copy
		hud.Zones = make([]render.Zone, len(zs))
		for i, z := range zs {
			hud.Zones[i] = render.Zone(z)
		}
	}
	if st.MaskMode > 0 && st.MaskSet {
		if m := e.MaskEnvelope(); m != nil {
			hud.MaskLo, hud.MaskHi, hud.MaskWin = m.Lo, m.Hi, m.WinCols
			hud.MaskTdivS, hud.MaskSampleS = m.TdivS, m.SampleS
		}
	}
}

// hudBode arms FRA / Bode for the BODE view and fills its curve.
// Selecting the BODE view (ViewMode 3) arms the engine's accumulation on its
// reference and DUT channels (the VIEW tab's Bode rows and the web's
// selectors set them; it re-armed C1 / C2 over a web choice) and feeds the
// accumulated curve to the renderer; leaving it disarms. The web can also
// arm FRA — both observe the same accumulated points.
// TRLC-LINKS: REQ-SDS-168
func (c *composition) hudBode(hud *render.HUD, st engine.Stats) {
	e := c.e
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
	} else if st.BodeMode > 0 && c.lastViewWasBode.Swap(false) {
		// only the device auto-arm gets auto-disarmed; a web-armed FRA persists
		e.SetBodeMode(false, st.BodeRef, st.BodeDut)
	}
	if hud.ViewMode == 3 {
		c.lastViewWasBode.Store(true)
	}
}
