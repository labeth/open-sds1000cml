// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS

package settings

import (
	"errors"
	"math"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
	core "github.com/labeth/open-dso-core/settings"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Engine is the instrument's controls the persisted setup flows through
// (the same calls web /api/set and the panel make; every setter clamps and
// stages — nothing here touches the bus).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Engine = instrument.Controls

// Analog is the vertical front-end surface (implemented by *analog.FrontEnd,
// producer-direct off the GPMC bus). May be nil when SPI is unavailable —
// vertical setup is then neither collected nor restored.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Analog interface {
	Emitted() bool
	VdivV(ch int) float64
	SetVdivV(ch int, v float64) error
	SetOffset(ch int, volts float64) uint16
	OffsetReqV(ch int) float64
	SetCoupling(ch, mode int) error
	Coupling(ch int) int
	SetProbe(ch int, x float64)
	ProbeFactor(ch int) float64
}

// Extension is the board's part of the setup: the trigger level, which the
// file keeps as the board stores it (the SDS1000CML+'s comparator DAC code,
// level_code). May be nil: the level is then neither collected nor restored.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Extension interface {
	CollectTrigger(t *Trigger)
	ApplyTrigger(t Trigger)
}

// ViewState is the controller-owned slice of the setup.
type ViewState = core.ViewState

// Panel is the front-panel controller surface (implemented by
// *panel.Controller). ApplySettingsView must enforce the same domains the
// DECODE/DISPLAY menus keep. May be nil.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Panel interface {
	SettingsView() ViewState
	ApplySettingsView(v ViewState)
}

// Collect assembles the current setup from the authoritative owners: the
// engine stats snapshot, the analog front end's shadows and the controller's
// view state. Cheap (mutex-guarded copies, zero bus access) — safe to call
// from the saver's poll goroutine.
// TRLC-LINKS: REQ-SDS-089
func Collect(eng Engine, fe Analog, pc Panel, ext Extension) Settings {
	s := Settings{Version: Version}
	if eng != nil {
		st := eng.Snapshot()
		s.TdivS = st.TdivSet()
		s.Trigger = Trigger{
			Qualifiers: st.TrigQual,
			Rising:     st.TrigRising,
			Source:     st.TrigSource,
			Type:       st.TrigType,
			Norm:       st.Norm,
			HoldoffS:   st.HoldoffS,
			PosFrac:    st.TrigPosFrac,
		}
		if ext != nil {
			ext.CollectTrigger(&s.Trigger)
		}
		s.Acq = Acq{Mode: st.AcqMode, AvgCount: st.AvgCount, EresLen: st.EresLen, PrecisionRateHz: st.PrecisionRateHz}
		if md, ok := eng.(interface{ MemDepth() int }); ok {
			s.Acq.MemDepth = md.MemDepth()
		}
		s.Acq.RollStream = st.RollStream
		// OffC1/OffC2 are the staged DAC codes; 0 means the boot-inherited
		// offset was never touched (spec 06 §4.4) — record that so restore
		// won't drive an explicit 0 V over an untouched channel.
		s.Ch[0].OffsetSet = st.OffC1 != 0
		s.Ch[1].OffsetSet = st.OffC2 != 0
		if _, ok := eng.(interface{ SetBodeMode(bool, int, int) }); ok {
			s.Bode = Bode{Set: true, Ref: st.BodeRef & 1, Dut: st.BodeDut & 1}
		}
	}
	if fe != nil {
		s.VertSet = fe.Emitted()
		bwl, hasBWL := fe.(interface{ BWL(ch int) bool })
		// One lock a channel where the front end reads them together.
		all, hasAll := fe.(interface {
			ChannelState(ch int) (offV float64, cpl int, probe float64, bwl bool)
		})
		for ch := range 2 {
			s.Ch[ch].VdivV = fe.VdivV(ch)
			c := &s.Ch[ch]
			if hasAll {
				c.OffsetV, c.Coupling, c.Probe, c.BWL = all.ChannelState(ch)
				continue
			}
			c.OffsetV = fe.OffsetReqV(ch)
			c.Coupling = fe.Coupling(ch)
			c.Probe = fe.ProbeFactor(ch)
			if hasBWL {
				c.BWL = bwl.BWL(ch)
			}
		}
	}
	if pc != nil {
		v := pc.SettingsView()
		s.ViewMode = v.ViewMode
		s.Decode = v.Decode
		s.Display = v.Display
		s.UI = v.UI
		s.Serial = v.Serial
		s.Screen = v.Screen
		s.Sound = v.Sound
	}
	return s
}

// Apply restores a setup through the owning setters — the SAME paths the
// panel and web /api/set use, so every clamp and side-effect (band staging,
// offset re-anchoring, trigger-map updates) applies. Out-of-domain values are
// clamped by the setters or skipped with a log line; Apply never panics on a
// hostile Settings value.
// TRLC-LINKS: REQ-SDS-072
func Apply(s Settings, eng Engine, fe Analog, pc Panel, ext Extension, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if eng != nil {
		applyEngine(s, eng, ext, logf)
	}
	if fe != nil {
		applyChannels(s, fe, logf)
	}
	if b, ok := eng.(interface{ SetBodeMode(bool, int, int) }); ok && s.Bode.Set {
		ref, dut := s.Bode.Ref&1, s.Bode.Dut&1
		if ref == dut {
			dut = 1 - ref
		}
		b.SetBodeMode(eng.Snapshot().BodeMode != 0, ref, dut) // the armed state stays as it is
	}
	if pc != nil {
		pc.ApplySettingsView(ViewState{
			ViewMode: s.ViewMode, Decode: s.Decode, Display: s.Display, UI: s.UI, Serial: s.Serial,
			Screen: s.Screen, Sound: s.Sound,
		})
	}
}

