// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS

package settings_test

import (
	core "github.com/labeth/open-dso-core/settings"
	"testing"
)

// Collect over a live engine, front end and panel (the saver's poll and the
// Save/Recall lamp).
// TRLC-LINKS: REQ-SDS-089
func BenchmarkCollect(b *testing.B) {
	e, fe, pc, _ := rig(b)
	for b.Loop() {
		core.Collect(e, fe, pc, ext(e, fe))
	}
}

// TRLC-LINKS: REQ-SDS-089
func BenchmarkSave(b *testing.B) { // the same setup again: the unchanged file
	path := b.TempDir() + "/s.json"
	s := sampleValid()
	for b.Loop() {
		if err := core.SaveFile(path, s); err != nil {
			b.Fatal(err)
		}
	}
}

// A changed setup is still written, and a saved one reads back.
// TRLC-LINKS: REQ-SDS-089
func TestSaveWritesAChange(t *testing.T) {
	path := t.TempDir() + "/s.json"
	s := sampleValid()
	for _, tdiv := range []float64{1e-3, 1e-3, 2e-3} {
		s.TdivS = tdiv
		if err := core.SaveFile(path, s); err != nil {
			t.Fatal(err)
		}
		got, ok := core.LoadFile(path, nil)
		if !ok || got.TdivS != tdiv {
			t.Fatalf("saved %g, read %g (%v)", tdiv, got.TdivS, ok)
		}
	}
}

// Collect's own work: the engine and the front end, without the panel.
// TRLC-LINKS: REQ-SDS-089
func BenchmarkCollectEngineAnalog(b *testing.B) {
	e, fe, _, _ := rig(b)
	for b.Loop() {
		core.Collect(e, fe, nil, ext(e, fe))
	}
}
