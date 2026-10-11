// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
	"time"
)

// The phone remote's workspace keys: RemoteWorkspace and RemoteAgain switch
// the workspace and run its "again" whatever the LCD shows, leaving an open
// sheet (its tab, row, edit and sub-mode) exactly as it was, where F1..F5
// would pick that sheet's choices instead.

// A switch with a sheet open on the LCD: the workspace changes behind it,
// the sheet stays on its tab and row with its F-keys and edit, the LCD
// confirms with a REMOTE line, and what again does next is reported.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func TestRemoteWorkspaceLeavesTheLCDSheet(t *testing.T) {
	c, _, _ := newC(t)
	withClock(c)
	press(t, c, "trigmenu")
	goRow(t, c, "trig.holdoff")
	press(t, c, "adjustpush") // editing the row on the LCD
	before := c.PanelStatus()
	if !before.Sheet.Open || !before.Sheet.Edit {
		t.Fatalf("the LCD's sheet: %+v", before.Sheet)
	}
	res := c.RemoteWorkspace("bus")
	if !res.OK || res.Value != "bus" || res.Text != "BUS" {
		t.Fatalf("RemoteWorkspace(bus): %+v", res)
	}
	st := c.PanelStatus()
	if st.Workspace != "BUS" || st.WorkspaceID != "bus" {
		t.Fatalf("workspace %s", st.Workspace)
	}
	if st.Mode != "sheet" || !st.Sheet.Open || st.Sheet.Tab != "trig" || st.Sheet.Row.ID != "trig.holdoff" || !st.Sheet.Edit {
		t.Fatalf("the LCD's sheet moved: %+v", st.Sheet)
	}
	if st.FKeys != before.FKeys {
		t.Fatalf("the F-keys changed under the sheet: %+v -> %+v", before.FKeys, st.FKeys)
	}
	if st.Remote != "REMOTE  workspace BUS" {
		t.Fatalf("remote line %q", st.Remote)
	}
	if st.Again != "" || st.AgainNext != "Eye diagram" {
		t.Fatalf("again with a sheet open: %q / next %q", st.Again, st.AgainNext)
	}
	// the workspace shown already: nothing changes (its again is RemoteAgain)
	if res := c.RemoteWorkspace("BUS"); !res.OK || c.PanelStatus().View != "Packets" {
		t.Fatalf("the active workspace again: %+v view %s", res, c.PanelStatus().View)
	}
	// names, tags and ids, any case
	for _, x := range [][2]string{{"SPEC", "SPECTRUM"}, {"measure", "MEASURE"}, {" compare ", "COMPARE"}, {"Scope", "SCOPE"}} {
		if res := c.RemoteWorkspace(x[0]); !res.OK || c.PanelStatus().Workspace != x[1] {
			t.Fatalf("RemoteWorkspace(%q): %+v in %s", x[0], res, c.PanelStatus().Workspace)
		}
	}
	if res := c.RemoteWorkspace("F3"); res.OK || !strings.Contains(res.Error, `no workspace "F3"`) {
		t.Fatalf("an F-key is not a workspace: %+v", res)
	}
	if st := c.PanelStatus(); st.Sheet.Tab != "trig" || st.Sheet.Row.ID != "trig.holdoff" || !st.Sheet.Edit {
		t.Fatalf("the LCD's sheet moved: %+v", st.Sheet)
	}
	// closed on the LCD, the sheet opens again on its tab: nothing was forgotten
	press(t, c, "trigmenu")
	if st := mustState(t, c, "SCOPE", "workspace"); st.Again != "F1 again: X-Y" || st.AgainNext != "X-Y" {
		t.Fatalf("again in workspace mode: %q / %q", st.Again, st.AgainNext)
	}
}

