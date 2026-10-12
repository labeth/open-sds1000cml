// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
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
	if res := c.RemoteAgain(""); res.OK || !strings.Contains(res.Error, "No master") {
		t.Fatalf("again on noise: %+v", res)
	}
}

// A remote call says what it did, measured on the panel goroutine: the rows
// whose value it changed (side effects too), the notice it raised and the
// sheet values' count after it; nothing for a set that changes nothing or
// is refused. A timebase counts at once, though the engine applies it at
// its next capture.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func TestRemoteCallsSayWhatTheyChanged(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	c.Sheets()
	res := c.SheetSet("acq.averages", nil, "64") // a count also selects Average
	if !res.OK || fmt.Sprint(res.Changed) != "[acq.mode acq.averages]" || res.SheetsSeq == 0 || res.SheetsSeq != c.PanelStatus().SheetsSeq {
		t.Fatalf("averages: %+v (sheets %d)", res, c.PanelStatus().SheetsSeq)
	}
	if res := c.SheetSet("acq.averages", nil, "64"); !res.OK || len(res.Changed) != 0 {
		t.Fatalf("the same value again: %+v", res)
	}
	if res := c.SheetSet("trig.level", nil, "99V"); res.OK || len(res.Changed) != 0 {
		t.Fatalf("a refused set: %+v", res)
	}
	res = c.RemoteWorkspace("measure") // MEASURE starts with Vpp and Freq, and says so
	if !res.OK || !slices.Contains(res.Changed, "meas.pkpk") || !slices.Contains(res.Changed, "meas.freq") || !strings.Contains(res.Notice, "Vpp") {
		t.Fatalf("MEASURE: %+v", res)
	}
	if res := c.RemoteAgain(""); !res.OK || len(res.Changed) != 0 || res.Notice != "" {
		t.Fatalf("again: %+v", res)
	}
	// the engine takes a new timebase at its next capture, after the call
	c.eng = &stagedEng{fakeEng: eng}
	if res := c.SheetSet("time.tdiv", nil, "1ms"); !res.OK || fmt.Sprint(res.Changed) != "[time.tdiv]" || eng.Snapshot().TdivS != 500e-6 {
		t.Fatalf("a staged timebase: %+v (engine %g)", res, eng.Snapshot().TdivS)
	}
	if res := c.SheetSet("time.tdiv", nil, "500us"); !res.OK || len(res.Changed) != 0 {
		t.Fatalf("the timebase in effect asked for again: %+v", res)
	}
	c.eng = eng
	if res := c.SheetAction("sys.default", ""); !res.OK || !slices.Contains(res.Changed, "acq.mode") ||
		slices.Contains(res.Changed, "ch2.coupling") || slices.Contains(res.Changed, "bus.match") {
		t.Fatalf("Default setup: %+v", res)
	}
}

// The remote's keys and knobs are answered once they have run, with what
// they changed and the notice they raised: Set to 50 % without a signal
// says so and leaves the level. While Autoset sweeps they wait as the
// panel's own keys do (AUTO cancels it); the Adjust and Position turns and
// pushes, sent with nomenu, wait while a menu is open on the LCD, where
// they would move it, and leave it as it is. Again names the workspace it
// was meant for and is refused in another one.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220
func TestRemoteKeysAnswerWhatTheyDid(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	c.SheetSet("trig.level", nil, "1.2V")
	code := eng.Snapshot().TrigCode
	if res := c.RemoteKey("set50", false); !res.OK || len(res.Changed) != 0 || res.Notice != "Set to 50 %: no signal on C1" || eng.Snapshot().TrigCode != code {
		t.Fatalf("50 %% without a signal: %+v (code %d, was %d)", res, eng.Snapshot().TrigCode, code)
	}
	if res := c.RemoteKey("triglvlpush", false); !res.OK || fmt.Sprint(res.Changed) != "[trig.level]" || res.Notice != "Trigger level 0 V" {
		t.Fatalf("level push: %+v", res)
	}
	if res := c.RemoteKey("nope", false); res.OK || res.Error != `no key "nope"` {
		t.Fatalf("an unknown key: %+v", res)
	}
	press(t, c, "trigmenu")
	goRow(t, c, "trig.mode")
	before := c.PanelStatus().Sheet
	for i, res := range []SetResult{c.RemoteKnob("adjust", 1, 1, true), c.RemoteKnob("horizpos", -1, 1, true), c.RemoteKey("adjustpush", true),
		c.RemoteKey("horizpospush", true)} {
		if res.OK || res.Error != menuRefusal || len(res.Changed) != 0 {
			t.Fatalf("%d with a menu open on the LCD: %+v", i, res)
		}
	}
	if st := c.PanelStatus().Sheet; !st.Open || st.Row.ID != before.Row.ID || st.Edit != before.Edit || eng.Snapshot().Norm {
		t.Fatalf("the LCD's sheet moved: %+v", st)
	}
	if res := c.RemoteKey("runstop", false); !res.OK || eng.Snapshot().Running {
		t.Fatalf("Run/Stop with the menu open: %+v", res)
	}
	press(t, c, "trigmenu")
	c.mu.Lock()
	c.autosetBusy = true // a sweep running
	c.mu.Unlock()
	for i, res := range []SetResult{c.RemoteKey("runstop", false), c.RemoteKnob("adjust", 1, 1, false)} {
		if res.OK || res.Error != autosetRefusal {
			t.Fatalf("%d during Autoset: %+v", i, res)
		}
	}
	if res := c.RemoteKey("auto", false); !res.OK {
		t.Fatalf("AUTO during Autoset: %+v", res)
	}
	c.mu.Lock()
	c.autosetBusy = false
	c.mu.Unlock()
	c.RemoteWorkspace("spectrum")
	if res := c.RemoteAgain("bus"); res.OK || res.Error != "BUS again: the scope shows SPECTRUM now" || c.PanelStatus().View != "FFT" {
		t.Fatalf("BUS again in SPECTRUM: %+v view %s", res, c.PanelStatus().View)
	}
	if res := c.RemoteAgain("spec"); !res.OK || res.Text != "Spectrogram" {
		t.Fatalf("SPECTRUM again: %+v", res)
	}
}

