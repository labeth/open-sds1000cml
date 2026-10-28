// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/settings"
)

// The task-workspace state machine (ADR-TASK-WORKSPACES): F1..F5 switch
// workspaces with no menu open and pick choices with one open, the active
// workspace's key again shows its next view, Menu On/Off and the printed
// keys open and close the sheets, the knobs are each workspace's tools,
// notices run on the controller's clock and the lamps say what is in use.

// TRLC-LINKS: REQ-SDS-211
func TestWorkspaceKeysSwitchAndCycleViews(t *testing.T) {
	c, _, _ := newC(t)
	k := withClock(c)
	if st := mustState(t, c, "SCOPE", "workspace"); st.View != "Y-T" || st.FKeys[0].Label != "SCOP" || !st.FKeys[0].Sel {
		t.Fatalf("power-on: %+v", st)
	}
	again := func(key, ws string, views ...string) {
		t.Helper()
		press(t, c, key)
		for i, want := range views {
			st := mustState(t, c, ws, "workspace")
			if st.View != want {
				t.Fatalf("%s view %d: %q, want %q", ws, i, st.View, want)
			}
			if i := slices.IndexFunc(st.FKeys[:], func(k KeyStatus) bool { return k.Sel }); i < 0 || wsview.Tags[i] != st.FKeys[i].Label || wsview.Names[i] != ws {
				t.Fatalf("%s: the rail marks key %d", ws, i)
			}
			if i < len(views)-1 {
				press(t, c, key)
			}
		}
		k.advance(2 * time.Second)
	}
	again("f1", "SCOPE", "X-Y", "Y-T") // F1 on SCOPE: its next view
	again("f2", "BUS", "Packets", "Eye diagram", "Stream", "Visual", "Packets")
	again("f3", "SPECTRUM", "FFT", "Spectrogram", "Bode", "Sound", "FFT")
	again("f4", "MEASURE", "Statistics", "All values", "Statistics")
	// The workspaces remember their views.
	press(t, c, "f3", "f3") // SPECTRUM: spectrogram
	press(t, c, "f1")
	if st := mustState(t, c, "SCOPE", "workspace"); st.View != "Y-T" || c.viewMode != viewYT {
		t.Fatalf("SCOPE took SPECTRUM's view: %s %d", st.View, c.viewMode)
	}
	press(t, c, "f3")
	if st := mustState(t, c, "SPECTRUM", "workspace"); st.View != "Spectrogram" || c.viewMode != viewSpectrogram {
		t.Fatalf("SPECTRUM lost its view: %s", st.View)
	}
}

// Every workspace's sheet opens with its own tabs first, on its first tab
// the first time and where it was left after; F-keys pick the highlighted
// row's choices while it is open and switch workspaces again once closed.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func TestMenuKeyOpensTheWorkspaceSheet(t *testing.T) {
	c, _, _ := newC(t)
	for i, key := range []string{"f1", "f2", "f3", "f4", "f5"} {
		press(t, c, key, "menu")
		st := mustState(t, c, wsview.Names[i], "sheet")
		own := wsDefs[i].tabs
		for j, id := range own {
			if st.Sheet.Tabs[j] != sheetTabs[tabIndex(id)].name {
				t.Fatalf("%s sheet tabs %v: own tabs %v first", wsview.Names[i], st.Sheet.Tabs, own)
			}
		}
		if len(st.Sheet.Tabs) != numTabs || st.Sheet.Tab != own[0] {
			t.Fatalf("%s sheet on %s with %d tabs", wsview.Names[i], st.Sheet.Tab, len(st.Sheet.Tabs))
		}
		if st.FKeys[0].Label == wsview.Tags[0] {
			t.Fatal("the F-keys still switch workspaces in the sheet")
		}
		turn(c, "tdiv", 2)
		left := c.PanelStatus().Sheet.Tab
		press(t, c, "menu")
		if st := mustState(t, c, wsview.Names[i], "workspace"); st.FKeys[i].Label != wsview.Tags[i] {
			t.Fatalf("closed sheet: F-keys %v", keyLabels(c))
		}
		press(t, c, "menu")
		if got := c.PanelStatus().Sheet.Tab; got != left {
			t.Fatalf("%s sheet reopened on %s, left on %s", wsview.Names[i], got, left)
		}
		press(t, c, "menu")
	}
}

