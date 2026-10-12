// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// The phone remote's surface: the panel snapshot labels everything as the
// LCD draws it and counts changes in one Seq the long-poll wakes on; typed
// entry and /api/sheets set rows through their own setters, leave the LCD
// where it is, and answer with what was applied or why not.

// The snapshot carries the workspace and its view, the F-key labels as
// drawn (rail tags, then a menu's choices), the tool line, the highlighted
// row with its kind, value, options or range, the picker, and the lamps.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func TestPanelStatusFields(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.show(toneFrame(""))
	st := c.PanelStatus()
	if st.Workspace != "SCOPE" || st.WorkspaceID != "scope" || st.View != "Y-T" || st.Mode != "workspace" ||
		st.Views != [5]string{"Y-T", "Packets", "FFT", "Statistics", "Compare"} {
		t.Fatalf("workspace: %+v", st)
	}
	for i, k := range st.FKeys {
		if k.Label != [5]string{"SCOP", "BUS", "SPEC", "MEAS", "COMP"}[i] || k.Sel != (i == 0) {
			t.Fatalf("rail key %d: %+v", i, k)
		}
	}
	if len(st.Tool) != 4 || st.Tool[0].Knob != "Adjust" || st.Tool[2].Knob != "Position" || st.Again != "F1 again: X-Y" {
		t.Fatalf("SCOPE tool line %+v again %q", st.Tool, st.Again)
	}
	if st.Lamps&(ledCH1|ledCH2|ledRun) != ledCH1|ledCH2|ledRun || !strings.Contains(strings.Join(st.LampsLit, " "), "RUN") {
		t.Fatalf("lamps %#04x %v", st.Lamps, st.LampsLit)
	}

	press(t, c, "trigmenu")
	goRow(t, c, "trig.level")
	st = c.PanelStatus()
	row := st.Sheet.Row
	if st.Mode != "sheet" || !st.Sheet.Open || st.Sheet.Tab != "trig" || st.Sheet.TabName != "TRIG" || st.Sheet.Tabs[0] != "TRIG" ||
		row.ID != "trig.level" || row.Kind != "number" || row.Unit != "V" || row.Min == nil || row.Max == nil || row.Text == "" {
		t.Fatalf("sheet %+v row %+v", st.Sheet, row)
	}
	if st.FKeys[0].Label != "Down" || st.FKeys[1].Label != "Up" || st.FKeys[0].Value != "one step" {
		t.Fatalf("sheet F-keys %+v", st.FKeys)
	}
	goRow(t, c, "trig.mode")
	if st = c.PanelStatus(); st.Sheet.Row.Kind != "enum" || len(st.Sheet.Row.Options) != 2 || st.FKeys[0].Label != "Auto" || !st.FKeys[0].Sel {
		t.Fatalf("enum row %+v keys %+v", st.Sheet.Row, st.FKeys)
	}
	press(t, c, "trigmenu")

	r.show(uartFrame([]string{"Hi!\r\n"}, 460800, 100e-6, 100e-9, 20000))
	press(t, c, "f2")
	r.show(uartFrame([]string{"Hi!\r\n"}, 460800, 100e-6, 100e-9, 20000))
	press(t, c, "adjustpush")
	st = c.PanelStatus()
	if st.Mode != "picker" || !st.Picker.Open || st.Picker.Mode != "pick" || len(st.Picker.Tokens) < 5 || st.Picker.Tokens[0] != "48" ||
		st.FKeys[0].Label != "Trig on 1" || st.FKeys[0].Value != "48" {
		t.Fatalf("picker %+v keys %+v", st.Picker, st.FKeys)
	}
	press(t, c, "menu", "utility")
	goRow(t, c, "bus.match")
	pickKey(t, c, "Edit bytes")
	st = c.PanelStatus()
	if st.Mode != "editor" || st.Picker.Mode != "editor" || st.Picker.KeySet != "Hex" || st.Picker.Key != "0" || st.Tool[0].Job != "key" {
		t.Fatalf("editor %+v tool %+v", st.Picker, st.Tool)
	}
}

