// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

package analog

import (
	"context"
	"fmt"
	"math"

	"github.com/labeth/open-dso-core/hal"
)

// The front end as the HAL's FrontEnd: every value in volts at the probe
// tip, the ladder's steps the detents times the probe. Its own methods
// (SetVdivV, OffsetReqV, SetOffset) speak volts at the BNC, where the
// detents and the offset law are defined.

var _ hal.FrontEnd = (*FrontEnd)(nil)

// bwLimitHz is the bandwidth limit's corner.
const bwLimitHz = 20e6

// Get is channel ch's setting: its V/div step, the offset asked (the DAC
// holds the nearest OffsetGrid step), coupling, bandwidth limit and probe.
// TRLC-LINKS: REQ-SDS-015, REQ-SDS-095
func (f *FrontEnd) Get(ch int) hal.Channel {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch &= 1
	p := f.probe[ch]
	if p < 1 {
		p = 1
	}
	c := hal.Channel{
		On: true, VoltsDiv: Detents[f.idx[ch]].VdivV * p, OffsetV: f.offReqV[ch] * p,
		Coupling: hal.Coupling(f.cpl[ch]), Probe: p, Impedance: 1e6,
	}
	if f.bwl[ch] {
		c.BWLimit = bwLimitHz
	}
	return c
}

// Set applies c to channel ch, changing only what differs from Get: the
// probe first (it scales the other values), then the V/div (a ladder step,
// or a refusal), the offset, the coupling and the bandwidth limit (on at
// any corner asked, as the hardware has one). It returns what is applied.
// TRLC-LINKS: REQ-SDS-015, REQ-SDS-095, REQ-SDS-096, REQ-SDS-097
func (f *FrontEnd) Set(ch int, c hal.Channel) (hal.Channel, error) {
	if ch < 0 || ch > 1 {
		return f.Get(0), hal.Refuse(fmt.Sprintf("channel %d", ch+1), "this scope has two channels")
	}
	cur := f.Get(ch)
	if c.Probe != cur.Probe && c.Probe >= 1 {
		f.SetProbe(ch, c.Probe)
	}
	p := f.ProbeFactor(ch)
	if c.VoltsDiv != cur.VoltsDiv {
		if err := f.SetVdivV(ch, c.VoltsDiv/p); err != nil {
			return f.Get(ch), err
		}
	}
	if c.OffsetV != cur.OffsetV {
		f.SetOffset(ch, c.OffsetV/p)
	}
	if c.Coupling != cur.Coupling {
		if err := f.SetCoupling(ch, int(c.Coupling)); err != nil {
			return f.Get(ch), hal.Refuse("coupling", err.Error())
		}
	}
	if on := c.BWLimit > 0; on != f.BWL(ch) {
		if err := f.SetBWL(ch, on); err != nil {
			return f.Get(ch), err
		}
	}
	return f.Get(ch), nil
}

// Preview is what Set(ch, c) would apply, with nothing applied: the same
// order (the probe first, as it scales the other values), the same ladder
// step or refusal, the offset clamped to the new detent's tier, the
// coupling and the bandwidth limit. A refusal applies nothing and returns
// what is applied.
// TRLC-LINKS: REQ-SDS-015, REQ-SDS-095, REQ-SDS-096, REQ-SDS-097
func (f *FrontEnd) Preview(ch int, c hal.Channel) (hal.Channel, error) {
	if ch < 0 || ch > 1 {
		return f.Get(0), hal.Refuse(fmt.Sprintf("channel %d", ch+1), "this scope has two channels")
	}
	cur := f.Get(ch)
	f.mu.Lock()
	idx, offV, cpl := f.idx[ch], f.offReqV[ch], f.cpl[ch]
	f.mu.Unlock()
	p := cur.Probe
	if c.Probe != cur.Probe && c.Probe >= 1 {
		p = c.Probe
	}
	if c.VoltsDiv != cur.VoltsDiv {
		i, ok := PlanVdiv(c.VoltsDiv / p)
		if !ok {
			return cur, hal.Refuse(fmt.Sprintf("%g V/div", c.VoltsDiv/p), "not a step of the V/div ladder")
		}
		idx = i
	}
	if c.OffsetV != cur.OffsetV {
		limit := offsetTierRangeV(idx)
		offV = math.Max(-limit, math.Min(limit, c.OffsetV/p))
	}
	if c.Coupling != cur.Coupling {
		if m := int(c.Coupling); m < CplDC || m > CplGND {
			return cur, hal.Refuse("coupling", fmt.Sprintf("analog: bad coupling ch=%d mode=%d", ch, m))
		}
		cpl = int(c.Coupling)
	}
	out := hal.Channel{
		On: true, VoltsDiv: Detents[idx].VdivV * p, OffsetV: offV * p,
		Coupling: hal.Coupling(cpl), Probe: p, Impedance: 1e6,
	}
	if c.BWLimit > 0 {
		out.BWLimit = bwLimitHz
	}
	return out, nil
}