// The printed keys open their tab (Math and Ref on their rows, Help on the
// guide) and close it again; 1 and 2 switch an off channel on.
// TRLC-LINKS: REQ-SDS-212
func TestPrintedKeysOpenAndCloseTheirTabs(t *testing.T) {
	c, _, _ := newC(t)
	for key, ti := range keyTabs {
		press(t, c, key)
		st := mustState(t, c, "SCOPE", "sheet")
		if st.Sheet.Tab != sheetTabs[ti].id {
			t.Fatalf("%s opened %s, want %s", key, st.Sheet.Tab, sheetTabs[ti].id)
		}
		if id := keyRows[key]; id != "" && st.Sheet.Row.ID != id {
			t.Fatalf("%s highlighted %s, want %s", key, st.Sheet.Row.ID, id)
		}
		press(t, c, key)
		mustState(t, c, "SCOPE", "workspace")
	}
	c.SetTraceOn(1, false)
	press(t, c, "ch2")
	if st := mustState(t, c, "SCOPE", "sheet"); st.Sheet.Tab != "ch2" || !c.TraceOn(1) {
		t.Fatalf("2 on an off channel: tab %s on %v", st.Sheet.Tab, c.TraceOn(1))
	}
	press(t, c, "ch2")
	mustState(t, c, "SCOPE", "workspace")
	press(t, c, "trigmenu", "acquire") // another key moves to its tab
	if st := mustState(t, c, "SCOPE", "sheet"); st.Sheet.Tab != "acq" {
		t.Fatalf("Acquire over TRIG: %s", st.Sheet.Tab)
	}
}

// In a sheet S/DIV turns the tab, Adjust moves the row (wrapping), Position
// changes the row and its push resets it; the Adjust push edits a row (Adjust
// then changes it) and never runs an action, which only its F-key does.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-213
func TestSheetKnobs(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.TrigRising = true
	press(t, c, "menu")
	n := len(sheetTabs[tabTrig].rows)
	turn(c, "adjust", -1)
	if r := c.PanelStatus().Sheet.Row.ID; r != sheetTabs[tabTrig].rows[n-1].id {
		t.Fatalf("Adjust back from the first row: %s", r)
	}
	turn(c, "adjust", 1)
	if r := c.PanelStatus().Sheet.Row.ID; r != "trig.edge" {
		t.Fatalf("Adjust on from the last row: %s", r)
	}
	turn(c, "horizpos", 2) // Source/slope: C1 Rise -> C1 Fall -> C2 Rise
	if st := eng.Snapshot(); st.TrigSource != 1 || !st.TrigRising {
		t.Fatalf("Position on Source/slope: src %d rising %v", st.TrigSource, st.TrigRising)
	}
	press(t, c, "horizpospush")
	if st := eng.Snapshot(); st.TrigSource != 0 || !st.TrigRising {
		t.Fatalf("Position push: src %d rising %v", st.TrigSource, st.TrigRising)
	}
	goRow(t, c, "trig.holdoff")
	press(t, c, "adjustpush")
	if !c.PanelStatus().Sheet.Edit {
		t.Fatal("Adjust push did not edit the row")
	}
	turn(c, "adjust", 2) // editing: Adjust changes the value, not the row (Off, 100 us, 200 us)
	if r := c.PanelStatus().Sheet.Row.ID; r != "trig.holdoff" || eng.Snapshot().HoldoffS != 200e-6 {
		t.Fatalf("edit turn: row %s holdoff %g", r, eng.Snapshot().HoldoffS)
	}
	press(t, c, "adjustpush")
	goRow(t, c, "trig.force")
	before := len(eng.calls)
	press(t, c, "adjustpush")
	turn(c, "horizpos", 1)
	press(t, c, "horizpospush")
	if c.PanelStatus().Sheet.Edit || len(eng.calls) != before {
		t.Fatalf("a knob ran an action: %v", eng.calls[before:])
	}
	turn(c, "tdiv", 1)
	if got := c.PanelStatus().Sheet.Tab; got != "ch1" {
		t.Fatalf("S/DIV: %s", got)
	}
	turn(c, "tdiv", -2)
	if got := c.PanelStatus().Sheet.Tab; got != "sys" {
		t.Fatalf("S/DIV back past the first tab: %s", got)
	}
}

