// Package settings persists the user-facing instrument setup across app
// restarts, the way a real scope restores its last setup at power-on: the
// timebase, vertical (V/div, offset, coupling, probe), trigger (level/slope/
// source/type/mode/holdoff), acquisition mode, the device decode setup and the
// view mode.
//
// The snapshot is one small human-readable JSON file on the U-disk (the only
// persistent writable store on this unit — /tmp is read-only and the internal
// firmware paths are not ours to write; the boot anchor even force-remounts
// the stick rw, and the OTA agent already keeps state.json + logs there).
// Saves are debounced and atomic (temp + rename); the loader falls back to
// defaults on ANY error — a corrupt or hostile file must never prevent boot.
// Restores go through the SAME setter paths the panel/web/SCPI use, so every
// clamp and side-effect applies.
// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	core "github.com/labeth/open-dso-core/settings"
)

// The setup's shape is the core's; this package loads, saves, collects and
// applies it for this scope.
const Version = core.Version

// maxFileSize caps what the loader will even parse: a real settings file is
// well under 1 KB; anything huge is not ours (and must not balloon the heap
// on a 64 MB-class ARM).
const maxFileSize = 64 << 10

type (
	Channel  = core.Channel
	Trigger  = core.Trigger
	Acq      = core.Acq
	Decode   = core.Decode
	Settings = core.Settings
	Bode     = core.Bode
	Sound    = core.Sound
	Screen   = core.Screen
	Serial   = core.Serial
	UI       = core.UI
	Display  = core.Display
)

// PathIn is the settings file in the stick's ota/ directory (OTA_DIR, which
// startup.sh exports and the agent keeps its state and logs in): writable,
// and kept across app slot switches.
// TRLC-LINKS: REQ-SDS-089
func PathIn(otaDir string) string { return filepath.Join(otaDir, "scope-settings.json") }

// Parse decodes and validates one settings blob. It never panics on hostile
// input; any structural problem is an error (the caller falls back to
// defaults). Value-range problems are NOT errors — Apply routes every value
// through the owning setter, which clamps.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func Parse(raw []byte) (Settings, error) {
	if len(raw) > maxFileSize {
		return Settings{}, fmt.Errorf("settings: file too large (%d bytes)", len(raw))
	}
	var s Settings
	if err := json.Unmarshal(bytes.TrimSpace(raw), &s); err != nil {
		return Settings{}, fmt.Errorf("settings: %w", err)
	}
	if s.Version != Version {
		// Future versions add migrations here, keyed on s.Version.
		return Settings{}, fmt.Errorf("settings: unsupported schema version %d", s.Version)
	}
	return s, nil
}

// Load reads the settings file. ok=false means "no restore" (missing file,
// unreadable, corrupt, wrong version): the scope boots with defaults exactly
// as before this feature existed. Never fatal, never a panic.
// TRLC-LINKS: REQ-SDS-089
func Load(path string, logf func(string, ...any)) (Settings, bool) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the app's own setup file
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logf("settings: read %s: %v — using defaults", path, err)
		}
		return Settings{}, false
	}
	s, err := Parse(raw)
	if err != nil {
		logf("settings: %s: %v — using defaults", path, err)
		return Settings{}, false
	}
	return s, true
}

// Save writes the settings atomically: temp file in the same directory,
// fsync, rename. A power cut mid-save leaves either the old file or the new
// one, never a torn read (as atomic as the stick's FAT allows — the same
// discipline the health token and the agent's state.json use).
// TRLC-LINKS: REQ-SDS-089
func Save(path string, s Settings) error {
	s.Version = Version
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	// A file that already holds this setup is left as it is: a write, an
	// fsync and a rename on the stick cost milliseconds and wear it.
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) { //nolint:gosec // G304: the app's own setup file
		return nil
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) //nolint:gosec // G302: the setup file is the owner's to copy off the stick
	if err != nil {
		return err
	}
	// On a failure the temporary file is cleaned up as far as it can be; the
	// error returned is the one that stopped the save.
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
