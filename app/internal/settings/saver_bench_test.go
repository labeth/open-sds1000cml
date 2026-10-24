// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings

import (
	"testing"
	"time"
)

// A poll with nothing changed: the saver's steady state, once a second.
// TRLC-LINKS: REQ-SDS-090
func BenchmarkSaverStepSteady(b *testing.B) {
	cur := Settings{Version: Version, TdivS: 1e-3}
	s := NewSaver("", func() Settings { return cur }, nil)
	s.save = func(string, Settings) error { return nil }
	s.step()
	for b.Loop() {
		s.step()
	}
}

// TRLC-LINKS: REQ-SDS-072
func BenchmarkSlotPath(b *testing.B) {
	s := Slots{Dir: "/usr/bin/siglent/usr/media/U-disk0/setups"}
	n := 0
	for b.Loop() {
		_ = s.slotPath(n % 20)
		n++
	}
}

// The saver still saves a change once it settles, and a change back to the
// saved state needs no save.
// TRLC-LINKS: REQ-SDS-090
func TestSaverSavesOnlyASettledChange(t *testing.T) {
	cur := Settings{Version: Version, TdivS: 1e-3}
	now := time.Unix(0, 0)
	saves := 0
	s := NewSaver("", func() Settings { return cur }, nil)
	s.save = func(string, Settings) error { saves++; return nil }
	s.now = func() time.Time { return now }
	s.step()
	cur.TdivS = 2e-3
	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		s.step()
	}
	if saves != 1 {
		t.Fatalf("%d saves after a settled change, want 1", saves)
	}
	cur.TdivS = 5e-3
	now = now.Add(time.Second)
	s.step()
	cur.TdivS = 2e-3
	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		s.step()
	}
	if saves != 1 {
		t.Fatalf("%d saves after changing back to the saved setup, want 1", saves)
	}
}

// Slot files are setup00.json to setup99.json in the directory.
// TRLC-LINKS: REQ-SDS-072
func TestSlotPathNames(t *testing.T) {
	s := Slots{Dir: "/d"}
	for n, want := range map[int]string{0: "/d/setup00.json", 7: "/d/setup07.json", 19: "/d/setup19.json", 123: "/d/setup123.json"} {
		if got := s.slotPath(n); got != want {
			t.Fatalf("slot %d: %s, want %s", n, got, want)
		}
	}
}
