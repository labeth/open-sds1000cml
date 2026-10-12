// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A saved slot recalls the setup it stored; an empty slot errors (bug hunt
// 2026-10-10: *SAV/*RCL were accepted and did nothing).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func TestSlotsSaveRecall(t *testing.T) {
	cur := Settings{TdivS: 200e-6}
	var applied Settings
	s := Slots{Dir: t.TempDir(), Collect: func() Settings { return cur }, Apply: func(v Settings) { applied = v }}
	if err := s.SaveSetup(1); err != nil {
		t.Fatal(err)
	}
	cur.TdivS = 1e-3
	if err := s.RecallSetup(1); err != nil || applied.TdivS != 200e-6 {
		t.Fatalf("recall: %v, tdiv %g", err, applied.TdivS)
	}
	if err := s.RecallSetup(2); err == nil {
		t.Fatal("recalled an empty slot")
	}
}

// The screen saver is the instrument's: a saved setup, slot or named file,
// leaves it out, and recalling one that has it (written by hand) leaves the
// screen saver as it is. The settings file keeps it.
// TRLC-LINKS: REQ-SDS-221, REQ-SDS-072
func TestSetupsLeaveTheScreenSaver(t *testing.T) {
	cur := Settings{TdivS: 200e-6, Screen: Screen{Set: true, SaverMin: 5, OffMin: 30}}
	var applied Settings
	dir := t.TempDir()
	s := Slots{Dir: dir, Collect: func() Settings { return cur }, Apply: func(v Settings) { applied = v }}
	if err := s.SaveSetup(3); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSetupFile("bench.json"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"setup03.json", "bench.json"} {
		st, ok := Load(filepath.Join(dir, name), nil)
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		if !ok || st.Screen != (Screen{}) || strings.Contains(string(raw), "screen") {
			t.Fatalf("%s keeps the screen saver: %+v\n%s", name, st.Screen, raw)
		}
	}
	hand := Settings{TdivS: 1e-3, Screen: Screen{Set: true, SaverMin: 1, OffMin: 5}}
	if err := Save(filepath.Join(dir, "setup04.json"), hand); err != nil {
		t.Fatal(err)
	}
	if err := s.RecallSetup(4); err != nil || applied.TdivS != 1e-3 || applied.Screen.Set {
		t.Fatalf("recall: %v, applied %+v", err, applied)
	}
	if err := s.RecallSetupFile("setup04.json"); err != nil || applied.Screen.Set {
		t.Fatalf("recall by name: %v, applied %+v", err, applied)
	}
	path := filepath.Join(dir, "scope-settings.json")
	if err := Save(path, cur); err != nil {
		t.Fatal(err)
	}
	if st, ok := Load(path, nil); !ok || st.Screen != cur.Screen {
		t.Fatalf("the settings file lost the screen saver: %+v", st.Screen)
	}
}
