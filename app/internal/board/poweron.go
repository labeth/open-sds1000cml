// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

package board

import (
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
)

// PowerOn is the SDS1000CML+'s instrument state at power-on, before a
// setup is restored, as its engine starts: running in AUTO at 500 us/div,
// 16 averages, ERES off, precision words at 31.25 MHz, and the pulse,
// slope and video qualifiers' first settings. The channels are the front
// end's (the composition projects them); n is the channel count.
// TRLC-LINKS: REQ-SDS-089, REQ-SDS-098
func PowerOn(n int) instrument.State {
	st := instrument.State{
		Channels: make([]hal.Channel, n), Running: true, Sweep: hal.Auto,
		TimeDivS: 500e-6, Averages: 16, EresLen: 1, PrecisionRateHz: 31.25e6,
		Qual: instrument.TrigQual{VideoNeg: true, PulseLvl: 0.5, SlopeLo: 0.2, SlopeHi: 0.8},
	}
	st.Trigger.Fill(n)
	return st
}