// Again from the remote with a sheet open: each workspace's next view (a
// master stacked and cancelled in COMPARE), the sheet untouched, the REMOTE
// line naming the view.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-218, REQ-SDS-220
func TestRemoteAgainLeavesTheLCDSheet(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	press(t, c, "utility")
	goRow(t, c, "bus.match")
	pickKey(t, c, "Edit bytes") // the match editor open on the LCD
	if st := c.PanelStatus(); st.Mode != "editor" {
		t.Fatalf("mode %s", st.Mode)
	}
	steps := []struct{ ws, view, next string }{
		{"scope", "X-Y", "Y-T"}, {"bus", "Eye diagram", "Packets"}, {"spectrum", "Spectrogram", "Bode"},
		{"measure", "All values", "Statistics"},
	}
	for _, s := range steps {
		if res := c.RemoteWorkspace(s.ws); !res.OK {
			t.Fatalf("%s: %+v", s.ws, res)
		}
		res := c.RemoteAgain()
		st := c.PanelStatus()
		if !res.OK || res.Text != s.view || st.View != s.view || st.AgainNext != s.next {
			t.Fatalf("%s again: %+v, view %s, next %s", s.ws, res, st.View, st.AgainNext)
		}
		if !strings.HasPrefix(st.Remote, "REMOTE  "+strings.ToUpper(s.ws)+" "+s.view) {
			t.Fatalf("%s again: remote line %q", s.ws, st.Remote)
		}
		if st.Mode != "editor" || st.Sheet.Tab != "bus" || st.Sheet.Row.ID != "bus.match" {
			t.Fatalf("%s again moved the LCD: %s %+v", s.ws, st.Mode, st.Sheet)
		}
	}
	// COMPARE again stacks a master, again cancels it; the acquisition comes back
	c.RemoteWorkspace("compare")
	if st := c.PanelStatus(); st.AgainNext != "stack master" {
		t.Fatalf("COMPARE next: %q", st.AgainNext)
	}
	if res := c.RemoteAgain(); !res.OK || res.Text != "Stacking" || eng.Snapshot().AcqMode != 1 {
		t.Fatalf("COMPARE again: %+v (acq %d)", res, eng.Snapshot().AcqMode)
	}
	if st := c.PanelStatus(); st.AgainNext != "cancel stack" || st.Mode != "editor" {
		t.Fatalf("stacking: next %q mode %s", st.AgainNext, st.Mode)
	}
	if res := c.RemoteAgain(); !res.OK || res.Text != "Compare" || eng.Snapshot().AcqMode != 0 {
		t.Fatalf("COMPARE again (cancel): %+v (acq %d)", res, eng.Snapshot().AcqMode)
	}
	// a master the scope refuses says why
	press(t, c, "menu") // the editor closes on the LCD
	c.SheetSet("ch1.on", nil, "Off")
	if res := c.RemoteAgain(); res.OK || !strings.Contains(res.Error, "No master: the trigger source CH1 is off") {
		t.Fatalf("a refused master: %+v", res)
	}
}

// Leaving BUS from the remote closes the BUS pane's own byte picker (it
// belongs to BUS; F-keys would otherwise still pick bytes in SCOPE).
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-220
func TestRemoteWorkspaceClosesTheBusPicker(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.show(uartFrame([]string{"Hi!\r\n"}, 460800, 100e-6, 100e-9, 20000))
	press(t, c, "f2")
	r.show(uartFrame([]string{"Hi!\r\n"}, 460800, 100e-6, 100e-9, 20000))
	press(t, c, "adjustpush")
	if st := c.PanelStatus(); st.Mode != "picker" {
		t.Fatalf("the BUS picker: %s", st.Mode)
	}
	c.RemoteWorkspace("scope")
	st := mustState(t, c, "SCOPE", "workspace")
	if st.Picker.Open || st.FKeys[2].Label != "SPEC" {
		t.Fatalf("the picker stayed: %+v keys %+v", st.Picker, st.FKeys)
	}
}

