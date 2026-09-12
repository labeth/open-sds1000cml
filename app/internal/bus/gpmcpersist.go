// Persistence of the swept GPMC CS1 timing, the factory-timing record and
// the boot-time gate.
//
// Two small JSON files live next to the app binary on the U-disk (an app
// slot: reboot-recoverable, gone with the slot — a new deployment starts
// over; SCOPE_GPMC_TIMING overrides the persisted-timing path for bench
// runs, the factory file always sits in the same directory):
//
//   - gpmc-timing.json  (TimingFile)  — the sweep's chosen timing.
//   - gpmc-factory.json (FactoryFile) — the bootloader's CS1 timing as the
//     controller held it the FIRST time this app ran with no persisted
//     timing file present (then the controller can only hold the
//     bootloader's values plus the shipped cycle gap of EnableEDMA). It is
//     captured once and never rewritten from the live controller: after an
//     in-place restart (update-app, activate, crash relaunch) the controller
//     may hold a persisted timing, and a "factory" read then would be the
//     persisted timing itself.
//
// Boot state machine (ApplyPersistedTiming, after the fabric identity check
// at whatever timing the controller holds, after EnableEDMA, before the
// engine owns the bus):
//
//  1. cur = the controller's CONFIG1..7.
//  2. factory file
//     missing/corrupt, no persisted file  → capture: factory = cur, write the file
//     missing/corrupt, persisted file     → REFUSE the persisted timing; the
//     controller stays at cur ("current":
//     the best known state); log
//     present                             → factory = the file's values
//  3. persisted file
//     missing              → controller := factory (restored when cur differs)
//     corrupt/invalid      → controller := factory; rejected, logged
//     present              → apply it, run the TSRC ramp check at it
//     (RampCheck: one record, several REWIND
//     re-drains, pop-counter delta, no underrun,
//     no break, byte-identical)
//     pass → "persisted"
//     fail → controller := factory; rejected, logged
//
// Exit (RestoreFactoryTiming, from main's SIGTERM/exit path and the diag's
// "apply factory"): the factory file's values go back into the controller,
// so the relaunched app (and the OTA agent's rollback) always meets the
// bootloader's timing. A rejected persisted file is left where it is: the
// check costs ~50 ms per boot and a sweep on the new build replaces it.
package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"open-sds/app/internal/iface"
)

// TimingFileName is the persisted-timing file next to the app.
const TimingFileName = "gpmc-timing.json"

// FactoryFileName is the factory-timing record next to the persisted file.
const FactoryFileName = "gpmc-factory.json"

// TimingFileVersion is the file schema (both files).
const TimingFileVersion = 1

// TimingFile is the persisted sweep outcome.
type TimingFile struct {
	Version     int          `json:"version"`
	Saved       string       `json:"saved"`
	BuildID     string       `json:"build_id"` // the fabric the sweep ran against (informational: the boot check is the gate)
	AppVersion  string       `json:"app_version,omitempty"`
	Factory     CS1Timing    `json:"factory"` // the timing the sweep started from (informational; the factory file is the record)
	Chosen      CS1Timing    `json:"chosen"`
	Fields      TimingFields `json:"fields"` // decoded Chosen (human readable)
	FloorAccess uint32       `json:"floor_access"`
	FloorCycle  uint32       `json:"floor_cycle"`
	FloorGap    uint32       `json:"floor_gap"`
	NsPerWord   float64      `json:"ns_per_word"`
	WordsPerS   float64      `json:"words_per_s"`
	FclkMHz     float64      `json:"fclk_mhz"`
	VerifyWords int          `json:"verify_words"`
	FastDrain   bool         `json:"fast_drain"`
}

// FactoryFile is the captured bootloader timing.
type FactoryFile struct {
	Version    int          `json:"version"`
	Saved      string       `json:"saved"`
	BuildID    string       `json:"build_id"` // the app's fabric when captured (informational)
	AppVersion string       `json:"app_version,omitempty"`
	Timing     CS1Timing    `json:"timing"`
	Fields     TimingFields `json:"fields"`
	Note       string       `json:"note,omitempty"` // set when the capture differs from the documented bootloader timing
}

// TimingPath resolves the persisted-timing file: SCOPE_GPMC_TIMING, else next
// to the executable.
func TimingPath() string {
	if p := os.Getenv("SCOPE_GPMC_TIMING"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), TimingFileName)
	}
	return TimingFileName
}

