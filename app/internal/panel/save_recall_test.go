// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/settings"
)

// The Save/Recall key opens the FILE tab: a row per numbered setup with what
// it holds, saving and recalling from the F-keys beside it, and the lamp says
// the settings are the ones last saved or recalled.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-212
func TestSaveRecallKeyFileTab(t *testing.T) {
	c, eng, _ := newC(t)
	m := &memSlots{saved: map[int]bool{}}
	c.SetSetupMemory(m)
	press(t, c, "f2", "saverecall")
	st := c.PanelStatus()
	if st.Sheet.Tab != "file" || st.Sheet.Row.ID != "file.setup1" || st.FKeys[0].Label != "Save" || st.FKeys[1].Value != "empty" {
		t.Fatalf("Save/Recall key: %+v keys %+v", st.Sheet, st.FKeys)
	}
	turn(c, "adjust", 1) // slot 2
	pickKey(t, c, "Recall")
	if !strings.Contains(c.View().Notice, "setup 2 is empty") || len(m.rec) != 0 {
		t.Fatalf("recall of an empty slot: %q %v", c.View().Notice, m.rec)
	}
	pickKey(t, c, "Save")
	if !m.saved[2] || c.View().Notice != "Setup saved to slot 2" || eng.lastLEDs()&ledSaveRec == 0 {
		t.Fatalf("save: %v %q lamps %#04x", m.saved, c.View().Notice, eng.lastLEDs())
	}
	if st := c.PanelStatus(); st.FKeys[1].Value != "into BUS" || !strings.HasPrefix(rowText(c, "file.setup2"), "BUS") {
		t.Fatalf("slot 2 after save: keys %+v row %q", st.FKeys, rowText(c, "file.setup2"))
	}
	c.SheetSet("trig.mode", nil, "Normal")
	if c.afterInput(); eng.lastLEDs()&ledSaveRec != 0 {
		t.Fatal("the lamp stayed lit after a change")
	}
	pickKey(t, c, "Recall")
	if len(m.rec) != 1 || m.rec[0] != 2 || eng.lastLEDs()&ledSaveRec == 0 {
		t.Fatalf("recall: %v lamps %#04x", m.rec, eng.lastLEDs())
	}
	press(t, c, "saverecall")
	mustState(t, c, "BUS", "workspace")
}

// PRINT saves through the hardcopy hook and shows the file name; without a
// hook it says so, not claim-and-ignore.
// TRLC-LINKS: REQ-SDS-136
func TestPrintKeyShowsResult(t *testing.T) {
	c, _, _ := newC(t)
	press(t, c, "print")
	if n := c.View().Notice; n != "PRINT: unavailable" {
		t.Fatalf("no hook: notice %q", n)
	}
	done := make(chan struct{})
	c.SetHardcopy(func() (string, error) { defer close(done); return "SDS0007.png", nil })
	press(t, c, "print")
	<-done
	deadline := time.Now().Add(time.Second)
	for c.View().Notice != "SAVED SDS0007.png" {
		if time.Now().After(deadline) {
			t.Fatalf("notice %q", c.View().Notice)
		}
		time.Sleep(time.Millisecond)
	}
}

// A slot's row summarises what it holds: the workspace, the timebase, the
// trigger, the channels, the decoder and the view.
// TRLC-LINKS: REQ-SDS-136
func TestSetupSummary(t *testing.T) {
	st := settings.Settings{TdivS: 500e-6, Trigger: settings.Trigger{Rising: true, Source: 1, LevelCode: 31437},
		Ch:     [2]settings.Channel{{VdivV: 1, Probe: 10}, {VdivV: 0.5, Probe: 1}},
		Decode: settings.Decode{Proto: 2}, ViewMode: 2,
		UI: settings.UI{Set: true, Workspace: 2}}
	if got := setupSummary(st); got != "SPECTRUM  500us/div  C2^ 0V  C1 10V C2 500mV  UART  FFT" {
		t.Fatalf("summary %q", got)
	}
}