// The Seq changes with every visible change (a key, a knob, a remote set,
// an engine-side change the lamps show) and not without one; the long-poll
// returns as soon as it changes, or at its timeout with the same Seq.
// TRLC-LINKS: REQ-SDS-211
func TestSeqAndLongPoll(t *testing.T) {
	c, eng, _ := newC(t)
	s0 := c.PanelStatus().Seq
	if s := c.PanelStatus().Seq; s != s0 {
		t.Fatalf("Seq moved without a change: %d -> %d", s0, s)
	}
	step := func(what string, fn func()) {
		t.Helper()
		before := c.PanelStatus().Seq
		fn()
		if c.PanelStatus().Seq == before {
			t.Fatalf("%s left the Seq at %d", what, before)
		}
	}
	step("F2", func() { press(t, c, "f2") })
	step("Adjust", func() { press(t, c, "menu"); turn(c, "adjust", 1) })
	step("a remote set", func() { c.SheetSet("trig.mode", nil, "Normal") })
	step("Run/Stop from SCPI", func() { eng.SetRunning(false); c.SyncLEDs() })
	press(t, c, "menu") // close the sheet: F3 switches workspaces again

	// The long-poll wakes on a change from another goroutine.
	stop := make(chan struct{})
	defer close(stop)
	go c.Run(stop)
	waitLoop(t, c)
	seq := c.PanelStatus().Seq
	got := make(chan Status, 1)
	go func() { got <- c.WaitPanel(seq, 5*time.Second) }()
	time.Sleep(20 * time.Millisecond)
	if !c.InjectButton("f3") {
		t.Fatal("F3 rejected")
	}
	select {
	case st := <-got:
		if st.Seq == seq || st.Workspace != "SPECTRUM" {
			t.Fatalf("woke with Seq %d (was %d) on %s", st.Seq, seq, st.Workspace)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the long-poll did not wake on F3")
	}
	seq = c.PanelStatus().Seq
	t0 := time.Now()
	if st := c.WaitPanel(seq, 80*time.Millisecond); st.Seq != seq || time.Since(t0) < 70*time.Millisecond {
		t.Fatalf("timeout: Seq %d (was %d) after %v", st.Seq, seq, time.Since(t0))
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("the long-poll overran its timeout: %v", d)
	}
}

// A mask test counting while the LCD's cursor rests on MATH/REF Counters
// (whose text is the live pass/fail count) does not move the Seq the
// remotes' long-polls wake on, nor SheetsSeq: the counts are not a setting.
// A setting changed meanwhile moves both.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func TestALiveRowLeavesTheSeqAlone(t *testing.T) {
	c, eng, _ := newC(t)
	c.Sheets() // a remote reads the sheets: their values count in the Seq
	press(t, c, "math")
	goRow(t, c, "cmp.counters")
	st0 := c.PanelStatus()
	if st0.Sheet.Row == nil || st0.Sheet.Row.ID != "cmp.counters" || st0.Sheet.Row.Text != "" {
		t.Fatalf("the LCD's row: %+v", st0.Sheet.Row)
	}
	for i := 1; i <= 50; i++ {
		eng.set(func(st *engine.Stats) { st.MaskPass, st.MaskFail = int64(i), int64(i/7) })
		c.SyncLEDs() // the panel loop's 40 ms tick
		if st := c.PanelStatus(); st.Seq != st0.Seq || st.SheetsSeq != st0.SheetsSeq {
			t.Fatalf("count %d moved the Seq %d -> %d (sheets %d -> %d)", i, st0.Seq, st.Seq, st0.SheetsSeq, st.SheetsSeq)
		}
	}
	if got := rowText(c, "cmp.counters"); got != "pass 50  fail 7" {
		t.Fatalf("the LCD's row says %q", got)
	}
	if res := c.SheetSet("cmp.mask_tol", nil, "8pt/12cd"); !res.OK {
		t.Fatalf("tolerance: %+v", res)
	}
	if st := c.PanelStatus(); st.Seq == st0.Seq || st.SheetsSeq == st0.SheetsSeq {
		t.Fatalf("a setting left the Seq at %d (sheets %d)", st.Seq, st.SheetsSeq)
	}
	// the workspace alone moves the Seq, not the sheet values' count
	st1 := c.PanelStatus()
	if res := c.RemoteWorkspace("measure"); !res.OK {
		t.Fatalf("MEASURE: %+v", res)
	}
	if st := c.PanelStatus(); st.Seq == st1.Seq {
		t.Fatalf("a workspace switch left the Seq at %d", st.Seq)
	}
}