// The remote's workspace keys run on the panel loop like its other changes.
// TRLC-LINKS: REQ-SDS-220
func TestRemoteWorkspaceOnThePanelLoop(t *testing.T) {
	c, _, _ := newC(t)
	stop := make(chan struct{})
	defer close(stop)
	go c.Run(stop)
	waitLoop(t, c)
	if !c.InjectButton("trigmenu") {
		t.Fatal("Trig Menu rejected")
	}
	for i := 0; i < 100 && c.PanelStatus().Mode != "sheet"; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if res := c.RemoteWorkspace("measure"); !res.OK {
		t.Fatalf("on the loop: %+v", res)
	}
	if res := c.RemoteAgain(); !res.OK || res.Text != "All values" {
		t.Fatalf("again on the loop: %+v", res)
	}
	if st := c.PanelStatus(); st.Workspace != "MEASURE" || st.Mode != "sheet" || st.Sheet.Tab != "trig" {
		t.Fatalf("after: %s %s %+v", st.Workspace, st.Mode, st.Sheet)
	}
}

// A number row's presets: listed in the model, run by label through the
// action call (as its F-key on the sheet), with the REMOTE line; a preset
// it does not have is named with the ones it has.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func TestRemotePresets(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	rows := map[string]RowModel{}
	for _, tab := range c.Sheets().Tabs {
		for _, r := range tab.Rows {
			rows[r.ID] = r
		}
	}
	if p := rows["trig.holdoff"].Presets; strings.Join(p, "|") != "Off|100 us|1 ms" {
		t.Fatalf("holdoff presets %q", p)
	}
	if rows["time.tdiv"].Presets != nil || rows["trig.mode"].Presets != nil {
		t.Fatal("presets on a row without any")
	}
	res := c.SheetAction("trig.holdoff", "1 ms")
	if !res.OK || res.Text != "1ms" || eng.Snapshot().HoldoffS != 1e-3 {
		t.Fatalf("holdoff 1 ms: %+v (%g)", res, eng.Snapshot().HoldoffS)
	}
	if st := c.PanelStatus(); st.Remote != "REMOTE  TRIG holdoff 1ms" || st.Mode != "workspace" {
		t.Fatalf("after a preset: %q %s", st.Remote, st.Mode)
	}
	if res := c.SheetAction("trig.holdoff", "OFF"); !res.OK || eng.Snapshot().HoldoffS != 0 {
		t.Fatalf("holdoff Off (any case): %+v", res)
	}
	if res := c.SheetAction("trig.holdoff", "2 ms"); res.OK || !strings.Contains(res.Error, `Holdoff has no preset "2 ms" (Off, 100 us, 1 ms)`) {
		t.Fatalf("no such preset: %+v", res)
	}
	if res := c.SheetAction("time.tdiv", "1 ms"); res.OK || !strings.Contains(res.Error, "is a setting, not an action") {
		t.Fatalf("a number row without presets: %+v", res)
	}
	c.SheetSet("ch1.offset", nil, "1V")
	if res := c.SheetAction("ch1.offset", "0 V"); !res.OK || res.Text != "0V" {
		t.Fatalf("offset 0 V: %+v", res)
	}
}

// The model carries each row's section header and, while a row is not in
// use, what would put it in use.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func TestRowModelSectionAndWhy(t *testing.T) {
	c, _, _ := newC(t)
	rows := map[string]RowModel{}
	for _, tab := range c.Sheets().Tabs {
		for _, r := range tab.Rows {
			rows[r.ID] = r
		}
	}
	pw := rows["trig.pulse_when"]
	if pw.Section != "PULSE  (used when Type = Pulse)" || pw.Available || pw.Why != "Type = Pulse" {
		t.Fatalf("pulse_when: section %q available %v why %q", pw.Section, pw.Available, pw.Why)
	}
	if pl := rows["trig.pulse_level"]; pl.Section != "" || pl.Why != "Type = Pulse" {
		t.Fatalf("pulse_level: section %q why %q", pl.Section, pl.Why)
	}
	if lv := rows["trig.level"]; !lv.Available || lv.Why != "" {
		t.Fatalf("an available row names no reason: %+v", lv)
	}
	c.SheetSet("trig.type", nil, "Pulse")
	for _, tab := range c.Sheets().Tabs {
		for _, r := range tab.Rows {
			if r.ID == "trig.pulse_when" && (!r.Available || r.Why != "") {
				t.Fatalf("in use: %+v", r)
			}
		}
	}
}