// Each workspace gives Adjust and Position (and their pushes) its tool, and
// the tool line names them; V/DIV, the vertical Position, Level and S/DIV
// keep their printed jobs in every workspace.
// TRLC-LINKS: REQ-SDS-213
func TestKnobToolsPerWorkspace(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.show(toneFrame(""))
	tool := func() string { return toolText(c) }
	// SCOPE: Adjust tunes the last value set in a sheet, Position the
	// trigger position.
	r.c.SheetSet("trig.level", nil, "0.5V")
	press(t, c, "trigmenu")
	goRow(t, c, "trig.level")
	pickKey(t, c, "Up")
	press(t, c, "trigmenu")
	lvl := r.eng.Snapshot().TrigCode
	turn(c, "adjust", 3)
	if r.eng.Snapshot().TrigCode >= lvl || !strings.Contains(tool(), "Adjust:Level") {
		t.Fatalf("SCOPE Adjust: code %d -> %d, tool %q", lvl, r.eng.Snapshot().TrigCode, tool())
	}
	turn(c, "horizpos", 5)
	if p := r.eng.Snapshot().TrigPosFrac; p < 0.549 || p > 0.551 {
		t.Fatalf("SCOPE Position: trigger position %g", p)
	}
	press(t, c, "horizpospush")
	if p := r.eng.Snapshot().TrigPosFrac; p != 0.5 {
		t.Fatalf("SCOPE Position push: %g", p)
	}

	// MEASURE: Adjust and (pushed to its second job) Position the cursors;
	// the MEAS sheet's Statistics row zeroes the statistics.
	press(t, c, "f4")
	for i := 0; i < 3; i++ {
		r.show(toneFrame(""))
	}
	if !strings.Contains(tool(), "Adjust:cursor A") || !strings.Contains(tool(), "Position:trigger position push:cursor B") {
		t.Fatalf("MEASURE tool %q", tool())
	}
	press(t, c, "adjustpush") // cursors on (time)
	a0, b0 := c.snap().curX[0], c.snap().curX[1]
	turn(c, "adjust", 4)
	press(t, c, "horizpospush")
	if !strings.Contains(tool(), "Position:cursor B push:trigger position") {
		t.Fatalf("MEASURE pushed tool %q", tool())
	}
	turn(c, "horizpos", -4)
	if !c.curOn || c.snap().curX[0] <= a0 || c.snap().curX[1] >= b0 {
		t.Fatalf("MEASURE cursors: on %v A %g->%g B %g->%g", c.curOn, a0, c.snap().curX[0], b0, c.snap().curX[1])
	}
	if v := c.View(); v.Meas.Stats[0].N != "3" {
		t.Fatalf("statistics N %s", v.Meas.Stats[0].N)
	}
	if res := c.SheetAction("meas.stats", "zero"); !res.OK {
		t.Fatalf("Statistics row: %+v", res)
	}
	if v := c.View(); v.Meas.Stats[0].N != "0" {
		t.Fatalf("Statistics row left N %s", v.Meas.Stats[0].N)
	}

	// SPECTRUM: Adjust peak to peak, Position (pushed to fine marker) bin by
	// bin, the VIEW sheet's Marker row back to the largest peak.
	press(t, c, "f3")
	r.show(toneFrame(""))
	v := c.View()
	if !v.Spec.HasMark || !strings.Contains(v.Spec.Marker, "largest peak") {
		t.Fatalf("SPECTRUM marker %+v", v.Spec)
	}
	f0 := v.Spec.MarkHz
	turn(c, "adjust", 1)
	if v := c.View(); v.Spec.MarkHz <= f0 || v.Spec.Rows[1].Sel != true {
		t.Fatalf("Adjust: marker %g (from %g)", v.Spec.MarkHz, f0)
	}
	f1 := c.View().Spec.MarkHz
	press(t, c, "horizpospush")
	turn(c, "horizpos", 2)
	if v := c.View(); v.Spec.MarkHz-f1 < c.ana.spec.BinHz*1.5 || v.Spec.MarkHz-f1 > c.ana.spec.BinHz*2.5 {
		t.Fatalf("Position: marker %g (from %g, bin %g)", v.Spec.MarkHz, f1, c.ana.spec.BinHz)
	}
	c.SheetAction("view.marker", "home")
	if v := c.View(); v.Spec.MarkHz != f0 {
		t.Fatalf("Marker row: marker %g, want the largest peak %g", v.Spec.MarkHz, f0)
	}

	// COMPARE: Adjust chooses a row, its push runs the row's action.
	press(t, c, "f5")
	turn(c, "adjust", 4) // MATH
	press(t, c, "adjustpush")
	if c.mathMode == 0 {
		t.Fatal("COMPARE: the MATH row's push did not change the math")
	}

	// The printed jobs stay: V/DIV, Level, the vertical Position, S/DIV.
	for _, ws := range []string{"f1", "f2", "f3", "f4", "f5"} {
		press(t, c, ws)
		if c.ui.ws == wsview.Bus && c.ui.busEye {
			press(t, c, "f2")
		}
		idx := r.fe.idx[0]
		turn(c, "ch1vdiv", 1)
		if r.fe.idx[0] != idx-1 {
			t.Fatalf("%s: V/DIV clockwise %d -> %d", ws, idx, r.fe.idx[0])
		}
		turn(c, "ch1vdiv", -1)
		code := r.eng.Snapshot().TrigCode
		turn(c, "triglevel", 1)
		if r.eng.Snapshot().TrigCode >= code {
			t.Fatalf("%s: Level clockwise %d -> %d", ws, code, r.eng.Snapshot().TrigCode)
		}
		td := r.eng.Snapshot().TdivS
		turn(c, "tdiv", 1)
		if got := r.eng.Snapshot().TdivS; got >= td {
			t.Fatalf("%s: S/DIV clockwise %g -> %g", ws, td, got)
		}
		turn(c, "tdiv", -1)
		off := r.fe.offReqV[1]
		turn(c, "ch2pos", 1)
		if r.fe.offReqV[1] <= off {
			t.Fatalf("%s: CH2 Position %g -> %g", ws, off, r.fe.offReqV[1])
		}
		r.clk.advance(2 * time.Second)
	}
}

