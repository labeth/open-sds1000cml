// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"reflect"
	"testing"
)

// TRLC-LINKS: REQ-SDS-135
func TestUnassignedPhysicalNamesReachDispatcherWithoutEffects(t *testing.T) {
	for _, name := range []string{"print", "saverecall", "set50", "defaultsetup", "force", "help", "ch1pospush", "ch2pospush", "tdivpush", "horizpospush"} {
		t.Run(name, func(t *testing.T) {
			c, eng, fe := newC(t)
			before := c.MenuView()
			calls, analogCalls := len(eng.calls), len(fe.calls)
			if !c.InjectButton(name) {
				t.Fatal("physical key was rejected")
			}
			select {
			case dispatch := <-c.inject:
				dispatch()
			default:
				t.Fatal("accepted physical key never reached the dispatch queue")
			}
			if len(eng.calls) != calls || len(fe.calls) != analogCalls || !reflect.DeepEqual(before, c.MenuView()) {
				t.Fatal("unassigned key changed instrument state")
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestHoldoffStepsToNearestSettingInRequestedDirection(t *testing.T) {
	for _, tc := range []struct {
		current   float64
		direction int
		want      float64
	}{
		{0.0025, 1, 0.01}, {0.0025, -1, 0.001}, {0.001, 1, 0.01}, {0.001, -1, 0.0001}, {0, -1, 0}, {1, 1, 1},
	} {
		if got := nextHoldoff(tc.current, tc.direction); got != tc.want {
			t.Errorf("holdoff %g dir %d: %g, want %g", tc.current, tc.direction, got, tc.want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestEmptyTriggerQualifierKeysDoNotMoveFocus(t *testing.T) {
	for _, tc := range []struct{ kind, firstEmpty int }{{1, 4}, {3, 3}} {
		c, eng, _ := newC(t)
		eng.stats.TrigType = tc.kind
		c.openMenu(pgTrigQ)
		before := c.MenuView()
		for slot := tc.firstEmpty; slot < 5; slot++ {
			c.menuCycle(slot, 1)
			if !reflect.DeepEqual(before, c.MenuView()) {
				t.Fatalf("type %d empty slot %d changed view", tc.kind, slot)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestSRAMAcquisitionMenuUsesBackendCapabilities(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.BandKind = "sram"
	eng.stats.MemDepth = 1048576
	c.openMenu(pgAcq)
	before := len(eng.calls)
	c.menuCycle(2, 1)
	c.menuCycle(3, 1)
	if len(eng.calls) != before {
		t.Fatal("unsupported SRAM controls issued engine commands")
	}
	eng.stats.AcqMode = 4
	if c.MenuView().Items[0].Value != "Precision" {
		t.Fatal("precision mode mislabeled")
	}
	c.menuCycle(0, 1)
	if got := eng.calls[len(eng.calls)-1]; got.what != "acq" || got.a != 0 {
		t.Fatalf("precision did not wrap to normal: %+v", got)
	}
}
