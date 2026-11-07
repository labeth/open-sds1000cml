// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"time"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// vertScales returns the applied offset volts and volts-per-code (Vdiv/25 —
// the 25-codes/div render scale, spec 10 §7.1) for each channel, using the
// front-end V/div when available.
// TRLC-LINKS: REQ-SDS-164
func (s *Scope) vertScales() (off, vpc [2]float64) {
	vpc = [2]float64{1.0 / 25, 1.0 / 25} // nominal 1 V/div when no front end
	st := s.sc.Snapshot()
	if s.fe != nil {
		// Volts per code on the ANALOG range: the 2 and 5 mV detents are display
		// zooms of the 10 mV range (spec 06), and the page applies the zoom.
		vpc[0] = s.fe.RangeV(0) / 25
		vpc[1] = s.fe.RangeV(1) / 25
		off[0], off[1] = s.lv.OffsetIn(0, st), s.lv.OffsetIn(1, st)
		// Probe attenuation is input-referred: it scales both the per-code
		// volts and the offset so every downstream readout (measurements,
		// cursors, CSV, ground marker) reports the signal at the probe tip.
		p0, p1 := s.fe.ProbeFactor(0), s.fe.ProbeFactor(1)
		vpc[0] *= p0
		vpc[1] *= p1
		off[0] *= p0
		off[1] *= p1
	}
	return off, vpc
}

// scales is how the web scope reads this scope's codes: the front end's
// analog range at 25 codes a division, the offset the board's DAC holds,
// the front end's software couplings, the SCPI inversion shadow and the
// engine's trigger position and run flags.
type scales struct{ s *Scope }

// TRLC-LINKS: REQ-SDS-164
func (v scales) Scales() (off, vpc [2]float64) { return v.s.vertScales() }

// Coupling is the front end's display coupling (DC without a front end).
// TRLC-LINKS: REQ-SDS-163
func (v scales) Coupling(ch int) int {
	if v.s.fe == nil {
		return 0
	}
	return v.s.fe.Coupling(ch)
}

// Probe is channel ch's probe factor (1 without a front end).
// TRLC-LINKS: REQ-SDS-265
func (v scales) Probe(ch int) float64 {
	if v.s.fe == nil {
		return 1
	}
	if p := v.s.fe.ProbeFactor(ch); p > 0 {
		return p
	}
	return 1
}

// Inverted is the SCPI INVS shadow (none without it).
// TRLC-LINKS: REQ-SDS-164
func (v scales) Inverted() [2]bool {
	if v.s.invSrc == nil {
		return [2]bool{}
	}
	return v.s.invSrc()
}

// Position is the engine's trigger position and run flags.
// TRLC-LINKS: REQ-SDS-163
func (v scales) Position() (posFrac float64, running, single bool) {
	st := v.s.sc.Snapshot()
	return st.TrigPosFrac, st.Running, st.Single
}

// leases are the engine's frame leases: raw (decode view), spectrum,
// samples, search, or the web's view window.
type leases struct{ s *Scope }

// Lease renews or ends one of them; false when the engine has no such lease.
// TRLC-LINKS: REQ-SDS-163
func (l leases) Lease(n webserver.Need) bool {
	switch n.Kind {
	case webserver.NeedRaw:
		if sc, ok := l.s.sc.(interface{ LeaseDecodeView(time.Duration) }); ok {
			sc.LeaseDecodeView(n.Hold)
			return true
		}
	case webserver.NeedSpectrum:
		if sc, ok := l.s.sc.(interface{ LeaseSpectrumView(time.Duration) }); ok {
			sc.LeaseSpectrumView(n.Hold)
			return true
		}
	case webserver.NeedSamples:
		if sc, ok := l.s.sc.(interface{ LeaseSampleView(time.Duration) }); ok {
			sc.LeaseSampleView(n.Hold)
			return true
		}
	case webserver.NeedSearch:
		if sc, ok := l.s.sc.(interface{ LeaseSearchView(time.Duration) }); ok {
			sc.LeaseSearchView(n.Hold)
			return true
		}
	case webserver.NeedView:
		if sc, ok := l.s.sc.(interface {
			LeaseViewWindow(engine.ViewSurface, float64, float64, int, time.Duration)
		}); ok {
			sc.LeaseViewWindow(engine.ViewWeb, n.View.A, n.View.B, n.View.Cols, n.Hold)
			return true
		}
	}
	return false
}