// The rail label, the notices, the REMOTE note and an F-key's flash run on
// the controller's clock, not on sleeps.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func TestNoticesFollowTheClock(t *testing.T) {
	c, _, _ := newC(t)
	k := withClock(c)
	press(t, c, "f4")
	v := c.View()
	if v.RailNote == nil || v.RailNote.Head != "F4 MEASURE" || !strings.Contains(v.RailNote.Text, "statistics / all values") {
		t.Fatalf("rail label %+v", v.RailNote)
	}
	k.advance(railNoteFor - time.Millisecond)
	if c.View().RailNote == nil {
		t.Fatal("the rail label went early")
	}
	k.advance(2 * time.Millisecond)
	if c.View().RailNote != nil {
		t.Fatal("the rail label stayed")
	}
	press(t, c, "ch1vdivpush")
	if !strings.Contains(c.View().Notice, "no fine V/div") {
		t.Fatalf("Variable notice %q", c.View().Notice)
	}
	k.advance(1801 * time.Millisecond)
	if n := c.View().Notice; n != "" {
		t.Fatalf("notice stayed: %q", n)
	}
	c.SheetSet("trig.level", nil, "100mV")
	if d, _ := c.Snapshot(); !strings.HasPrefix(d.Remote, "REMOTE  TRIG level") {
		t.Fatalf("remote note %q", d.Remote)
	}
	k.advance(remoteNoteFor + time.Millisecond)
	if d, _ := c.Snapshot(); d.Remote != "" {
		t.Fatalf("remote note stayed: %q", d.Remote)
	}
	press(t, c, "menu")
	goRow(t, c, "meas.clear")
	pickKey(t, c, "Clear all")
	if !c.View().Keys.Items[0].Sel {
		t.Fatal("the F-key pressed is not lit")
	}
	k.advance(400 * time.Millisecond)
	if c.View().Keys.Items[0].Sel {
		t.Fatal("the flash stayed")
	}
}

