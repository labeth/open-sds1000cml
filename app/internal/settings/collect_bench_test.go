// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings_test

import (
	"testing"

	"open-sds/app/internal/settings"
)

// Collect over a live engine, front end and panel (the saver's poll and the
// Save/Recall lamp).
// TRLC-LINKS: REQ-SDS-089
func BenchmarkCollect(b *testing.B) {
	e, fe, pc, _ := rig(b)
	for b.Loop() {
		settings.Collect(e, fe, pc)
	}
}

// TRLC-LINKS: REQ-SDS-089
func BenchmarkSave(b *testing.B) { // the same setup again: the unchanged file
	path := b.TempDir() + "/s.json"
	s := sampleValid()
	for b.Loop() {
		if err := settings.Save(path, s); err != nil {
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
		if err := settings.Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, ok := settings.Load(path, nil)
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
		settings.Collect(e, fe, nil)
	}
}
