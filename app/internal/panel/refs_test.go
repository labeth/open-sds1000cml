// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// A reference is the screen at the save: from a deep stopped record it holds
// the window around the trigger, with the edge at the trigger position, and
// the save's analog V/div.
// TRLC-LINKS: REQ-SDS-138
func TestRefSavesTheScreenWindow(t *testing.T) {
	c, eng, fe := newC(t)
	eng.stats.TrigPosFrac = .25
	n := 1 << 20
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: 8000, EdgeX: 700000}
	for i := range f.C1 {
		f.C1[i], f.C2[i] = 128, 128
		if i >= 700000 {
			f.C1[i] = 200
		}
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) })
	c.captureRef(0)
	r := c.RefView()[0]
	if len(r.C1) != RefCols || !r.Show {
		t.Fatalf("ref %d columns, show %v", len(r.C1), r.Show)
	}
	edge := -1
	for x, v := range r.C1 {
		if v == 200 {
			edge = x
			break
		}
	}
	if edge != RefCols/4 {
		t.Fatalf("edge at column %d, want %d", edge, RefCols/4)
	}
	if r.Vdiv[0] != analog.AnalogVdiv(fe.idx[0]) {
		t.Fatalf("saved V/div %v", r.Vdiv[0])
	}
}

// The F-keys pick a list option directly; Position < > steps it and stops
// at the ends (the TIME tab's Zoom row).
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-212
func TestSheetOptionsPickAndClamp(t *testing.T) {
	c, _, _ := newC(t)
	press(t, c, "horizmenu")
	goRow(t, c, "time.zoom")
	pickKey(t, c, "20x")
	if c.zoom != 20 {
		t.Fatalf("F-key picked zoom %d", c.zoom)
	}
	turn(c, "horizpos", 3) // past the end
	if c.zoom != 50 {
		t.Fatalf("Position past the end: zoom %d", c.zoom)
	}
	turn(c, "horizpos", -9)
	if c.zoom != 1 {
		t.Fatalf("Position past the start: zoom %d", c.zoom)
	}
}

// The pulse rows show and step the qualifiers the engine uses, set on the
// web or over SCPI, and the width ladder reaches past 50 us.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-212
func TestPulseRowsFollowEngine(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.TrigType = 1
	eng.stats.TrigQual = engine.TrigQual{PulseLvl: .5, PulseMinNs: 20000, PulseMaxNs: 150000, PulseCond: 2, SlopeLo: .2, SlopeHi: .8, SlopeMinNs: 100, SlopeMaxNs: 1000}
	if a, b := rowText(c, "trig.pulse_min"), rowText(c, "trig.pulse_max"); a != "20us" || b != "150us" {
		t.Fatalf("rows show %q / %q", a, b)
	}
	goRow(t, c, "trig.pulse_max")
	pickKey(t, c, "Up") // one ladder step from the engine's value
	if q := eng.Snapshot().TrigQual; q.PulseMaxNs != 200000 || q.PulseMinNs != 20000 {
		t.Fatalf("stepped to min %v max %v", q.PulseMinNs, q.PulseMaxNs)
	}
	if got := stepNs(5e9, 1, false); got != 1e10 {
		t.Fatalf("ladder top %v", got)
	}
	if got := stepNs(1e10, 1, true); got != 10 {
		t.Fatalf("press past the top %v", got)
	}
}

// TRLC-LINKS: REQ-SDS-072, REQ-SDS-136
func TestPanelStartPreservesRestoredQualifiers(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.TrigQual = engine.TrigQual{PulseLvl: .4, PulseMinNs: 105000, PulseMaxNs: 120000, PulseCond: 3, SlopeLo: .3, SlopeHi: .7, VideoStd: 1, VideoLine: 42}
	stop := make(chan struct{})
	close(stop)
	c.Run(stop)
	if c.pulseMin != 105000 || c.pulseMax != 120000 || c.videoStd != 1 || c.videoLine != 42 || c.videoNeg {
		t.Fatal("panel startup replaced restored qualifiers")
	}
}