// TimingPathForBoot is TimingPath, except that when this slot has no persisted
// record it falls back to the newest one in a sibling slot.
//
// The persisted file lives next to the executable, which on the instrument is
// the OTA slot directory, so a deploy into the other slot silently boots the
// factory timing again and gives back the 1.78x the sweep won. The timing is a
// property of the BOARD, not of the app build, so a sibling's record is the
// right thing to use — and it is not trusted blindly either way: the boot path
// ramp-checks whatever it applies and falls back to factory if the check fails.
// A sibling is used only if it carries its own factory record beside it, which
// is the same safety rule an own-slot record has to meet.
func TimingPathForBoot() string {
	own := TimingPath()
	if _, err := os.Stat(own); err == nil {
		return own
	}
	dir := filepath.Dir(own)
	sibs, err := filepath.Glob(filepath.Join(filepath.Dir(dir), "*", TimingFileName))
	if err != nil {
		return own
	}
	best, bestMod := "", time.Time{}
	for _, c := range sibs {
		if c == own {
			continue
		}
		st, err := os.Stat(c)
		if err != nil {
			continue
		}
		if _, err := os.Stat(FactoryPathFor(c)); err != nil {
			continue // no factory record beside it: the safety rule refuses it
		}
		if st.ModTime().After(bestMod) {
			best, bestMod = c, st.ModTime()
		}
	}
	if best != "" {
		return best
	}
	return own
}

// FactoryPathFor is the factory file next to the persisted-timing file.
func FactoryPathFor(timingPath string) string {
	return filepath.Join(filepath.Dir(timingPath), FactoryFileName)
}

// FileFromSweep builds the persisted record from a passed sweep.
func FileFromSweep(r *SweepResult, appVersion string) (TimingFile, error) {
	if r == nil || !r.OK {
		return TimingFile{}, fmt.Errorf("gpmc timing: the sweep did not pass — nothing to persist")
	}
	if err := r.ChosenRaw.Validate(); err != nil {
		return TimingFile{}, err
	}
	return TimingFile{
		Version: TimingFileVersion, Saved: time.Now().UTC().Format(time.RFC3339),
		BuildID: fmt.Sprintf("0x%08x", iface.BuildID), AppVersion: appVersion,
		Factory: r.StartRaw, Chosen: r.ChosenRaw, Fields: r.Chosen,
		FloorAccess: r.FloorAccess, FloorCycle: r.FloorCycle, FloorGap: r.FloorGap,
		NsPerWord: r.Verify.NsPerWord, WordsPerS: r.Verify.WordsPerS, FclkMHz: r.FclkMHz,
		VerifyWords: r.Verify.Words, FastDrain: r.FastDrain,
	}, nil
}

// writeJSONAtomic writes v as indented JSON through temp + rename.
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readJSON reads a small JSON file. os.IsNotExist(err) means "no file".
func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) > 64<<10 {
		return fmt.Errorf("gpmc timing: %s is %d bytes — not ours", path, len(raw))
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("gpmc timing: %s: %w", path, err)
	}
	return nil
}

// SaveTiming writes the persisted-timing file atomically (temp + rename).
func SaveTiming(path string, f TimingFile) error {
	if err := f.Chosen.Validate(); err != nil {
		return err
	}
	return writeJSONAtomic(path, f)
}

// LoadTiming reads and validates the persisted-timing file.
// os.IsNotExist(err) means "never swept"; any other error means the file is
// unusable.
func LoadTiming(path string) (TimingFile, error) {
	var f TimingFile
	if err := readJSON(path, &f); err != nil {
		return TimingFile{}, err
	}
	if f.Version != TimingFileVersion {
		return TimingFile{}, fmt.Errorf("gpmc timing: %s: version %d, want %d", path, f.Version, TimingFileVersion)
	}
	if err := f.Chosen.Validate(); err != nil {
		return TimingFile{}, fmt.Errorf("gpmc timing: %s: %w", path, err)
	}
	return f, nil
}

// SaveFactory records t as the factory timing (the one-time capture).
func SaveFactory(path string, t CS1Timing, appVersion string) (FactoryFile, error) {
	f := FactoryFile{Version: TimingFileVersion, Saved: time.Now().UTC().Format(time.RFC3339),
		BuildID: fmt.Sprintf("0x%08x", iface.BuildID), AppVersion: appVersion, Timing: t, Fields: t.Fields()}
	if t.Config5 == 0 || t.RdCycle() == 0 {
		return f, fmt.Errorf("gpmc timing: refusing to record an empty factory timing (%s)", t)
	}
	if doc := FactoryCS1Timing; t.RdCycle() != doc.RdCycle() || t.RdAccess() != doc.RdAccess() ||
		t.OEOn() != doc.OEOn() || t.OEOff() != doc.OEOff() || t.CSRdOff() != doc.CSRdOff() {
		f.Note = fmt.Sprintf("differs from the documented bootloader timing (%s; fpga-specs 10 §4.3)", doc)
	}
	if err := writeJSONAtomic(path, f); err != nil {
		return f, err
	}
	return f, nil
}

