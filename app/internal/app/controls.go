// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
)

// modelAnalog is the analog front end as the controllers drive it: its
// setters are the front end's (its shadows answer at once, its relays and
// gains are written on the owner with nothing armed), and each writes the
// channel's setting, as the front end applies it, into the instrument
// model, with the trigger level's volts on the new range. The model is
// attached once the core's application built it (composition.attach).
type modelAnalog struct {
	*analog.FrontEnd
	m      *instrument.Model
	levels *board.Levels
}

// project puts channel ch's setting into the model.
// TRLC-LINKS: REQ-SDS-015
func (a *modelAnalog) project(ch int) {
	if ch < 0 || ch > 1 || a.m == nil { // no model yet: attach projects both
		return
	}
	c := a.Get(ch)
	_ = a.m.Change(func(s *instrument.State) error {
		if ch < len(s.Channels) {
			s.Channels[ch] = c
		}
		return nil
	})
	if a.levels != nil {
		a.levels.Reproject()
	}
}

// Set applies c to channel ch (hal.FrontEnd).
// TRLC-LINKS: REQ-SDS-015
func (a *modelAnalog) Set(ch int, c hal.Channel) (hal.Channel, error) {
	got, err := a.FrontEnd.Set(ch, c)
	a.project(ch)
	return got, err
}

// SetVdiv sets channel ch's detent.
// TRLC-LINKS: REQ-SDS-015
func (a *modelAnalog) SetVdiv(ch, idx int) error {
	err := a.FrontEnd.SetVdiv(ch, idx)
	a.project(ch)
	return err
}

// SetVdivV sets channel ch's V/div at the BNC.
// TRLC-LINKS: REQ-SDS-015
func (a *modelAnalog) SetVdivV(ch int, v float64) error {
	err := a.FrontEnd.SetVdivV(ch, v)
	a.project(ch)
	return err
}

// SetProbe sets channel ch's probe factor.
// TRLC-LINKS: REQ-SDS-015
func (a *modelAnalog) SetProbe(ch int, x float64) {
	a.FrontEnd.SetProbe(ch, x)
	a.project(ch)
}

// SetCoupling sets channel ch's coupling.
// TRLC-LINKS: REQ-SDS-096
func (a *modelAnalog) SetCoupling(ch, mode int) error {
	err := a.FrontEnd.SetCoupling(ch, mode)
	a.project(ch)
	return err
}

// SetBWL sets channel ch's bandwidth limit.
// TRLC-LINKS: REQ-SDS-097
func (a *modelAnalog) SetBWL(ch int, on bool) error {
	err := a.FrontEnd.SetBWL(ch, on)
	a.project(ch)
	return err
}

// SetOffset stages channel ch's offset at the BNC and returns its DAC code.
// TRLC-LINKS: REQ-SDS-095
func (a *modelAnalog) SetOffset(ch int, volts float64) uint16 {
	code := a.FrontEnd.SetOffset(ch, volts)
	a.project(ch)
	return code
}

// SetOffsetV stages channel ch's offset at the BNC.
// TRLC-LINKS: REQ-SDS-095
func (a *modelAnalog) SetOffsetV(ch int, volts float64) {
	a.FrontEnd.SetOffsetV(ch, volts)
	a.project(ch)
}

// OffsetSet reports that channel ch's offset was set (its DAC code is
// staged), for the settings file.
// TRLC-LINKS: REQ-SDS-095, REQ-SDS-089
func (a *modelAnalog) OffsetSet(ch int) bool { return a.levels != nil && a.levels.OffsetSet(ch) }
