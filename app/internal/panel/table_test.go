// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"slices"
	"testing"

	"github.com/labeth/open-dso-core/hal"
)

// The key table is the decoder's: every event the matrix decode makes
// passes the table's check, and the table makes no event the decode does
// not (an accelerated knob's magnitudes are exactly accel's).
// TRLC-LINKS: REQ-SDS-135
func TestTableIsTheDecoders(t *testing.T) {
	tab := Table()
	if err := tab.Validate(); err != nil {
		t.Fatal(err)
	}
	var mags []int
	for raw := uint16(1); raw < 1000; raw++ {
		mags = append(mags, accel(raw))
	}
	slices.Sort(mags)
	if got := slices.Compact(mags); !slices.Equal(got, accelSteps) {
		t.Fatalf("accel's magnitudes %v, the table's %v", got, accelSteps)
	}
	var evs []hal.InputEvent
	for i := range 4 {
		for bit := range 16 {
			d := seeded()
			m := idle()
			m[i] &^= 1 << uint(bit)
			for _, ev := range d.decode(m, false) {
				if ev.Key != "" { // an unwired position only wakes the screen
					evs = append(evs, ev)
				}
			}
		}
	}
	for _, k := range knobs {
		for _, raw := range []uint16{1, 5, 9, 12, 30} {
			for _, bit := range []uint{k.bitLo, k.bitHi} {
				m := idle()
				m[k.selIdx] &^= 1 << bit
				m[4] = raw
				if ev, ok := knobOf(m); ok && ev.Knob == k.name {
					evs = append(evs, ev)
				}
			}
		}
	}
	if len(evs) < len(keyCodes)+len(knobs) {
		t.Fatalf("only %d events decoded", len(evs))
	}
	for _, ev := range evs {
		if err := tab.Check(ev, 1); err != nil {
			t.Errorf("%+v: %v", ev, err)
		}
	}
	for _, k := range tab.Keys {
		if got, _ := k.Press(1); len(got) != 1 || !got[0].Down {
			t.Errorf("%s: the table presses %+v, the panel reports a press alone", k.Panel, got)
		}
	}
}
