// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS

package settings_test

import (
	"bytes"
	"flag"
	core "github.com/labeth/open-dso-core/settings"
	"os"
	"path/filepath"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/settings"
)

var update = flag.Bool("update", false, "rewrite the settings file goldens")

// fullSetup is a setup with every block set: the file it makes is the
// file's whole format.
// TRLC-LINKS: REQ-SDS-089
func fullSetup() settings.Settings {
	return settings.Settings{
		Version: settings.Version,
		TdivS:   1e-3,
		Ch: [2]settings.Channel{
			{VdivV: 0.5, OffsetV: 1.5, OffsetSet: true, Coupling: 1, Probe: 10, BWL: true},
			{VdivV: 2, OffsetV: -2.5, OffsetSet: true, Coupling: 2, Probe: 100},
		},
		VertSet:  true,
		Trigger:  settings.Trigger{Qualifiers: engine.TrigQual{PulseLvl: .4, PulseMinNs: 105000, PulseMaxNs: 120000, PulseCond: 3, SlopeLo: .3, SlopeHi: .7, SlopeMinNs: 100, SlopeMaxNs: 1000, VideoStd: 1, VideoLine: 42}, LevelCode: 30500, Source: 1, Type: 2, Norm: true, HoldoffS: 1e-3, PosFrac: 0.3},
		Acq:      settings.Acq{Mode: 1, AvgCount: 64, EresLen: 1, MemDepth: 16384, PrecisionRateHz: 15625000},
		Decode:   settings.Decode{Proto: 4, Baud: 9600, ChA: 1, CPOL: true, CPHA: true, Format: 1},
		ViewMode: 4,
		UI: settings.UI{
			Set: true, Workspace: 2, Views: [5]int{1, 0, 4, 0, 0}, BusEye: true, MeasAll: true,
			Meas: [12]int{1*32 + 0 + 1, 0*32 + 1 + 1}, FFTSource: 1, MasterDepth: 128, RemoteNotices: true,
			MaskFrames: 64, MaskTol: 3, SrCh: 2, SrGrid: 16, SrStop: 3, SrTarget: 120,
		},
		Serial: settings.Serial{Set: true, Match: "55 AA", Addr: -1, RW: 2},
		Screen: settings.Screen{Set: true, SaverMin: 5, OffMin: 120},
		Display: settings.Display{
			Set: true, CurMode: 2, CurX: [2]float64{0.25, 0.75}, CurY: [2]float64{0.4, 0.55},
			CurSel: 1, Zoom: 10, ZoomOff: -0.2,
		},
		Bode: settings.Bode{Set: true, Ref: 1, Dut: 0},
		Sound: settings.Sound{
			Set: true, A4: 442, Ch: 1, Ref: "euro", Range: "bass", Slow: true, Page: 3, Cycles: 4, ABIn: 1,
			EnvCV: true, GateV: 1.5, VOctAuto: true, PreS: 2.5, PostS: 6, TrigGate: true,
		},
	}
}

// golden compares got with testdata/name, or rewrites it with -update.
// TRLC-LINKS: REQ-SDS-089
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s changed:\n got %s\nwant %s", name, got, want)
	}
}

// The settings file, a setup slot and a named setup are the bytes they
// have always been: a setup restored through the scope's setters and
// collected again is saved as the golden file, level_code and all
// (ADR-HAL-SETTINGS-FORMAT-KEPT).
// TRLC-LINKS: REQ-SDS-089, REQ-SDS-072
func TestSettingsFileGolden(t *testing.T) {
	e, fe, pc, _ := rig(t)
	x := ext(e, fe)
	core.Apply(fullSetup(), e, fe, pc, x, t.Logf)
	collect := func() settings.Settings { return core.Collect(e, fe, pc, x) }

	dir := t.TempDir()
	path := settings.PathIn(dir)
	if err := core.SaveFile(path, collect()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "scope-settings.json.golden", raw)

	slots := core.Slots{KV: settings.Storage{Dir: dir}.Settings(), Key: settings.SlotKey, Collect: collect}
	if err := slots.SaveSetup(3); err != nil {
		t.Fatal(err)
	}
	if err := slots.SaveSetupFile("bench.json"); err != nil {
		t.Fatal(err)
	}
	for file, g := range map[string]string{"setup03.json": "setup03.json.golden", "bench.json": "setup03.json.golden"} {
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		golden(t, g, raw)
	}
	// The golden file loads back to the setup it was saved from.
	got, ok := core.LoadFile(path, t.Logf)
	if !ok || got != collect() {
		t.Fatalf("the golden file does not load back: %v\n got %+v\nwant %+v", ok, got, collect())
	}
}
