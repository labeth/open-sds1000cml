package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() Settings {
	return Settings{
		Version: Version,
		TdivS:   500e-6,
		Ch: [2]Channel{
			{VdivV: 0.5, OffsetV: 1.25, OffsetSet: true, Coupling: 1, Probe: 10},
			{VdivV: 2, OffsetV: 0, OffsetSet: false, Coupling: 0, Probe: 1},
		},
		VertSet:  true,
		Trigger:  Trigger{LevelCode: 30500, Rising: false, Source: 1, Type: 1, Norm: true, HoldoffS: 1e-3},
		Acq:      Acq{Mode: 1, AvgCount: 64, EresLen: 1},
		Decode:   Decode{Proto: 2, Baud: 115200, ChA: 1, ChB: 0, CPOL: true, CPHA: false, Format: 2},
		ViewMode: 2,
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scope-settings.json")
	want := sample()
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := Load(path, t.Logf)
	if !ok {
		t.Fatal("Load: ok=false for a freshly saved file")
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	// human-readable: the file is indented JSON with the schema field visible
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "\"version\": 1") {
		t.Fatalf("file not human-readable/indented:\n%s", raw)
	}
	if len(raw) > 4096 {
		t.Fatalf("settings file unexpectedly large: %d bytes", len(raw))
	}
}

func TestSaveAtomicNoTmpLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	if err := Save(path, sample()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}

func TestLoadMissing(t *testing.T) {
	if _, ok := Load(filepath.Join(t.TempDir(), "nope.json"), t.Logf); ok {
		t.Fatal("Load: ok=true for a missing file")
	}
}

func TestLoadCorruptFallsBack(t *testing.T) {
	cases := map[string]string{
		"garbage":       "\x00\xff\xfeklaatu barada nikto",
		"truncated":     `{"version":1,"tdiv_s":`,
		"empty":         "",
		"null":          "null", // decodes to zero Settings → version 0 → rejected
		"wrong-type":    `{"version":1,"tdiv_s":"fast"}`,
		"wrong-version": `{"version":99}`,
		"zero-version":  `{"tdiv_s":0.0005}`,
		"array":         `[1,2,3]`,
		"huge-number":   `{"version":1,"trigger":{"level_code":1e999}}`,
	}
	for name, body := range cases {
		path := filepath.Join(t.TempDir(), name+".json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if s, ok := Load(path, t.Logf); ok {
			t.Errorf("%s: Load accepted corrupt input: %+v", name, s)
		}
	}
}

func TestLoadOversizeRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.json")
	big := []byte(`{"version":1,"pad":"` + strings.Repeat("a", maxFileSize) + `"}`)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load(path, t.Logf); ok {
		t.Fatal("Load accepted an oversized file")
	}
}

// fakeSaver builds a Saver with an injected clock and sink: the debounce
// logic runs against virtual time, no goroutine and no disk.
func fakeSaver(cur *Settings) (*Saver, *[]Settings, *time.Time) {
	saves := &[]Settings{}
	now := time.Unix(1000, 0)
	s := NewSaver("virtual", func() Settings { return *cur }, func(string, ...any) {})
	s.now = func() time.Time { return now }
	s.save = func(_ string, st Settings) error { *saves = append(*saves, st); return nil }
	return s, saves, &now
}

func TestSaverDebounce(t *testing.T) {
	cur := sample()
	s, saves, now := fakeSaver(&cur)

	step := func(advance time.Duration) {
		*now = now.Add(advance)
		s.step()
	}

	step(0) // prime: baseline == live state → never writes
	step(time.Second)
	if len(*saves) != 0 {
		t.Fatalf("saved with no change: %d writes", len(*saves))
	}

	// One change: no write while inside the settle window, one write after.
	cur.ViewMode = 1
	step(time.Second) // observed the change (starts the settle clock)
	step(time.Second) // stable for 1s — still inside the 2s settle
	if len(*saves) != 0 {
		t.Fatalf("saved before the settle window elapsed: %d writes", len(*saves))
	}
	step(time.Second) // stable for 2s → write
	if len(*saves) != 1 || (*saves)[0].ViewMode != 1 {
		t.Fatalf("expected exactly one save of the settled state, got %+v", *saves)
	}

	// Quiet steady state: no rewrites.
	for i := 0; i < 10; i++ {
		step(time.Second)
	}
	if len(*saves) != 1 {
		t.Fatalf("rewrote an unchanged file: %d writes", len(*saves))
	}

	// A burst of changes (knob sweep) coalesces into ONE write, settle-delayed
	// from the LAST change.
	for i := 0; i < 5; i++ {
		cur.Trigger.LevelCode = 28000 + i*100
		step(time.Second) // every poll sees a fresh value → settle keeps restarting
	}
	if len(*saves) != 1 {
		t.Fatalf("saved mid-burst: %d writes", len(*saves))
	}
	step(time.Second)
	step(time.Second) // 2s stable after the last change
	if len(*saves) != 2 || (*saves)[1].Trigger.LevelCode != 28400 {
		t.Fatalf("burst not coalesced to one settled write: %+v", *saves)
	}
}

func TestSaverFlush(t *testing.T) {
	cur := sample()
	s, saves, now := fakeSaver(&cur)
	*now = now.Add(time.Second)
	s.step() // prime

	cur.Acq.Mode = 2
	*now = now.Add(time.Second)
	s.step() // change observed, still inside settle
	if len(*saves) != 0 {
		t.Fatal("saved before settle")
	}
	s.Flush() // shutdown: pending change must not be lost
	if len(*saves) != 1 || (*saves)[0].Acq.Mode != 2 {
		t.Fatalf("Flush did not persist the pending change: %+v", *saves)
	}
	s.Flush() // idempotent: nothing new to write
	if len(*saves) != 1 {
		t.Fatalf("Flush rewrote an unchanged file: %d writes", len(*saves))
	}
}

func TestSaverRetriesAfterFailure(t *testing.T) {
	cur := sample()
	s, _, now := fakeSaver(&cur)
	var fails, oks int
	broken := true
	s.save = func(string, Settings) error {
		if broken {
			fails++
			return os.ErrPermission
		}
		oks++
		return nil
	}
	step := func() { *now = now.Add(time.Second); s.step() }
	step() // prime
	cur.ViewMode = 4
	step()
	step()
	step() // settle elapsed → first attempt fails
	step() // retries while the state stays dirty
	if fails < 2 {
		t.Fatalf("no retry after save failure: fails=%d", fails)
	}
	broken = false
	step()
	if oks != 1 {
		t.Fatalf("save did not recover: oks=%d", oks)
	}
	step()
	if oks != 1 {
		t.Fatalf("rewrote after recovery with no change: oks=%d", oks)
	}
}