// applyEngine restores the acquisition and trigger settings.
// TRLC-LINKS: REQ-SDS-072
func applyEngine(s Settings, eng Engine, ext Extension, logf func(string, ...any)) {
	if finite(s.TdivS) && s.TdivS > 0 {
		if _, ok := eng.SetTdiv(s.TdivS); !ok {
			logf("settings: tdiv %g not in ladder — skipped", s.TdivS)
		}
	}
	eng.SetNorm(s.Trigger.Norm)
	eng.SetTrigSlope(s.Trigger.Rising)
	eng.SetTrigSource(s.Trigger.Source) // clamps to 0/1
	eng.SetTrigType(s.Trigger.Type)     // clamps to edge on out-of-range
	if ext != nil {
		ext.ApplyTrigger(s.Trigger)
	}
	if p := s.Trigger.PosFrac; finite(p) && p > 0 && p <= 1 {
		eng.SetTrigPosFrac(p)
	}
	if s.Acq.MemDepth > 0 {
		eng.SetMemDepth(s.Acq.MemDepth) // clamps to the backend's range
	}
	if finite(s.Trigger.HoldoffS) {
		eng.SetHoldoff(s.Trigger.HoldoffS) // clamps [0, 10] s
	}
	if rate, ok := eng.(interface{ SetPrecisionRate(float64) float64 }); ok && finite(s.Acq.PrecisionRateHz) && s.Acq.PrecisionRateHz > 0 {
		rate.SetPrecisionRate(s.Acq.PrecisionRateHz)
	}
	applyQualifiers(s.Trigger.Qualifiers, eng)
	if rs, ok := eng.(interface{ SetRollStream(bool) }); ok {
		rs.SetRollStream(s.Acq.RollStream)
	}
	eng.SetAcqMode(s.Acq.Mode) // clamps to normal on out-of-range
	if s.Acq.AvgCount > 0 {
		eng.SetAvgCount(s.Acq.AvgCount) // clamps [1, 256]
	}
	if s.Acq.EresLen > 0 {
		eng.SetEresLen(s.Acq.EresLen) // clamps [1, 64] odd
	}
}

// applyQualifiers restores the pulse, slope and video trigger qualifiers. A
// missing qualifier block in older settings has PulseLvl=0.
// TRLC-LINKS: REQ-SDS-072
func applyQualifiers(q engine.TrigQual, eng Engine) {
	if qe, ok := eng.(interface {
		SetPulseParams(float64, float64, float64, int)
		SetSlopeParams(float64, float64, float64, float64, int)
		SetVideoParams(int, int, bool)
	}); ok && q.PulseLvl > 0 {
		if finite(q.PulseLvl) && finite(q.PulseMinNs) && finite(q.PulseMaxNs) && q.PulseMinNs >= 0 && q.PulseMaxNs >= 0 {
			qe.SetPulseParams(q.PulseLvl, q.PulseMinNs, q.PulseMaxNs, q.PulseCond)
		}
		if finite(q.SlopeLo) && finite(q.SlopeHi) && finite(q.SlopeMinNs) && finite(q.SlopeMaxNs) && q.SlopeMinNs >= 0 && q.SlopeMaxNs >= 0 {
			qe.SetSlopeParams(q.SlopeLo, q.SlopeHi, q.SlopeMinNs, q.SlopeMaxNs, q.SlopeCond)
		}
		qe.SetVideoParams(q.VideoStd, q.VideoLine, q.VideoNeg)
	}
}

// applyChannels restores each channel's vertical settings.
// TRLC-LINKS: REQ-SDS-072
func applyChannels(s Settings, fe Analog, logf func(string, ...any)) {
	for ch := range 2 {
		cs := s.Ch[ch]
		// V/div + offset only when the saved front end had actually been
		// driven: restoring boot defaults onto a virgin front end would
		// break the seed-don't-emit startup rule (spec 06 §4.4) for no
		// gain. Coupling/probe are software-only and always safe.
		if s.VertSet {
			var refused *hal.Refusal
			switch err := fe.SetVdivV(ch, cs.VdivV); {
			case errors.As(err, &refused):
				logf("settings: C%d vdiv %g not in ladder — skipped", ch+1, cs.VdivV)
			case err != nil:
				logf("settings: SetVdiv C%d: %v", ch+1, err)
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
		if b, ok := fe.(interface{ SetBWL(ch int, on bool) error }); ok && s.VertSet {
			if err := b.SetBWL(ch, cs.BWL); err != nil {
				logf("settings: BWL C%d: %v", ch+1, err)
			}
		}
	}
}

// finite rejects NaN/±Inf before a value reaches float→hardware-code math
// (encoding/json cannot produce them, but the saver snapshot could in
// principle carry one and Apply is also fuzzed directly).
// TRLC-LINKS: REQ-SDS-072
func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
