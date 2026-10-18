// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// Reachability (REQ-SDS-212): every setting is a sheet row, every row can be
// reached and changed with the panel's own inputs (Menu On/Off, S/DIV,
// Adjust, Position, the F-keys), and the same row can be set through the
// remote's /api/sheets model with its stable id. Rows that are not in use
// in one state are in another: the test walks contexts (trigger types,
// protocols, acquisition modes, zoom, super-res, cursors, references) until
// every row has been in use, and fails on a row that never is.

// reachContext puts the instrument in a state that brings rows into use.
type reachContext struct {
	name string
	prep func(t *testing.T, r *viewRig)
}

// set is a remote set that must succeed.
// TRLC-LINKS: REQ-SDS-212
func set(t *testing.T, c *Controller, id, text string) {
	t.Helper()
	if res := c.SheetSet(id, nil, text); !res.OK {
		t.Fatalf("set %s %q: %s", id, text, res.Error)
	}
}

// reachContexts are the states the reachability tests walk.
// TRLC-LINKS: REQ-SDS-212
func reachContexts() []reachContext {
	return []reachContext{
		{"base", func(*testing.T, *viewRig) {}},
		{"pulse", func(t *testing.T, r *viewRig) { set(t, r.c, "trig.type", "Pulse") }},
		{"slope", func(t *testing.T, r *viewRig) { set(t, r.c, "trig.type", "Slope") }},
		{"video", func(t *testing.T, r *viewRig) { set(t, r.c, "trig.type", "Video") }},
		{"uart", func(t *testing.T, r *viewRig) { set(t, r.c, "bus.protocol", "UART") }},
		{"i2c serial", func(t *testing.T, r *viewRig) {
			set(t, r.c, "bus.protocol", "I2C")
			set(t, r.c, "trig.type", "Serial")
		}},
		{"spi", func(t *testing.T, r *viewRig) { set(t, r.c, "bus.protocol", "SPI") }},
		{"manchester", func(t *testing.T, r *viewRig) { set(t, r.c, "bus.protocol", "Manch") }},
		{"modbus", func(t *testing.T, r *viewRig) { set(t, r.c, "bus.protocol", "Modbus") }},
		{"i2c filter", func(t *testing.T, r *viewRig) {
			set(t, r.c, "bus.protocol", "I2C")
			set(t, r.c, "bus.layer", "Address filter")
		}},
		{"stream", func(t *testing.T, r *viewRig) {
			set(t, r.c, "bus.protocol", "UART")
			set(t, r.c, "bus.stream", "On")
		}},
		{"stream on value", func(t *testing.T, r *viewRig) {
			set(t, r.c, "bus.protocol", "UART")
			set(t, r.c, "bus.stream", "On")
			set(t, r.c, "bus.stream_trigger", "On value")
		}},
		{"zoom", func(t *testing.T, r *viewRig) { set(t, r.c, "time.zoom", "10x") }},
		{"roll", func(t *testing.T, r *viewRig) { set(t, r.c, "time.tdiv", "200ms") }},
		{"average", func(t *testing.T, r *viewRig) { set(t, r.c, "acq.mode", "Average") }},
		{"eres", func(t *testing.T, r *viewRig) { set(t, r.c, "acq.mode", "ERes") }},
		{"super-res", func(t *testing.T, r *viewRig) {
			r.show(smallFrame())
			set(t, r.c, "acq.superres", "On")
		}},
		{"cursors", func(t *testing.T, r *viewRig) { set(t, r.c, "curs.mode", "Time") }},
		{"refs", func(t *testing.T, r *viewRig) {
			for _, id := range []string{"cmp.store_a", "cmp.store_b"} {
				if res := r.c.SheetAction(id, ""); !res.OK {
					t.Fatalf("%s: %s", id, res.Error)
				}
			}
		}},
		{"readouts", func(t *testing.T, r *viewRig) { set(t, r.c, "meas.readouts", "On") }},
	}
}

// smallFrame is a short square wave super-res can stack in software.
// TRLC-LINKS: REQ-SDS-212
func smallFrame() *engine.Frame {
	n := 256
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(40 + (i*7)%160)
	}
	return &engine.Frame{C1: sig, C2: sig, Valid: n, WinCols: n, EdgeX: 32, SampleS: 2e-9, TdivS: 500e-6, Trigd: true}
}

// rigFor is a rig in context x, showing a short capture super-res can stack.
// TRLC-LINKS: REQ-SDS-212
func rigFor(t *testing.T, x reachContext) *viewRig {
	r := newViewRig(t)
	// the engine's power-on qualifiers (defaultTrigParams): the time limits
	// start inside their rows' 10 ns .. 10 s, as on the scope
	r.eng.set(func(st *engine.Stats) {
		st.TrigQual = engine.TrigQual{PulseLvl: 0.5, PulseMinNs: 100, PulseMaxNs: 1000, SlopeLo: 0.2, SlopeHi: 0.8,
			SlopeMinNs: 100, SlopeMaxNs: 1000, VideoNeg: true}
	})
	r.show(smallFrame())
	x.prep(t, r)
	t.Cleanup(func() { r.c.srCancel("cancelled") })
	return r
}

