package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/app/internal/iface"
)

func TestTimingFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TimingFileName)
	if _, err := LoadTiming(path); !os.IsNotExist(err) {
		t.Fatalf("missing file: %v", err)
	}
	res := &SweepResult{OK: true, StartRaw: FactoryCS1Timing, ChosenRaw: FactoryCS1Timing.WithRdAccess(10).forCycle(13),
		FloorAccess: 9, FloorCycle: 12, FloorGap: 5, FclkMHz: 99.5, Verify: SettingResult{NsPerWord: 180, WordsPerS: 5.55e6, Words: 409560}}
	res.Chosen = res.ChosenRaw.Fields()
	f, err := FileFromSweep(res, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveTiming(path, f); err != nil {
		t.Fatal(err)
	}
	g, err := LoadTiming(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Chosen != res.ChosenRaw || g.Factory != FactoryCS1Timing || g.FloorCycle != 12 || g.BuildID != "0x"+strings.Repeat("0", 0)+hex8(iface.BuildID) {
		t.Fatalf("round trip: %+v", g)
	}
	if _, err := FileFromSweep(&SweepResult{OK: false}, ""); err == nil {
		t.Fatal("a failed sweep must not be persisted")
	}
	// Corrupt / foreign files are unusable, not fatal.
	os.WriteFile(path, []byte("{\"version\":1,\"chosen\":{\"config5\":0}}"), 0o644)
	if _, err := LoadTiming(path); err == nil || os.IsNotExist(err) {
		t.Fatalf("invalid chosen timing accepted: %v", err)
	}
	os.WriteFile(path, []byte("not json"), 0o644)
	if _, err := LoadTiming(path); err == nil {
		t.Fatal("garbage accepted")
	}
}

func hex8(v uint32) string {
	const d = "0123456789abcdef"
	var b [8]byte
	for i := 7; i >= 0; i-- {
		b[i] = d[v&15]
		v >>= 4
	}
	return string(b[:])
}

func bootFile(t *testing.T, dir string, chosen CS1Timing) string {
	t.Helper()
	path := filepath.Join(dir, TimingFileName)
	f := TimingFile{Version: TimingFileVersion, BuildID: "0x" + hex8(iface.BuildID), Factory: FactoryCS1Timing, Chosen: chosen, Fields: chosen.Fields()}
	if err := SaveTiming(path, f); err != nil {
		t.Fatal(err)
	}
	return path
}

// factoryFile writes the factory record next to the persisted file.
func factoryFile(t *testing.T, timingPath string, fac CS1Timing) {
	t.Helper()
	if _, err := SaveFactory(FactoryPathFor(timingPath), fac, "v"); err != nil {
		t.Fatal(err)
	}
}

func TestBootApplyPersistedPasses(t *testing.T) {
	fab := newFakeFab()
	port := &fakePort{fab: fab, cur: FactoryCS1Timing, minAccess: 9, minCycle: 12}
	chosen := FactoryCS1Timing.WithRdAccess(10).forCycle(13)
	path := bootFile(t, t.TempDir(), chosen)
	factoryFile(t, path, FactoryCS1Timing)
	var logs []string
	bt := applyPersisted(fab, port, path, "v", func(f string, a ...any) { logs = append(logs, f) }, noSleep)
	if bt.Source != "persisted" || bt.Rejected || bt.Captured || port.cur != chosen || bt.Check == nil || !bt.Check.OK {
		t.Fatalf("boot: %+v (cur %s)", bt, port.cur)
	}
	if bt.Check.Passes != bootCheckPasses || bt.Check.RecLen != iface.PretrigMax {
		t.Fatalf("check shape: %+v", bt.Check)
	}
	// The fabric is back in the reset posture for the engine.
	if fab.armed || fab.regs[iface.SelIlCtrl] != 0 || fab.regs[iface.SelDrainLen] != 0 {
		t.Fatalf("fabric not reset after the boot check")
	}
}

func TestBootApplyPersistedFallsBack(t *testing.T) {
	fab := newFakeFab()
	port := &fakePort{fab: fab, cur: FactoryCS1Timing, minAccess: 12, minCycle: 12} // the persisted access time is below this unit's floor
	chosen := FactoryCS1Timing.WithRdAccess(10).forCycle(13)
	path := bootFile(t, t.TempDir(), chosen)
	factoryFile(t, path, FactoryCS1Timing)
	bt := applyPersisted(fab, port, path, "v", nil, noSleep)
	if bt.Source != "factory" || !bt.Rejected || port.cur != FactoryCS1Timing || bt.Check == nil || bt.Check.OK {
		t.Fatalf("fallback: %+v (cur %s)", bt, port.cur)
	}
	if !strings.Contains(bt.Reason, "FAILED") {
		t.Fatalf("reason: %q", bt.Reason)
	}
	if fab.armed || fab.regs[iface.SelIlCtrl] != 0 {
		t.Fatalf("fabric not reset after a failed boot check")
	}
	// Missing persisted file, factory record present: factory, not rejected,
	// no fabric traffic, no write (the controller already holds it).
	fab2 := newFakeFab()
	port2 := &fakePort{fab: fab2, cur: FactoryCS1Timing}
	p := filepath.Join(t.TempDir(), "none.json")
	factoryFile(t, p, FactoryCS1Timing)
	bt = applyPersisted(fab2, port2, p, "v", nil, noSleep)
	if bt.Source != "factory" || bt.Rejected || bt.Captured || port2.applies != 0 || fab2.goCount != 0 {
		t.Fatalf("missing file: %+v applies=%d go=%d", bt, port2.applies, fab2.goCount)
	}
	// Corrupt persisted file: factory, rejected, no fabric traffic.
	p = filepath.Join(t.TempDir(), TimingFileName)
	factoryFile(t, p, FactoryCS1Timing)
	os.WriteFile(p, []byte("{}"), 0o644)
	bt = applyPersisted(fab2, port2, p, "v", nil, noSleep)
	if bt.Source != "factory" || !bt.Rejected || port2.applies != 0 || fab2.goCount != 0 {
		t.Fatalf("corrupt file: %+v", bt)
	}
	// A persisted timing the validator refuses (hand-edited) never reaches the controller.
	p = bootFile(t, t.TempDir(), FactoryCS1Timing.WithRdAccess(11).forCycle(13))
	factoryFile(t, p, FactoryCS1Timing)
	port2.minAccess = 0
	bt = applyPersisted(fab2, port2, p, "v", nil, noSleep)
	if bt.Source != "persisted" {
		t.Fatalf("valid persisted timing rejected: %+v", bt)
	}
}

// TestFactoryRecordRules is the factory-file state machine of the file
// header: captured once from the controller on the first run without a
// persisted file, then always the file — never the live controller.
func TestFactoryRecordRules(t *testing.T) {
	logf := func(f string, a ...any) { t.Logf(f, a...) }
	// (a) First run, no files: the controller's values are recorded and nothing is written to the controller.
	dir := t.TempDir()
	path := filepath.Join(dir, TimingFileName)
	fab := newFakeFab()
	port := &fakePort{fab: fab, cur: FactoryCS1Timing}
	bt := applyPersisted(fab, port, path, "v1", logf, noSleep)
	if bt.Source != "factory" || !bt.Captured || bt.Rejected || port.applies != 0 || fab.goCount != 0 {
		t.Fatalf("first run: %+v applies=%d", bt, port.applies)
	}
	ff, err := LoadFactory(FactoryPathFor(path))
	if err != nil || ff.Timing != FactoryCS1Timing || ff.Note != "" || ff.AppVersion != "v1" || bt.FactoryPath != filepath.Join(dir, FactoryFileName) {
		t.Fatalf("factory record: %+v %v (%s)", ff, err, bt.FactoryPath)
	}
	// A second run without a persisted file leaves the record alone even when
	// the controller holds something else (a crash mid-sweep): the controller
	// is put back to the record's values.
	odd := FactoryCS1Timing.WithRdAccess(9).forCycle(12)
	port.cur = odd
	bt = applyPersisted(fab, port, path, "v2", logf, noSleep)
	if bt.Source != "factory" || bt.Captured || bt.Rejected || port.cur != FactoryCS1Timing || port.applies != 1 {
		t.Fatalf("second run: %+v cur=%s applies=%d", bt, port.cur, port.applies)
	}
	if ff2, _ := LoadFactory(FactoryPathFor(path)); ff2 != ff {
		t.Fatalf("factory record rewritten: %+v", ff2)
	}

	// (b) A persisted file with no factory record: refused, the controller is
	// left exactly as found, no fabric traffic, and nothing is captured.
	dir = t.TempDir()
	path = bootFile(t, dir, FactoryCS1Timing.WithRdAccess(10).forCycle(13))
	fab = newFakeFab()
	port = &fakePort{fab: fab, cur: odd}
	bt = applyPersisted(fab, port, path, "v", logf, noSleep)
	if bt.Source != "current" || !bt.Rejected || bt.Captured || port.applies != 0 || fab.goCount != 0 || port.cur != odd {
		t.Fatalf("no factory record: %+v applies=%d go=%d", bt, port.applies, fab.goCount)
	}
	if _, err := LoadFactory(FactoryPathFor(path)); !os.IsNotExist(err) {
		t.Fatalf("factory record must not be captured while a persisted file exists: %v", err)
	}
	if !strings.Contains(bt.Reason, "NOT applied") || bt.Applied != odd.Fields() {
		t.Fatalf("reason/applied: %+v", bt)
	}

	// (c) In-place restart with the persisted timing still in the controller
	// and a failing check: the fallback is the FILE's timing, not the
	// controller's boot-time values.
	dir = t.TempDir()
	chosen := FactoryCS1Timing.WithRdAccess(10).forCycle(13)
	path = bootFile(t, dir, chosen)
	factoryFile(t, path, FactoryCS1Timing)
	fab = newFakeFab()
	port = &fakePort{fab: fab, cur: chosen, minAccess: 12}
	bt = applyPersisted(fab, port, path, "v", logf, noSleep)
	if bt.Source != "factory" || !bt.Rejected || port.cur != FactoryCS1Timing || bt.Factory != FactoryCS1Timing.Fields() {
		t.Fatalf("in-place restart fallback: %+v cur=%s", bt, port.cur)
	}
	// ... and with a passing check the persisted timing is in force, the
	// factory record untouched.
	port.minAccess = 0
	bt = applyPersisted(fab, port, path, "v", logf, noSleep)
	if bt.Source != "persisted" || port.cur != chosen {
		t.Fatalf("in-place restart pass: %+v", bt)
	}
	// (d) The exit path restores the file's values; without a record it refuses.
	got, err := RestoreFactoryTiming(port, path, logf)
	if err != nil || got != FactoryCS1Timing || port.cur != FactoryCS1Timing {
		t.Fatalf("restore factory: %v %s cur=%s", err, got, port.cur)
	}
	n := port.applies
	if _, err := RestoreFactoryTiming(port, path, logf); err != nil || port.applies != n {
		t.Fatalf("restore when already at the factory timing must not write: %v %d", err, port.applies-n)
	}
	if _, err := RestoreFactoryTiming(port, filepath.Join(t.TempDir(), TimingFileName), logf); err == nil {
		t.Fatal("restore without a factory record must refuse")
	}
	// (e) Forget: the persisted file goes, the record stays, the factory timing is in force.
	port.cur = chosen
	if err := ForgetTiming(port, path, logf); err != nil || port.cur != FactoryCS1Timing {
		t.Fatalf("forget: %v cur=%s", err, port.cur)
	}
	if _, err := LoadTiming(path); !os.IsNotExist(err) {
		t.Fatalf("persisted file still there: %v", err)
	}
	if _, err := LoadFactory(FactoryPathFor(path)); err != nil {
		t.Fatalf("factory record gone: %v", err)
	}
	// (f) A capture that differs from the documented bootloader timing is noted, not refused.
	dir = t.TempDir()
	path = filepath.Join(dir, TimingFileName)
	port = &fakePort{fab: newFakeFab(), cur: FactoryCS1Timing.WithRdAccess(12)}
	bt = applyPersisted(port.fab, port, path, "v", logf, noSleep)
	ff, err = LoadFactory(FactoryPathFor(path))
	if err != nil || !bt.Captured || ff.Timing.RdAccess() != 12 || ff.Note == "" {
		t.Fatalf("noted capture: %+v %v", ff, err)
	}
	// (g) A corrupt factory record with a persisted file present is a refusal too.
	os.WriteFile(FactoryPathFor(path), []byte("garbage"), 0o644)
	bootFile(t, dir, chosen)
	bt = applyPersisted(port.fab, port, path, "v", logf, noSleep)
	if bt.Source != "current" || !bt.Rejected {
		t.Fatalf("corrupt factory record: %+v", bt)
	}
}

// TestTimingPathForBootFallsBackToSibling: a deploy lands the app in the other
// OTA slot, where no timing record exists, and the board's measured timing must
// not be lost. The sibling is used only when it carries a factory record too.
func TestTimingPathForBootFallsBackToSibling(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "A")
	b := filepath.Join(root, "B")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	own := filepath.Join(b, TimingFileName)
	t.Setenv("SCOPE_GPMC_TIMING", own)

	if got := TimingPathForBoot(); got != own {
		t.Errorf("no records anywhere: got %q, want the own path %q", got, own)
	}

	sib := filepath.Join(a, TimingFileName)
	if err := os.WriteFile(sib, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := TimingPathForBoot(); got != own {
		t.Errorf("sibling has no factory record beside it, so it must be refused: got %q", got)
	}

	if err := os.WriteFile(FactoryPathFor(sib), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := TimingPathForBoot(); got != sib {
		t.Errorf("sibling with a factory record: got %q, want %q", got, sib)
	}

	if err := os.WriteFile(own, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := TimingPathForBoot(); got != own {
		t.Errorf("our own record must win once it exists: got %q, want %q", got, own)
	}
}
