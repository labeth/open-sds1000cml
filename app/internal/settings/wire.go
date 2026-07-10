package settings

import (
	"math"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// Engine is the slice of the engine's staging-setter surface the persisted
// setup flows through (the same calls web /api/set and the panel make; every
// setter clamps and stages — nothing here touches the bus).
type Engine interface {
	Snapshot() engine.Stats
	SetTdiv(tdivS float64) (engine.Band, bool)
	SetNorm(on bool)
	SetTrigSlope(rising bool)
	SetTrigSource(ch int)
	SetTrigType(t int)
	SetTrigLevelCode(code uint16) uint16
	SetHoldoff(sec float64) float64
	SetAcqMode(m int)
	SetAvgCount(n int)
	SetEresLen(l int)
}

// Analog is the vertical front-end surface (implemented by *analog.FrontEnd,
// producer-direct off the GPMC bus). May be nil when SPI is unavailable —
// vertical setup is then neither collected nor restored.
type Analog interface {
	Snapshot() (idx [2]int, emitted bool)
	SetVdiv(ch, idx int) error
	SetOffset(ch int, volts float64) uint16
	OffsetReqV(ch int) float64
	SetCoupling(ch, mode int) error
	Coupling(ch int) int
	SetProbe(ch int, x float64)
	ProbeFactor(ch int) float64
}

// ViewState is the controller-owned slice of the setup: the device decode
// config and the display view mode.
type ViewState struct {
	ViewMode int
	Decode   Decode
}

// Panel is the front-panel controller surface (implemented by
// *panel.Controller). ApplySettingsView must enforce the same domains the
// DECODE/DISPLAY menus keep. May be nil.
type Panel interface {
	SettingsView() ViewState
	ApplySettingsView(v ViewState)
}

// Collect assembles the current setup from the authoritative owners: the
// engine stats snapshot, the analog front end's shadows and the controller's
// view state. Cheap (mutex-guarded copies, zero bus access) — safe to call
// from the saver's poll goroutine.
func Collect(eng Engine, fe Analog, pc Panel) Settings {
	s := Settings{Version: Version}
	if eng != nil {
		st := eng.Snapshot()
		s.TdivS = st.TdivS
		s.Trigger = Trigger{
			LevelCode: int(st.TrigCode),
			Rising:    st.TrigRising,
			Source:    st.TrigSource,
			Type:      st.TrigType,
			Norm:      st.Norm,
			HoldoffS:  st.HoldoffS,
		}
		s.Acq = Acq{Mode: st.AcqMode, AvgCount: st.AvgCount, EresLen: st.EresLen}
		// OffC1/OffC2 are the staged DAC codes; 0 means the boot-inherited
		// offset was never touched (spec 06 §4.4) — record that so restore
		// won't drive an explicit 0 V over an untouched channel.
		s.Ch[0].OffsetSet = st.OffC1 != 0
		s.Ch[1].OffsetSet = st.OffC2 != 0
	}
	if fe != nil {
		idx, emitted := fe.Snapshot()
		s.VertSet = emitted
		for ch := 0; ch < 2; ch++ {
			if idx[ch] >= 0 && idx[ch] < len(analog.Detents) {
				s.Ch[ch].VdivV = analog.Detents[idx[ch]].VdivV
			}
			s.Ch[ch].OffsetV = fe.OffsetReqV(ch)
			s.Ch[ch].Coupling = fe.Coupling(ch)
			s.Ch[ch].Probe = fe.ProbeFactor(ch)
		}
	}
	if pc != nil {
		v := pc.SettingsView()
		s.ViewMode = v.ViewMode
		s.Decode = v.Decode
	}
	return s
}

// Apply restores a setup through the owning setters — the SAME paths the
// panel and web /api/set use, so every clamp and side-effect (band staging,
// offset re-anchoring, trigger-map updates) applies. Out-of-domain values are
// clamped by the setters or skipped with a log line; Apply never panics on a
// hostile Settings value.
func Apply(s Settings, eng Engine, fe Analog, pc Panel, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if eng != nil {
		if finite(s.TdivS) && s.TdivS > 0 {
			if _, ok := eng.SetTdiv(s.TdivS); !ok {
				logf("settings: tdiv %g not in ladder — skipped", s.TdivS)
			}
		}
		eng.SetNorm(s.Trigger.Norm)
		eng.SetTrigSlope(s.Trigger.Rising)
		eng.SetTrigSource(s.Trigger.Source) // clamps to 0/1
		eng.SetTrigType(s.Trigger.Type)     // clamps to edge on out-of-range
		// Level code 0 = "boot comparator untouched" — never stage it. The
		// uint16 conversion is guarded in float-free int space (an
		// out-of-range conversion is implementation-defined on ARMv7).
		if c := s.Trigger.LevelCode; c > 0 && c <= 0xFFFF {
			eng.SetTrigLevelCode(uint16(c)) // clamps to the operational window
		}
		if finite(s.Trigger.HoldoffS) {
			eng.SetHoldoff(s.Trigger.HoldoffS) // clamps [0, 10] s
		}
		eng.SetAcqMode(s.Acq.Mode) // clamps to normal on out-of-range
		if s.Acq.AvgCount > 0 {
			eng.SetAvgCount(s.Acq.AvgCount) // clamps [1, 256]
		}
		if s.Acq.EresLen > 0 {
			eng.SetEresLen(s.Acq.EresLen) // clamps [1, 64] odd
		}
	}
	if fe != nil {
		for ch := 0; ch < 2; ch++ {
			cs := s.Ch[ch]
			// V/div + offset only when the saved front end had actually been
			// driven: restoring boot defaults onto a virgin front end would
			// break the seed-don't-emit startup rule (spec 06 §4.4) for no
			// gain. Coupling/probe are software-only and always safe.
			if s.VertSet {
				if idx, ok := analog.PlanVdiv(cs.VdivV); ok {
					if err := fe.SetVdiv(ch, idx); err != nil {
						logf("settings: SetVdiv C%d: %v", ch+1, err)
					}
				} else {
					logf("settings: C%d vdiv %g not in ladder — skipped", ch+1, cs.VdivV)
				}
			}
			if cs.OffsetSet && finite(cs.OffsetV) {
				fe.SetOffset(ch, cs.OffsetV) // per-tier offset law clamps
			}
			if cs.Coupling >= analog.CplDC && cs.Coupling <= analog.CplGND {
				_ = fe.SetCoupling(ch, cs.Coupling)
			}
			if cs.Probe == 1 || cs.Probe == 10 || cs.Probe == 100 { // same domain the web enforces
				fe.SetProbe(ch, cs.Probe)
			}
		}
	}
	if pc != nil {
		pc.ApplySettingsView(ViewState{ViewMode: s.ViewMode, Decode: s.Decode})
	}
}

// finite rejects NaN/±Inf before a value reaches float→hardware-code math
// (encoding/json cannot produce them, but the saver snapshot could in
// principle carry one and Apply is also fuzzed directly).
func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