// COMPARE's trigger-stability verdict flickers with a marginal signal: the
// LCD's pane shows it, but the remote's snapshot names the key's job
// ("stack master"), so the verdict does not move the Seq; a refused master
// says why when the key is pressed.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-218, REQ-SDS-220
func TestCompareStabilityLeavesTheSeqAlone(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	press(t, c, "f5")
	c.Sheets()
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 8; i++ {
		r.show(toneFrame(""))
	}
	st0 := c.PanelStatus()
	if st0.AgainNext != "stack master" || st0.Again != "F5 again: stack master" {
		t.Fatalf("COMPARE: again %q next %q", st0.Again, st0.AgainNext)
	}
	for i := 0; i < 8; i++ {
		r.show(noiseFrame(rng))
	}
	if !strings.Contains(c.View().Cmp.Again, "needs a stable trigger") {
		t.Fatalf("the LCD's pane: %q", c.View().Cmp.Again)
	}
	if st := c.PanelStatus(); st.Seq != st0.Seq || st.AgainNext != "stack master" || st.Again != "F5 again: stack master" {
		t.Fatalf("the verdict moved the snapshot: Seq %d -> %d, again %q next %q", st0.Seq, st.Seq, st.Again, st.AgainNext)
	}
	if res := c.RemoteAgain(); res.OK || !strings.Contains(res.Error, "No master") {
		t.Fatalf("again on noise: %+v", res)
	}
}

// waitLoop waits for Run to serve injected events.
func waitLoop(t *testing.T, c *Controller) {
	t.Helper()
	for i := 0; i < 200 && !c.looping.Load(); i++ {
		time.Sleep(time.Millisecond)
	}
}

// Typed entry: a number row takes SI text and is clamped and validated as
// the knob is; an enum row takes its label; the byte picker and the match
// editor take hex or quoted text. Errors say what is wrong.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-214
func TestTypedEntry(t *testing.T) {
	c, eng, _ := newC(t)
	if res := c.TypeText("1V"); res.OK || !strings.Contains(res.Error, "nothing to type into") {
		t.Fatalf("no context: %+v", res)
	}
	press(t, c, "trigmenu")
	goRow(t, c, "trig.level")
	res := c.TypeText("1.25V")
	if !res.OK || res.Row != "trig.level" || res.Text != "1.25V" || eng.Snapshot().TrigCode != 31437-1139 {
		t.Fatalf("level 1.25V: %+v code %d", res, eng.Snapshot().TrigCode)
	}
	res = c.TypeText("500m")
	if v, _ := res.Value.(float64); !res.OK || math.Abs(v-0.5) > 1.0/911 { // the DAC step, as the knob sets it
		t.Fatalf("level 500m: %+v", res)
	}
	if res = c.TypeText("50 V"); res.OK || !strings.Contains(res.Error, "level 50V is outside") {
		t.Fatalf("level 50 V: %+v", res)
	}
	if res = c.TypeText("fast"); res.OK || res.Error == "" {
		t.Fatalf("level fast: %+v", res)
	}
	goRow(t, c, "trig.edge")
	if res = c.TypeText("c2 fall"); !res.OK || eng.Snapshot().TrigSource != 1 || eng.Snapshot().TrigRising {
		t.Fatalf("enum by label: %+v", res)
	}
	if res = c.TypeText("C3 Rise"); res.OK || !strings.Contains(res.Error, "is not one of") {
		t.Fatalf("bad label: %+v", res)
	}
	press(t, c, "utility")
	goRow(t, c, "bus.baud")
	c.SheetSet("bus.protocol", nil, "UART")
	if res = c.TypeText("2k"); !res.OK || c.decBaud != 2000 {
		t.Fatalf("baud 2k: %+v baud %d", res, c.decBaud)
	}
	if res = c.TypeText("115200"); !res.OK || c.decBaud != 115200 {
		t.Fatalf("baud 115200: %+v", res)
	}
	goRow(t, c, "bus.match")
	pickKey(t, c, "Edit bytes")
	if res = c.TypeText(`"Hi\r\n"`); !res.OK || res.Value != "48 69 0D 0A" {
		t.Fatalf("editor text: %+v", res)
	}
	if got := eng.SerialConfig(); len(got.Bytes) != 4 && len(got.Pattern) != 4 {
		t.Fatalf("match not in the trigger: %+v", got)
	}
	goRow(t, c, "bus.match")
	pickKey(t, c, "Edit bytes")
	if res = c.TypeText("55 AA 0F"); !res.OK || res.Value != "55 AA 0F" {
		t.Fatalf("editor hex: %+v", res)
	}
	goRow(t, c, "bus.match")
	pickKey(t, c, "Edit bytes")
	if res = c.TypeText("55 ZZ"); res.OK || res.Error == "" {
		t.Fatalf("bad hex: %+v", res)
	}
}

