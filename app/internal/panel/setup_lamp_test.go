// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/settings"
)

// wholeSlots is a setup store that gives the setup a save would write: here
// the controller's own part of it.
type wholeSlots struct {
	memSlots
	c *Controller
}

func (w *wholeSlots) CurrentSetup() settings.Settings {
	v := w.c.SettingsView()
	return settings.Settings{ViewMode: v.ViewMode, Decode: v.Decode, Display: v.Display, UI: v.UI, Serial: v.Serial}
}

// The Save/Recall lamp compares the whole setup a save writes: a change
// outside the old fingerprint (MEASURE's all-values view, BUS's eye, the
// serial match, memory depth...) puts it out, and going back lights it
// again.
// TRLC-LINKS: REQ-SDS-136
func TestSaveRecallLampFollowsTheWholeSetup(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	c.SetSetupMemory(&wholeSlots{memSlots{saved: map[int]bool{}}, c})
	press(t, c, "f4") // MEASURE, statistics
	if res := c.SheetAction("file.setup2", "save"); !res.OK {
		t.Fatalf("save: %+v", res)
	}
	lit := func() bool { c.afterInput(); return eng.lastLEDs()&ledSaveRec != 0 }
	if !lit() {
		t.Fatal("the lamp is out right after the save")
	}
	press(t, c, "f4") // again: all values
	if lit() {
		t.Fatal("the lamp stayed lit on MEASURE's all values, saved on statistics")
	}
	press(t, c, "f4")
	if !lit() {
		t.Fatal("back on statistics, the lamp stayed out")
	}
}

// Between inputs the snapshot is published at most every 150 ms (hashing
// every row and encoding the snapshot on each 40 ms tick and LCD frame takes
// a tenth of the CPU), and a change still reaches it.
// TRLC-LINKS: REQ-SDS-211
func TestSyncPublishIsRateLimited(t *testing.T) {
	c, eng, _ := newC(t)
	k := withClock(c)
	c.SyncLEDs()
	seq := c.PanelStatus().Seq
	eng.set(func(st *engine.Stats) { st.Running = false })
	c.SyncLEDs()
	c.rmu.Lock()
	got := c.remote.status.Seq
	c.rmu.Unlock()
	if got != seq {
		t.Fatalf("published again within 150 ms: seq %d -> %d", seq, got)
	}
	k.advance(150 * time.Millisecond)
	c.SyncLEDs()
	c.rmu.Lock()
	got = c.remote.status.Seq
	c.rmu.Unlock()
	if got == seq {
		t.Fatal("a stop never reached the snapshot")
	}
}

// A setup saved or recalled from the web or SCPI goes through the panel's
// shared store: its FILE row shows what the slot now holds and the
// SAVE/RECALL lamp lights, as for a save on the scope (not "empty" on the
// scope until a restart).
// TRLC-LINKS: REQ-SDS-136
func TestSharedSetupsUpdateTheFileRowsAndLamp(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	m := &wholeSlots{memSlots{saved: map[int]bool{}}, c}
	c.SetSetupMemory(m)
	shared := c.SharedSetups()
	lit := func() bool { c.afterInput(); return eng.lastLEDs()&ledSaveRec != 0 }
	if rowText(c, "file.setup6") != "empty" || lit() {
		t.Fatalf("before: row %q lamp %v", rowText(c, "file.setup6"), lit())
	}
	if err := shared.SaveSetup(6); err != nil || !m.saved[6] {
		t.Fatalf("save: %v %v", err, m.saved)
	}
	if row := rowText(c, "file.setup6"); row == "empty" || !lit() {
		t.Fatalf("after a shared save: row %q lamp %v", row, lit())
	}
	if sl := shared.Slots(); len(sl) != 20 || !sl[5].Used || sl[5].Summary != rowText(c, "file.setup6") || sl[6].Used {
		t.Fatalf("slots: %+v", sl)
	}
	press(t, c, "f4") // another workspace
	if lit() {
		t.Fatal("the lamp stayed lit after a change")
	}
	if err := shared.RecallSetup(6); err != nil || len(m.rec) != 1 || !lit() {
		t.Fatalf("recall: %v %v lamp %v", err, m.rec, lit())
	}
	if err := shared.RecallSetup(7); err == nil {
		t.Fatal("an empty slot recalled")
	}
	if err := shared.SaveSetupFile("x"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("named setups on a store without them: %v", err)
	}
}