// sinceEng is the fake engine with the publish clock (the Run/Stop lamp's
// waiting state).
type sinceEng struct {
	*fakeEng
	since time.Duration
}

// SincePublish is the time since the engine last published a frame.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func (e *sinceEng) SincePublish() time.Duration { return e.since }

// Each lamp means its function is in use; INTENSITY, which no firmware has
// lit, stays dark (spec 08 §8.2, spec 02 §7.5).
// TRLC-LINKS: REQ-SDS-135, REQ-SDS-211
func TestLampsMeanFunctionInUse(t *testing.T) {
	c, base, _ := newC(t)
	eng := &sinceEng{fakeEng: base}
	c.eng = eng
	m := &memSlots{saved: map[int]bool{}}
	c.SetSetupMemory(m)
	lamp := func(bit uint16) bool { c.afterInput(); return eng.lastLEDs()&bit != 0 }
	check := func(what string, bit uint16, want bool) {
		t.Helper()
		if got := lamp(bit); got != want {
			t.Fatalf("%s: lamp %#04x %v, want %v (word %#04x)", what, bit, got, want, eng.lastLEDs())
		}
	}
	check("power-on CH1", ledCH1, true)
	check("power-on CH2", ledCH2, true)
	check("power-on MEASURE", ledMeasure, false)
	check("power-on ACQUIRE", ledAcquire, false)
	check("power-on DISPLAY", ledDisplay, false)
	check("power-on UTILITY", ledUtility, false)
	check("power-on SAVE/RECALL", ledSaveRec, false)
	check("power-on MATH", ledMath, false)
	check("power-on REF", ledRef, false)
	check("power-on CURSORS", ledCursors, false)
	check("running", ledRun, true)
	check("running", ledStop, false)

	c.SetTraceOn(1, false)
	check("C2 off", ledCH2, false)
	c.SetTraceOn(1, true)
	c.SheetSet("meas.freq", nil, "C1")
	c.SheetSet("meas.readouts", nil, "On")
	check("readouts on", ledMeasure, true)
	c.SheetSet("acq.mode", nil, "Average")
	check("averaging", ledAcquire, true)
	c.SheetSet("acq.mode", nil, "Normal")
	check("normal", ledAcquire, false)
	c.SheetSet("view.view", nil, "FFT")
	check("FFT", ledDisplay, true)
	c.SheetSet("view.view", nil, "Y-T")
	c.SheetSet("view.persist", nil, "On")
	check("persistence", ledDisplay, true)
	c.SheetSet("view.persist", nil, "Off")
	check("Y-T", ledDisplay, false)
	c.SheetSet("bus.protocol", nil, "UART")
	check("decoding", ledUtility, true)
	c.SheetSet("bus.protocol", nil, "Off")
	check("decode off", ledUtility, false)

	c.SheetAction("file.setup3", "save")
	check("just saved", ledSaveRec, true)
	c.SheetSet("trig.mode", nil, "Normal")
	check("changed since the save", ledSaveRec, false)
	c.SheetAction("file.setup3", "recall")
	check("just recalled", ledSaveRec, true)
	if len(m.rec) != 1 || m.rec[0] != 3 {
		t.Fatalf("recall did not reach the store: %v", m.rec)
	}

	check("Normal, a frame just in", ledStop, false)
	eng.since = time.Hour
	check("Normal, waiting", ledRun|ledStop, true)
	if eng.lastLEDs()&(ledRun|ledStop) != ledRun|ledStop {
		t.Fatal("waiting is not amber (both elements)")
	}
	press(t, c, "single")
	check("single armed", ledSingle, true)
	press(t, c, "runstop")
	check("stopped", ledRun, false)
	check("stopped", ledStop, true)
	check("stopped", ledSingle, false)

	c.SheetSet("cmp.math", nil, "C1+C2")
	check("math on", ledMath, true)
	c.SheetSet("cmp.math", nil, "Off")
	check("math off", ledMath, false)
	c.SetCursorsOn(true)
	check("cursors on", ledCursors, true)
	c.SetCursorsOn(false)
	check("cursors off", ledCursors, false)
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(smallFrame()) })
	if res := c.SheetAction("cmp.store_a", ""); !res.OK {
		t.Fatalf("store REF A: %s", res.Error)
	}
	check("REF A stored and shown", ledRef, true)
	c.SheetSet("cmp.show_a", nil, "Off")
	check("REF A hidden", ledRef, false)
	c.SheetSet("cmp.show_a", nil, "On")
	check("REF A shown again", ledRef, true)
	c.SheetAction("cmp.clear_refs", "")
	check("refs cleared", ledRef, false)
	check("INTENSITY", ledIntens, false)
}

