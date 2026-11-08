// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"slices"

	"github.com/labeth/open-dso-core/hal/keytable"
)

// accelSteps are the magnitudes one event of a continuous knob carries
// (accel): the raw count up to 9, then 50 and 100.
var accelSteps = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 50, 100}

// Table is this panel's key table: every wired key and knob push by the UI
// model's name (the PCB wiring has no other names), each sent as a press
// alone (the matrix decode reports no release), and every knob in the
// FPGA's priority order, a stepped one a detent an event, a continuous one
// in its acceleration's magnitudes. The browser demo's panel is held to it.
// TRLC-LINKS: REQ-SDS-135
func Table() keytable.Table {
	var t keytable.Table
	codes := make([]int, 0, len(keyCodes))
	for c := range keyCodes {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	for _, c := range codes {
		t.Keys = append(t.Keys, keytable.Key{Panel: keyCodes[c], Name: keyCodes[c]})
	}
	for _, k := range knobs {
		kn := keytable.Knob{Panel: k.name, Name: k.name, Max: 1}
		if !k.stepped {
			kn.Max, kn.Steps = 0, slices.Clone(accelSteps)
		}
		t.Knobs = append(t.Knobs, kn)
	}
	return t
}