// Ladder is the V/div steps at the probe tip: the detents times the probe.
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) Ladder(ch int) []hal.Step {
	p := f.ProbeFactor(ch)
	steps := make([]hal.Step, len(Detents))
	for i, d := range Detents {
		steps[i] = hal.Step{VoltsDiv: d.VdivV * p, Zoom: float64(d.Zoom)}
	}
	return steps
}

// OffsetGrid is one offset DAC step at the probe tip on the current detent,
// and the offsets the DAC's ends give.
// TRLC-LINKS: REQ-SDS-095
func (f *FrontEnd) OffsetGrid(ch int) hal.Grid {
	p := f.ProbeFactor(ch)
	lo, hi := f.OffsetVolts(ch, 0xFFFF)*p, f.OffsetVolts(ch, 0)*p
	return hal.Grid{Step: (hi - lo) / 0xFFFF, Min: lo, Max: hi}
}

// Couplings lists the couplings: the relay stays in DC; AC and GND are done
// on the samples.
// TRLC-LINKS: REQ-SDS-096
func (f *FrontEnd) Couplings(int) []hal.CouplingOption {
	return []hal.CouplingOption{{Coupling: hal.DC}, {Coupling: hal.AC, Software: true}, {Coupling: hal.GND, Software: true}}
}

// Calibration names the calibration table in use: "file" or "backup" (the
// unit's, which the vendor's factory calibration wrote), or "defaults" (the
// compiled ones).
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) Calibration() hal.CalSource {
	if f.tab.Source == "defaults" {
		return hal.CalSource{Source: f.tab.Source}
	}
	return hal.CalSource{Source: f.tab.Source, Author: "vendor", Valid: true}
}

// SelfCal is refused: this firmware has no self-calibration and runs on the
// unit's factory table.
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) SelfCal(context.Context, func(float64)) (hal.CalReport, error) {
	return hal.CalReport{Source: f.Calibration()}, hal.Refuse("self-calibration",
		"this firmware has no self-calibration: it runs on the unit's factory calibration table")
}

// SetVdivV sets channel ch's V/div to v volts at the BNC, a ladder step.
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) SetVdivV(ch int, v float64) error {
	idx, ok := PlanVdiv(v)
	if !ok {
		return hal.Refuse(fmt.Sprintf("%g V/div", v), "not a step of the V/div ladder")
	}
	return f.SetVdiv(ch, idx)
}

// Emitted reports whether the front end was ever driven (false: the
// instrument still runs the range it booted with).
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) Emitted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.emitted
}

// VdivV is channel ch's V/div at the BNC, as displayed.
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) VdivV(ch int) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Detents[f.idx[ch&1]].VdivV
}

// OffsetRangeV is channel ch's offset range at the BNC on its current
// detent (±): the coarse attenuator's tier sets it.
// TRLC-LINKS: REQ-SDS-095
func (f *FrontEnd) OffsetRangeV(ch int) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return offsetTierRangeV(f.idx[ch&1])
}

// Zoom is channel ch's display zoom of its analog range (1, 2 or 5).
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) Zoom(ch int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Detents[f.idx[ch&1]].Zoom
}

// VdivsV is the V/div ladder at the BNC.
// TRLC-LINKS: REQ-SDS-015
func VdivsV() []float64 {
	v := make([]float64, len(Detents))
	for i, d := range Detents {
		v[i] = d.VdivV
	}
	return v
}

// RangeV is the V/div at the BNC of the analog range channel ch's codes are
// on: the 2 and 5 mV/div steps zoom the 10 mV range on the display.
// TRLC-LINKS: REQ-SDS-015
func (f *FrontEnd) RangeV(ch int) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Vdiv(f.idx[ch&1])
}