// LoadFactory reads the factory file. os.IsNotExist(err) means "never
// captured"; any other error means the file is unusable.
func LoadFactory(path string) (FactoryFile, error) {
	var f FactoryFile
	if err := readJSON(path, &f); err != nil {
		return FactoryFile{}, err
	}
	if f.Version != TimingFileVersion {
		return FactoryFile{}, fmt.Errorf("gpmc timing: %s: version %d, want %d", path, f.Version, TimingFileVersion)
	}
	if f.Timing.Config5 == 0 || f.Timing.RdCycle() == 0 {
		return FactoryFile{}, fmt.Errorf("gpmc timing: %s: empty factory timing", path)
	}
	return f, nil
}

// sameReadSide reports whether the four words the sweep may change agree.
func sameReadSide(a, b CS1Timing) bool {
	return a.Config2 == b.Config2 && a.Config4 == b.Config4 && a.Config5 == b.Config5 && a.Config6 == b.Config6
}

// BootTiming is what ApplyPersistedTiming decided, for the log and /api/diag.
type BootTiming struct {
	Path        string       `json:"path"`
	FactoryPath string       `json:"factory_path"`
	Source      string       `json:"source"` // "factory" | "persisted" | "current" (no factory record: left as found)
	Factory     TimingFields `json:"factory"`
	Applied     TimingFields `json:"applied"`
	Captured    bool         `json:"factory_captured"` // this boot wrote the factory file
	Rejected    bool         `json:"rejected"`         // a persisted timing existed and was not applied
	Reason      string       `json:"reason,omitempty"`
	Check       *RampReport  `json:"check,omitempty"`
	BuildID     string       `json:"file_build_id,omitempty"`
}

// bootCheckPasses is the number of REWIND re-drains of the boot-time check
// (3 × 20 478 words ≈ 60 k words, ~50 ms at the factory timing).
const bootCheckPasses = 3

// ApplyPersistedTiming is the boot hook: the state machine of the file
// header. path is the persisted-timing file; the factory file sits next to
// it. The fabric is left in the reset posture. Never fatal.
func ApplyPersistedTiming(b Bus, port TimingPort, path, appVersion string, logf func(string, ...any)) BootTiming {
	return applyPersisted(b, port, path, appVersion, logf, time.Sleep)
}