// A remote set changes the setting and nothing on the LCD: an open sheet
// stays on its tab and row (showing the new value), a closed one stays
// closed; the LCD shows a REMOTE note, unless the SYS row turns them off.
// Actions run only through the action call, never through a set.
// TRLC-LINKS: REQ-SDS-212
func TestRemoteSetLeavesTheLCDAlone(t *testing.T) {
	c, eng, fe := newC(t)
	withClock(c)
	press(t, c, "f3")
	before := c.PanelStatus()
	if res := c.SheetSet("ch2.coupling", nil, "AC"); !res.OK || fe.cpl[1] != 1 {
		t.Fatalf("coupling: %+v", res)
	}
	after := c.PanelStatus()
	if after.Workspace != before.Workspace || after.Mode != "workspace" || after.View != before.View {
		t.Fatalf("a remote set moved the LCD: %s/%s -> %s/%s", before.Workspace, before.Mode, after.Workspace, after.Mode)
	}
	if after.Remote != "REMOTE  CH2 coupling AC" {
		t.Fatalf("remote note %q", after.Remote)
	}
	press(t, c, "trigmenu")
	goRow(t, c, "trig.holdoff")
	press(t, c, "adjustpush") // editing on the LCD
	if res := c.SheetSet("trig.mode", nil, "Normal"); !res.OK || !eng.Snapshot().Norm {
		t.Fatalf("mode: %+v", res)
	}
	if st := c.PanelStatus(); !st.Sheet.Open || st.Sheet.Tab != "trig" || st.Sheet.Row.ID != "trig.holdoff" || !st.Sheet.Edit {
		t.Fatalf("the LCD's sheet moved: %+v", st.Sheet)
	}
	if res := c.SheetAction("acq.run", ""); res.OK || !strings.Contains(res.Error, "a setting, not an action") {
		t.Fatalf("action on a setting: %+v", res)
	}
	if res := c.SheetSet("trig.force", nil, "Force now"); res.OK || !strings.Contains(res.Error, "is an action") {
		t.Fatalf("set on an action: %+v", res)
	}
	if res := c.SheetSet("nope.row", nil, "1"); res.OK || !strings.Contains(res.Error, `no row "nope.row"`) {
		t.Fatalf("unknown row: %+v", res)
	}
	c.SheetSet("sys.remote_notices", nil, "Off")
	c.SheetSet("trig.mode", nil, "Auto")
	if d, _ := c.Snapshot(); d.Remote != "" {
		t.Fatalf("remote notes off, yet %q", d.Remote)
	}
	if st := c.PanelStatus(); !st.Sheet.Open || st.Sheet.Row.ID != "trig.holdoff" {
		t.Fatal("the LCD's sheet moved")
	}
}

// Remote actions run exactly once each, from the action call only, and a
// remote Default setup or Autoset leaves the LCD's open sheet as it is.
// TRLC-LINKS: REQ-SDS-212
func TestRemoteActions(t *testing.T) {
	c, _, _ := newC(t)
	var forced int
	c.eng = &forceEng{fakeEng: c.eng.(*fakeEng), n: &forced}
	press(t, c, "trigmenu")
	goRow(t, c, "trig.mode")
	if res := c.SheetAction("trig.force", ""); !res.OK || forced != 1 {
		t.Fatalf("force: %+v (%d)", res, forced)
	}
	if res := c.SheetAction("trig.force", "nope"); res.OK || !strings.Contains(res.Error, `no action "nope"`) {
		t.Fatalf("bad action: %+v", res)
	}
	if res := c.SheetAction("sys.default", ""); !res.OK {
		t.Fatalf("default: %+v", res)
	}
	if st := c.PanelStatus(); !st.Sheet.Open || st.Sheet.Row.ID != "trig.mode" {
		t.Fatalf("a remote Default setup moved the LCD: %+v", st.Sheet)
	}
	if forced != 1 {
		t.Fatalf("the action ran %d times", forced)
	}
}

// forceEng is the fake engine with FORCE.
type forceEng struct {
	*fakeEng
	n *int
}

// ForceTrigger counts forced captures.
func (e *forceEng) ForceTrigger() { *e.n++ }
