// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math/rand"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// noiseFrame is a capture that does not repeat: a random C1.
func noiseFrame(rng *rand.Rand) *engine.Frame {
	f := toneFrame("")
	for i := range f.C1 {
		f.C1[i] = uint8(40 + rng.Intn(170))
	}
	return f
}

// COMPARE again stacks a master only from a trigger source that repeats:
// otherwise it refuses and says how to fix the trigger.
// TRLC-LINKS: REQ-SDS-218
func TestMasterRefusedWhileTheSourceDoesNotRepeat(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	rng := rand.New(rand.NewSource(3))
	press(t, c, "f5")
	for i := 0; i < 8; i++ {
		r.show(noiseFrame(rng))
	}
	if !strings.Contains(c.View().Cmp.Again, "needs a stable trigger") {
		t.Fatalf("again: %q", c.View().Cmp.Again)
	}
	press(t, c, "f5")
	if c.stack != nil || r.eng.Snapshot().AcqMode != 0 || !strings.Contains(c.View().Notice, "set a stable trigger first") {
		t.Fatalf("stacked on noise: stack %v acq %d notice %q", c.stack != nil, r.eng.Snapshot().AcqMode, c.View().Notice)
	}
}

// A master: COMPARE again averages N captures of the trigger source with
// the engine's Average, stores the result as REF A, builds the mask from
// it, restores the acquisition and starts the test with zeroed counters;
// COMPARE again while stacking cancels and restores.
// TRLC-LINKS: REQ-SDS-218
func TestMasterStacksStoresAndTests(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.eng.set(func(st *engine.Stats) { st.AvgCount = 4 })
	press(t, c, "f5")
	for i := 0; i < 8; i++ {
		r.show(toneFrame(""))
	}
	press(t, c, "f5")
	if st := r.eng.Snapshot(); c.stack == nil || st.AcqMode != engine.AcqAverage || st.AvgCount != 64 {
		t.Fatalf("not stacking: acq %d avg %d", st.AcqMode, st.AvgCount)
	}
	press(t, c, "f5") // again: cancel, the acquisition restored
	if st := r.eng.Snapshot(); c.stack != nil || st.AcqMode != 0 || st.AvgCount != 4 {
		t.Fatalf("cancel: acq %d avg %d", st.AcqMode, st.AvgCount)
	}
	press(t, c, "f5")
	r.show(toneFrame("block average 30/64 (waveform aligned)"))
	if n, of, _, ok := c.stackStatus(); !ok || n != 30 || of != 64 {
		t.Fatalf("progress %d/%d", n, of)
	}
	r.eng.set(func(st *engine.Stats) { st.MaskPass, st.MaskFail = 9, 2 })
	r.show(toneFrame("block average 64/64 (waveform aligned)"))
	// the mask waits for a live capture: it is built in the geometry the test judges
	if st := r.eng.Snapshot(); st.MaskSet || c.View().Cmp.Rows[cmpMask].Info != "building..." {
		t.Fatalf("before a live capture: mask %v, row %+v", st.MaskSet, c.View().Cmp.Rows[cmpMask])
	}
	for range 3 { // the live geometry holds: the mask is built in it
		r.show(toneFrame(""))
	}
	st := r.eng.Snapshot()
	if c.stack != nil || !c.RefView()[0].Show || !st.MaskSet || st.MaskMode != 1 || st.AcqMode != 0 || st.AvgCount != 4 ||
		st.MaskPass != 0 || st.MaskFail != 0 {
		t.Fatalf("master: stack %v ref %v mask %v/%d acq %d avg %d counts %d/%d", c.stack != nil, c.RefView()[0].Show,
			st.MaskSet, st.MaskMode, st.AcqMode, st.AvgCount, st.MaskPass, st.MaskFail)
	}
	if v := c.View(); !strings.HasPrefix(v.Cmp.Rows[cmpMaster].Info, "REF A: 64 x C1") || v.Cmp.Rows[cmpMask].Info != "counting" {
		t.Fatalf("rows after the master: %+v / %+v", v.Cmp.Rows[cmpMaster], v.Cmp.Rows[cmpMask])
	}
}

// The rows' pushes: the master's depth, store then show/hide a reference,
// the mask test's three states once a mask exists, the math functions.
// TRLC-LINKS: REQ-SDS-218
func TestCompareRowActions(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.show(toneFrame(""))
	press(t, c, "f5")
	press(t, c, "adjustpush") // MASTER: depth 64 -> 128
	if c.stackN != 128 {
		t.Fatalf("depth %d", c.stackN)
	}
	turn(c, "adjust", 1) // REF A
	press(t, c, "adjustpush")
	if rv := c.RefView()[0]; !rv.Show || rv.C1 == nil {
		t.Fatal("REF A not stored")
	}
	press(t, c, "adjustpush")
	if c.RefView()[0].Show {
		t.Fatal("REF A not hidden")
	}
	turn(c, "adjust", 2) // MASK
	r.eng.set(func(st *engine.Stats) { st.MaskSet, st.MaskMode = true, 0 })
	for _, want := range []int{1, 2, 0} {
		press(t, c, "adjustpush")
		if got := r.eng.Snapshot().MaskMode; got != want {
			t.Fatalf("mask push: mode %d, want %d", got, want)
		}
	}
	turn(c, "adjust", 1) // MATH
	for _, want := range []int{2, 3, 1, 4, 0} {
		press(t, c, "adjustpush")
		if c.mathMode != want {
			t.Fatalf("math push: %d, want %d", c.mathMode, want)
		}
	}
}

// A master is refused while the acquisition is Average: the stability check
// and the mask test both need raw captures. A master started there stacked a
// source that did not repeat and restored Average, so its mask never came or
// failed every capture (bug hunt #3).
// TRLC-LINKS: REQ-SDS-218
func TestMasterRefusedInAverage(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.eng.SetAcqMode(engine.AcqAverage)
	r.eng.set(func(st *engine.Stats) { st.AvgCount = 16 })
	press(t, c, "f5")
	press(t, c, "f5") // again: stack
	st := r.eng.Snapshot()
	if c.stack != nil || st.AcqMode != engine.AcqAverage || st.AvgCount != 16 {
		t.Fatalf("stacked from Average: stack %v acq %d avg %d", c.stack != nil, st.AcqMode, st.AvgCount)
	}
	if n := c.View().Notice; !strings.Contains(n, "acquisition is Average") || !strings.Contains(n, "set it to Normal") {
		t.Fatalf("notice %q", n)
	}
}