// The web app takes the scope's references (GET /api/refs) with the volts at
// the probe tip, as its own volts per code, the timebase and the trigger's
// place across the screen: the web's Stack master shows the scope's master
// as its REF A.
// TRLC-LINKS: REQ-SDS-138, REQ-SDS-218
func TestRefsExportForTheWeb(t *testing.T) {
	c, eng, fe := newC(t)
	eng.stats.TrigPosFrac, eng.stats.TdivS = .25, 100e-6
	fe.SetProbe(0, 10)
	n := 4096
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: 2048, EdgeX: 2048}
	for i := range f.C1 {
		f.C1[i], f.C2[i] = 128, 60
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) })
	c.captureRef(0)
	e := c.RefsExport()
	a := e[0]
	if !a.Has || e[1].Has || a.Cols != RefCols || len(a.C1) != RefCols || a.C1[0] != 128 || a.C2[0] != 60 {
		t.Fatalf("export %+v / %+v", a.Has, e[1].Has)
	}
	v := analog.AnalogVdiv(fe.idx[0])
	if a.Vdiv[0] != 10*v || a.Vdiv[1] != analog.AnalogVdiv(fe.idx[1]) || a.TdivS != 100e-6 || a.PosFrac != .25 {
		t.Fatalf("tip V/div %v (want %v), tdiv %v, trigger at %v", a.Vdiv, 10*v, a.TdivS, a.PosFrac)
	}
}

// A web page's REF A/B are the scope's: it stores, hides and deletes a slot
// as the phone's COMPARE rows do, the status names each store so a page reads
// it again, the export carries the probes and the setup it was stored at,
// and the rows say what a slot holds.
// TRLC-LINKS: REQ-SDS-227
func TestWebRefActionsAreTheScopes(t *testing.T) {
	c, eng, fe := newC(t)
	eng.stats.TdivS = 100e-6
	fe.SetProbe(1, 10)
	n := 4096
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: 2048, EdgeX: 2048}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) })
	if r := c.RefAction(1, "store"); !r.OK {
		t.Fatalf("store B: %+v", r)
	}
	tags := c.PanelStatus().Refs
	if tags[0].Has || !tags[1].Has || !tags[1].Show || tags[1].Gen == 0 {
		t.Fatalf("status refs after storing B: %+v", tags)
	}
	b := c.RefsExport()[1]
	if b.Gen != tags[1].Gen || b.At == 0 || b.Probe != [2]float64{1, 10} || b.Setup == nil || b.Setup.TdivS != 100e-6 ||
		b.Setup.Vdiv[1] != 10*analog.AnalogVdiv(fe.idx[1]) {
		t.Fatalf("export of B: gen %d at %d probe %v setup %+v", b.Gen, b.At, b.Probe, b.Setup)
	}
	if got := rowText(c, "cmp.store_b"); got != "holds C1 and C2 at 100us/div" {
		t.Fatalf("Store Ref B row says %q", got)
	}
	if r := c.RefAction(1, "hide"); !r.OK || c.RefsExport()[1].Show || c.PanelStatus().Refs[1].Show {
		t.Fatalf("hide B: %+v, export show %v", r, c.RefsExport()[1].Show)
	}
	if r := c.RefAction(1, "store"); !r.OK || c.PanelStatus().Refs[1].Gen == tags[1].Gen {
		t.Fatalf("a second store keeps the first one's name: %+v", c.PanelStatus().Refs[1])
	}
	if r := c.RefAction(0, "clear"); r.OK {
		t.Fatalf("clearing an empty REF A was taken: %+v", r)
	}
	if r := c.RefAction(1, "clear"); !r.OK || c.PanelStatus().Refs[1].Has || c.RefsExport()[1].Has {
		t.Fatalf("clear B: %+v", r)
	}
	if r := c.RefAction(0, "paint"); r.OK || r.Error == "" {
		t.Fatalf("an unknown action: %+v", r)
	}
}
