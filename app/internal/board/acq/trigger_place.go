// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// triggerPlace is what places an armed trigger's point on its record: the
// zones fire when the latest window has ended, so the point moves back by
// their fire delay to the edge they qualified; a qualifier (pulse, slope,
// video) fires after its event, so the point moves to the event's level
// crossing (PlaceQualified). The FPGA decided the trigger; only its
// position moves.
type triggerPlace struct {
	zoneDelayS float64 // > 0: the zones' fire delay
	qual       sramcapture.QualifierConfig
	channel    uint8 // the trigger's source
	level      uint8 // its comparator code
}

// placeOf is the placement of a capture armed with cfg.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func placeOf(cfg *sramcapture.Config, zoneDelayS float64) triggerPlace {
	p := triggerPlace{qual: cfg.Qualifier, channel: cfg.TriggerChannel, level: cfg.TriggerLevel}
	if cfg.Zones.Enabled() {
		p.zoneDelayS = zoneDelayS
	}
	return p
}

// placeTrigger places a triggered record's point in every frame read, as
// the main window's codes give it: the zone delay first, then a
// qualifier's event. A frame of the record without the trigger's source,
// or an envelope, keeps the zones' shift only.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func (b *Board) placeTrigger(ws []hal.Window, dst []*sample.Frame) {
	p := b.armedPlace
	if !b.recStatus.Triggered || (p.zoneDelayS <= 0 && !p.qual.Enabled) {
		return
	}
	var main *sample.Frame
	for i, f := range dst {
		if ws[i].Role == sample.Main {
			main = f
		}
	}
	if main == nil || !(main.TriggerAt >= 0) || !(main.SampleS > 0) {
		return
	}
	at := main.TriggerAt
	if p.zoneDelayS > 0 {
		at -= p.zoneDelayS / main.SampleS
	}
	if p.qual.Enabled {
		main.TrigNode = QualName(p.qual.Kind)
		if tr := traceOf(main, int(p.channel)); tr != nil && tr.Codes8 != nil && main.Content != sample.PeakPairs && !main.Flags.Has(sample.Envelope) {
			placed := *main
			placed.TriggerAt = at
			at = PlaceQualified(&placed, tr.Codes8[:main.Len()], tr.Precision, p.qual, p.level)
		}
	}
	shiftS := (at - main.TriggerAt) * main.SampleS
	for _, f := range dst {
		if f.TriggerAt >= 0 && f.SampleS > 0 {
			f.TriggerAt += shiftS / f.SampleS
			if p.qual.Enabled {
				f.TrigNode = main.TrigNode
			}
		}
	}
}

// traceOf is f's trace of channel ch, nil without one.
// TRLC-LINKS: REQ-SDS-011
func traceOf(f *sample.Frame, ch int) *sample.Trace {
	for i := range f.Traces {
		if f.Traces[i].Ch == ch {
			return &f.Traces[i]
		}
	}
	return nil
}