func applyPersisted(b Bus, port TimingPort, path, appVersion string, logf func(string, ...any), sleep func(time.Duration)) BootTiming {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	fpath := FactoryPathFor(path)
	bt := BootTiming{Path: path, FactoryPath: fpath, Source: "current"}
	cur, err := port.Read()
	if err != nil {
		bt.Reason = fmt.Sprintf("read CS1 timing: %v", err)
		logf("gpmc timing: %s — controller left as found", bt.Reason)
		return bt
	}
	bt.Applied = cur.Fields()

	// 2. the factory record
	pf, perr := LoadTiming(path)
	ff, ferr := LoadFactory(fpath)
	if ferr != nil {
		if !os.IsNotExist(ferr) {
			logf("gpmc timing: factory record unusable: %v", ferr)
		}
		if perr != nil && os.IsNotExist(perr) {
			// No persisted timing has ever been applied on this slot: the
			// controller holds the bootloader's values. Capture them once.
			f, err := SaveFactory(fpath, cur, appVersion)
			if err != nil {
				bt.Reason = fmt.Sprintf("record factory timing: %v", err)
				logf("gpmc timing: %s — controller left as found (%s); no persisted timing will be applied until it exists", bt.Reason, cur)
				return bt
			}
			ff, bt.Captured = f, true
			logf("gpmc timing: factory timing %s recorded in %s (first run without %s)%s", cur, fpath, path,
				map[bool]string{true: " — NOTE " + f.Note, false: ""}[f.Note != ""])
		} else {
			// A persisted file exists but nothing says what the bootloader's
			// timing was: whatever the controller holds now (this timing
			// passed the fabric identity check) is the safest state there is.
			bt.Rejected = true
			bt.Reason = fmt.Sprintf("no factory record %s — the persisted timing in %s is NOT applied; controller stays at %s (delete the persisted file, power cycle, restart to record the factory timing)", fpath, path, cur)
			logf("gpmc timing: %s", bt.Reason)
			return bt
		}
	}
	factory := ff.Timing
	bt.Factory, bt.Source = factory.Fields(), "factory"
	toFactory := func(reason string) BootTiming {
		if reason != "" {
			bt.Rejected, bt.Reason = true, reason
		}
		if sameReadSide(cur, factory) {
			bt.Applied = factory.Fields()
			if reason != "" {
				logf("gpmc timing: %s — factory timing stays (%s)", reason, factory)
			}
			return bt
		}
		if err := port.Restore(factory); err != nil {
			logf("gpmc timing: RESTORING FACTORY TIMING FAILED: %v", err)
			bt.Reason += "; restore failed: " + err.Error()
			return bt
		}
		bt.Applied = factory.Fields()
		if reason == "" {
			reason = fmt.Sprintf("controller held %s", cur)
		}
		logf("gpmc timing: %s — factory timing restored (%s)", reason, factory)
		return bt
	}

	// 3. the persisted timing
	if perr != nil {
		if os.IsNotExist(perr) {
			logf("gpmc timing: no %s — factory timing (%s); run tools/hw/gpmc_sweep.sh to sweep", path, factory)
			return toFactory("")
		}
		return toFactory(perr.Error())
	}
	bt.BuildID = pf.BuildID
	if want := fmt.Sprintf("0x%08x", iface.BuildID); pf.BuildID != want {
		logf("gpmc timing: %s was swept on fabric %s, this app is %s — the boot check decides", path, pf.BuildID, want)
	}
	if err := port.Apply(pf.Chosen); err != nil {
		return toFactory(fmt.Sprintf("apply persisted timing %s: %v", pf.Chosen, err))
	}
	cur = pf.Chosen
	rep, err := RampCheck(b, TsrcOptions{}, bootCheckPasses, sleep)
	if rerr := ResetTsrc(b); rerr != nil {
		logf("gpmc timing: reset after the boot check: %v", rerr)
	}
	if err != nil {
		return toFactory(fmt.Sprintf("boot ramp check could not run at %s: %v", pf.Chosen, err))
	}
	bt.Check = &rep
	if !rep.OK {
		return toFactory(fmt.Sprintf("boot ramp check FAILED at %s: %d bad drains, %d breaks, %d pop mismatches, %d underruns, %d mismatches over %d words",
			pf.Chosen, rep.Bad, rep.Breaks, rep.PopsBad, rep.Underrun, rep.Mismatch, rep.Words))
	}
	bt.Source, bt.Applied = "persisted", pf.Chosen.Fields()
	logf("gpmc timing: persisted %s applied — boot ramp check passed (%d words, %.0f ns/word, %.2f Mw/s); factory is %s",
		pf.Chosen, rep.Words, rep.NsPerW, rep.WordsPerS/1e6, factory)
	return bt
}

// RestoreFactoryTiming puts the factory file's timing back into the
// controller (the exit / SIGTERM path, and the diag's "factory" apply). It
// never reads the "factory" from the live controller. Returns the timing
// restored.
func RestoreFactoryTiming(port TimingPort, path string, logf func(string, ...any)) (CS1Timing, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	fpath := FactoryPathFor(path)
	ff, err := LoadFactory(fpath)
	if err != nil {
		if os.IsNotExist(err) {
			err = fmt.Errorf("gpmc timing: no factory record %s — cannot restore the factory timing", fpath)
		}
		logf("%v", err)
		return CS1Timing{}, err
	}
	cur, rerr := port.Read()
	if rerr == nil && sameReadSide(cur, ff.Timing) {
		return ff.Timing, nil
	}
	if err := port.Restore(ff.Timing); err != nil {
		logf("gpmc timing: RESTORING FACTORY TIMING FAILED: %v", err)
		return ff.Timing, err
	}
	logf("gpmc timing: factory timing restored (%s; was %s)", ff.Timing, cur)
	return ff.Timing, nil
}

// ForgetTiming removes the persisted-timing file (the factory record stays)
// and restores the factory timing. The next boot then runs at the factory
// timing until a new sweep is persisted.
func ForgetTiming(port TimingPort, path string, logf func(string, ...any)) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if logf != nil {
		logf("gpmc timing: %s removed", path)
	}
	_, err := RestoreFactoryTiming(port, path, logf)
	return err
}