// SCOPE's Adjust tunes the last number set in a sheet: the snapshot names
// that row by id, as its label ("Offset", "V/div") is the same for CH1 and
// CH2.
// TRLC-LINKS: REQ-SDS-213, REQ-SDS-220
func TestAdjustTargetIsNamedByID(t *testing.T) {
	c, _, _ := newC(t)
	if st := c.PanelStatus(); st.AdjustRow != "" {
		t.Fatalf("nothing set yet, yet %q", st.AdjustRow)
	}
	for _, id := range []string{"ch2.offset", "ch2.vdiv", "ch1.offset"} {
		press(t, c, map[bool]string{true: "ch2", false: "ch1"}[strings.HasPrefix(id, "ch2")])
		goRow(t, c, id)
		turn(c, "horizpos", 1) // Position changes the row's value
		press(t, c, "menu")
		st := c.PanelStatus()
		if st.Mode != "workspace" || st.AdjustRow != id || st.Tool[0].Job != rowByID[id].label {
			t.Fatalf("%s: mode %s, adjust row %q, tool %+v", id, st.Mode, st.AdjustRow, st.Tool)
		}
	}
	c.RemoteWorkspace("bus")
	if st := c.PanelStatus(); st.AdjustRow != "" {
		t.Fatalf("BUS: adjust row %q", st.AdjustRow)
	}
}

// The I2C address the serial trigger requires is Any until set, and kept
// while the bus is not I2C (the row reads it, not in use) for when it is:
// setting the match on a UART bus leaves it as it is (it was moved to Any
// as a side effect, so an undo of the match could never put it back), and
// an address typed meanwhile is the one I2C then uses.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-220
func TestI2CAddressKeptOffI2C(t *testing.T) {
	c, eng, _ := newC(t)
	if r := rowModel(c.snap(), rowByID["bus.i2c_address"]); r.Text != "Any" {
		t.Fatalf("never set: %q", r.Text)
	}
	c.SheetSet("bus.protocol", nil, "I2C")
	if res := c.SheetSet("bus.i2c_address", nil, "0x50"); !res.OK {
		t.Fatalf("address: %+v", res)
	}
	c.SheetSet("bus.protocol", nil, "UART")
	res := c.SheetSet("bus.match", nil, "55 AA")
	if !res.OK || slices.Contains(res.Changed, "bus.i2c_address") || fmt.Sprint(res.Changed) != "[bus.match]" {
		t.Fatalf("the match on UART: %+v", res)
	}
	if r := rowModel(c.snap(), rowByID["bus.i2c_address"]); r.Text != "0x50" || r.Available {
		t.Fatalf("the address on UART: %+v", r)
	}
	if res := c.SheetSet("bus.i2c_address", nil, "0x42"); !res.OK || res.Text != "0x42" {
		t.Fatalf("an address typed on UART: %+v", res)
	}
	c.SheetSet("bus.protocol", nil, "I2C")
	if p := eng.SerialConfig(); p.Addr != 0x42 || rowText(c, "bus.i2c_address") != "0x42" {
		t.Fatalf("back on I2C: %+v", p)
	}
}

// stagedEng applies a timebase at its next capture, as the real engine does:
// its Snapshot keeps the old one until then.
type stagedEng struct{ *fakeEng }

// SetTdiv stages the timebase.
func (e *stagedEng) SetTdiv(t float64) (engine.Band, bool) { return engine.PlanTdiv(t) }

// An Autoset a remote starts is named in its answer; when the sweep ends
// (here cancelled by a second press) the snapshot names it as the last one
// done, with the rows it changed.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-220
func TestAutosetSaysWhatItChanged(t *testing.T) {
	c, _, _ := newC(t)
	c.SheetSet("trig.mode", nil, "Normal")
	sig := make([]uint8, 200)
	for i := range sig {
		sig[i] = uint8(40 + 160*(i/10%2))
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: sig, C2: sig, Valid: len(sig), SampleS: 50e-9})
	})
	res := c.SheetAction("sys.autoset", "")
	if !res.OK || res.Autoset == 0 || !c.AutosetBusy() {
		t.Fatalf("autoset: %+v busy %v", res, c.AutosetBusy())
	}
	time.Sleep(30 * time.Millisecond)
	if again := c.SheetAction("sys.autoset", ""); !again.OK || again.Autoset != 0 {
		t.Fatalf("the second press cancels: %+v", again)
	}
	for i := 0; i < 400 && c.AutosetBusy(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	st := c.PanelStatus()
	if c.AutosetBusy() || st.AutosetDone != res.Autoset || !slices.Contains(st.AutosetChanged, "trig.mode") {
		t.Fatalf("after the sweep: done %d (started %d), changed %v", st.AutosetDone, res.Autoset, st.AutosetChanged)
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