// settable reports whether a row has a value to set (not an action).
// TRLC-LINKS: REQ-SDS-212
func settable(m RowModel) bool { return m.Kind != "action" }

// sameValue compares two model values (numbers within quantisation).
// TRLC-LINKS: REQ-SDS-212
func sameValue(a, b any, tol float64) bool {
	fa, oka := a.(float64)
	fb, okb := b.(float64)
	if oka && okb {
		return math.Abs(fa-fb) <= tol
	}
	ia, oka := a.(int)
	ib, okb := b.(int)
	if oka && okb {
		return ia == ib
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// otherValue is a value for m other than its current one: another option,
// the next ladder entry, a number a few steps away inside the range, other
// match bytes.
// TRLC-LINKS: REQ-SDS-212
func otherValue(m RowModel) (any, bool) {
	switch m.Kind {
	case "enum":
		for _, o := range m.Options {
			if o.Value != m.Value {
				return float64(o.Value), true
			}
		}
	case "number":
		v := m.Value.(float64)
		if l := m.Ladder; len(l) > 1 {
			i := 0
			for k := range l {
				if math.Abs(l[k]-v) < math.Abs(l[i]-v) {
					i = k
				}
			}
			if i+1 < len(l) {
				return l[i+1], true
			}
			return l[i-1], true
		}
		step := (*m.Max - *m.Min) / 10
		if m.Step != nil && *m.Step > 0 {
			step = 3 * *m.Step
		}
		if w := v + step; w <= *m.Max {
			return w, true
		}
		if w := v - step; w >= *m.Min {
			return w, true
		}
	case "bytes":
		if m.Value == "55 AA" {
			return "12 34", true
		}
		return "55 AA", true
	}
	return nil, false
}

// tolOf is the quantisation a number row may round a value to.
// TRLC-LINKS: REQ-SDS-212
func tolOf(m RowModel) float64 {
	if m.Step != nil && *m.Step > 0 {
		return *m.Step/2 + 1e-9
	}
	if v, ok := m.Value.(float64); ok {
		return math.Abs(v)*1e-6 + 1e-12
	}
	return 0
}

// reach records, per row, whether any context changed it and the last
// reason one could not.
type reach struct {
	ok  map[string]bool
	why map[string]string
}

// TRLC-LINKS: REQ-SDS-212
func newReach() *reach { return &reach{ok: map[string]bool{}, why: map[string]string{}} }

// never lists the settable rows no context changed, with the last reason.
// TRLC-LINKS: REQ-SDS-212
func (rc *reach) never() []string {
	var out []string
	for _, tb := range sheetTabs {
		for _, rw := range tb.rows {
			if rw.kind != wsview.Action && rw.kind != wsview.Slot && !rc.ok[rw.id] {
				out = append(out, rw.id+" ("+rc.why[rw.id]+")")
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryRowReachableThroughSheetsAPI sets every row through the remote's
// model in a state that uses it, and reads the value back.
// TRLC-LINKS: REQ-SDS-212
func TestEveryRowReachableThroughSheetsAPI(t *testing.T) {
	rc := newReach()
	for _, x := range reachContexts() {
		t.Run(x.name, func(t *testing.T) {
			r := rigFor(t, x)
			model := r.c.Sheets()
			for _, tab := range model.Tabs {
				for _, m := range tab.Rows {
					if !m.Available || !settable(m) {
						continue
					}
					v, ok := otherValue(m)
					if !ok {
						rc.why[m.ID] = fmt.Sprintf("no other value than %v", m.Value)
						continue
					}
					res := r.c.SheetSet(m.ID, v, "")
					if !res.OK {
						rc.why[m.ID] = fmt.Sprintf("%s: set %v: %s", x.name, v, res.Error)
						continue
					}
					got := rowModel(r.c.snap(), rowByID[m.ID])
					if !sameValue(got.Value, v, tolOf(m)) || !sameValue(res.Value, got.Value, 0) {
						t.Errorf("%s: set %v, reads %v (answer %v)", m.ID, v, got.Value, res.Value)
						continue
					}
					rc.ok[m.ID] = true
					if res := r.c.SheetSet(m.ID, m.Value, ""); !res.OK { // as it was, for the rows after it
						t.Errorf("%s: back to %v: %s", m.ID, m.Value, res.Error)
					}
				}
			}
		})
	}
	if never := rc.never(); len(never) > 0 {
		t.Fatalf("rows never set through /api/sheets in any context:\n  %s", strings.Join(never, "\n  "))
	}
}

// TestEveryRowReachableFromThePanel reaches every row with the panel's own
// inputs and changes it with Position (the F-keys pick the same values; the
// match bytes are typed in the match editor).
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func TestEveryRowReachableFromThePanel(t *testing.T) {
	rc := newReach()
	for _, x := range reachContexts() {
		t.Run(x.name, func(t *testing.T) {
			r := rigFor(t, x)
			c := r.c
			for _, tb := range sheetTabs {
				for _, rw := range tb.rows {
					if !rw.relevant(c.snap()) || rw.kind == wsview.Action || rw.kind == wsview.Slot || rc.ok[rw.id] {
						continue
					}
					goRow(t, c, rw.id)
					before := rowModel(c.snap(), rw)
					if rw.kind == wsview.Bytes {
						pickKey(t, c, "Edit bytes") // the match editor: type 7 7, OK
						turn(c, "adjust", 7)
						press(t, c, "adjustpush", "adjustpush", "f4")
					} else {
						turn(c, "horizpos", 1)
						if after := rowModel(c.snap(), rw); sameValue(after.Value, before.Value, 0) {
							turn(c, "horizpos", -2) // at the end: the other way
						}
					}
					if after := rowModel(c.snap(), rw); sameValue(after.Value, before.Value, 0) {
						rc.why[rw.id] = fmt.Sprintf("%s: Position left %v (%s)", x.name, before.Value, c.View().Notice)
						continue
					}
					rc.ok[rw.id] = true
				}
			}
			press(t, c, "menu")
		})
	}
	if never := rc.never(); len(never) > 0 {
		t.Fatalf("rows never changed from the panel in any context:\n  %s", strings.Join(never, "\n  "))
	}
}

// Every row is in the model with a unique, stable id, its kind, a label, a
// one-line help, and its options or range; actions name their actions.
// TRLC-LINKS: REQ-SDS-212
func TestSheetsModelListsEveryRow(t *testing.T) {
	c, _, _ := newC(t)
	m := c.Sheets()
	seen := map[string]bool{}
	n := 0
	for i, tab := range m.Tabs {
		if tab.ID != sheetTabs[i].id || len(tab.Rows) != len(sheetTabs[i].rows) {
			t.Fatalf("tab %d: %s with %d rows", i, tab.ID, len(tab.Rows))
		}
		for _, rw := range tab.Rows {
			n++
			if seen[rw.ID] || !strings.HasPrefix(rw.ID, tab.ID+".") {
				t.Errorf("row id %q: duplicate or outside its tab %s", rw.ID, tab.ID)
			}
			seen[rw.ID] = true
			if rw.Label == "" || rw.Help == "" || rw.Text == "" && rw.Kind != "action" {
				t.Errorf("%s: label %q help %q text %q", rw.ID, rw.Label, rw.Help, rw.Text)
			}
			switch rw.Kind {
			case "enum":
				if len(rw.Options) < 2 {
					t.Errorf("%s: %d options", rw.ID, len(rw.Options))
				}
			case "number":
				if rw.Min == nil || rw.Max == nil || *rw.Max <= *rw.Min || rw.Step == nil && len(rw.Ladder) == 0 {
					t.Errorf("%s: no range or step", rw.ID)
				}
			case "action":
				if len(rw.Actions) == 0 {
					t.Errorf("%s: no actions", rw.ID)
				}
			case "bytes":
			default:
				t.Errorf("%s: kind %q", rw.ID, rw.Kind)
			}
		}
	}
	if n != len(rowByID) {
		t.Fatalf("%d rows in the model, %d declared", n, len(rowByID))
	}
	for _, tb := range sheetTabs { // every row has what its kind needs
		for _, r := range tb.rows {
			var miss []string
			need := func(ok bool, what string) {
				if !ok {
					miss = append(miss, what)
				}
			}
			switch r.kind {
			case wsview.Enum:
				need(r.opts != nil, "opts")
				need(r.get != nil, "get")
				need(r.set != nil, "set")
			case wsview.Number:
				need(r.val != nil, "val")
				need(r.setN != nil, "setN")
				need(r.stepN != nil, "stepN")
				need(r.lim != nil, "lim")
				need(r.fmtN != nil || r.fmtS != nil, "fmtN")
			case wsview.Action, wsview.Slot:
				need(r.acts != nil, "acts")
			}
			if len(miss) > 0 {
				t.Errorf("%s lacks %s", r.id, strings.Join(miss, ", "))
			}
		}
	}
	for _, id := range []string{"trig.level", "ch2.coupling", "bus.strip_format", "cmp.master_depth", "bus.match", "sys.remote_notices"} {
		if !seen[id] {
			t.Errorf("stable id %s missing", id)
		}
	}
}