// V/DIV push (Variable): the front end has only its calibrated detents.
// TRLC-LINKS: REQ-SDS-213
func TestVariablePushSaysSo(t *testing.T) {
	c, eng, fe := newC(t)
	for ch, key := range []string{"ch1vdivpush", "ch2vdivpush"} {
		press(t, c, key)
		if n := c.View().Notice; n != fmt.Sprintf("CH%d Variable: no fine V/div on this instrument (12 calibrated steps)", ch+1) {
			t.Fatalf("%s notice %q", key, n)
		}
	}
	if len(eng.calls) != 0 || len(fe.calls) != 0 {
		t.Fatalf("Variable changed something: %v %v", eng.calls, fe.calls)
	}
}

// Leaving BUS keeps decoding on; entering BUS with decode off detects the
// bus and says so.
// TRLC-LINKS: REQ-SDS-214
func TestBusEntryDetectsAndStaysDecoding(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := uartFrame([]string{"Hello\r\n"}, 115200, 200e-6, 200e-9, 20000)
	r.show(uart)
	press(t, c, "f2")
	if d, _ := c.Snapshot(); d.DecProto != 2 || !strings.HasPrefix(c.View().Notice, "AUTO: UART") {
		t.Fatalf("BUS entry: proto %d notice %q", d.DecProto, c.View().Notice)
	}
	press(t, c, "f1")
	if d, _ := c.Snapshot(); d.DecProto != 2 {
		t.Fatalf("leaving BUS turned decode off: %d", d.DecProto)
	}
}

// memSlots is a setup store that records saves and recalls.
type memSlots struct {
	saved map[int]bool
	rec   []int
}

// TRLC-LINKS: REQ-SDS-214
func (m *memSlots) SaveSetup(n int) error { m.saved[n] = true; return nil }

// TRLC-LINKS: REQ-SDS-214
func (m *memSlots) PeekSetup(n int) (settings.Settings, bool) {
	return settings.Settings{UI: settings.UI{Set: true, Workspace: 1}}, m.saved[n]
}

// TRLC-LINKS: REQ-SDS-214
func (m *memSlots) RecallSetup(n int) error {
	if !m.saved[n] {
		return fmt.Errorf("empty")
	}
	m.rec = append(m.rec, n)
	return nil
}
