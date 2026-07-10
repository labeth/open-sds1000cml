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
package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Version is the schema version written to (and required from) the file.
// Bump it and add a migration in Parse when the shape changes.
const Version = 1

// maxFileSize caps what the loader will even parse: a real settings file is
// well under 1 KB; anything huge is not ours (and must not balloon the heap
// on a 64 MB-class ARM).
const maxFileSize = 64 << 10

// Channel is one vertical channel's persisted setup.
type Channel struct {
	VdivV     float64 `json:"vdiv_v"`     // volts/div (must be a ladder detent to restore)
	OffsetV   float64 `json:"offset_v"`   // input-referred offset volts
	OffsetSet bool    `json:"offset_set"` // false = boot-inherited offset untouched (never restore 0 V over it)
	Coupling  int     `json:"coupling"`   // 0=DC 1=AC 2=GND (analog.Cpl*)
	Probe     float64 `json:"probe"`      // probe attenuation: 1, 10 or 100
}

// Trigger is the persisted trigger setup.
type Trigger struct {
	LevelCode int     `json:"level_code"` // trigger-level DAC code; 0 = boot comparator untouched
	Rising    bool    `json:"rising"`     // edge slope
	Source    int     `json:"source"`     // 0=C1 1=C2
	Type      int     `json:"type"`       // 0=edge 1=pulse 2=slope 3=video
	Norm      bool    `json:"norm"`       // trigger mode: true=NORM false=AUTO
	HoldoffS  float64 `json:"holdoff_s"`  // 0 = off
}

// Acq is the persisted acquisition mode.
type Acq struct {
	Mode     int `json:"mode"` // 0=normal 1=average 2=eres 3=peak
	AvgCount int `json:"avg_count"`
	EresLen  int `json:"eres_len"`
}

// Decode is the device protocol-decode setup (controller-owned; historically
// reset to Off on every app restart).
type Decode struct {
	Proto  int  `json:"proto"` // 0=off 1=auto 2=uart 3=i2c 4=spi
	Baud   int  `json:"baud"`
	ChA    int  `json:"cha"` // UART source; I2C SCL; SPI CLK (0=C1 1=C2)
	ChB    int  `json:"chb"` // I2C SDA; SPI DATA
	CPOL   bool `json:"cpol"`
	CPHA   bool `json:"cpha"`
	Format int  `json:"format"` // 0=hex 1=ascii 2=both
}

// Settings is the whole persisted setup. Every field is comparable, so the
// saver detects change with plain ==.
type Settings struct {
	Version  int        `json:"version"`
	TdivS    float64    `json:"tdiv_s"`
	Ch       [2]Channel `json:"ch"`
	VertSet  bool       `json:"vert_set"` // vertical front end was driven; false = keep the seed-don't-emit boot rule
	Trigger  Trigger    `json:"trigger"`
	Acq      Acq        `json:"acq"`
	Decode   Decode     `json:"decode"`
	ViewMode int        `json:"view_mode"` // 0=Y-T 1=X-Y 2=FFT 3=Bode 4=Spgm
}

// DefaultPath resolves where the settings file lives.
//
//   - SCOPE_SETTINGS: explicit override (tests, bench runs).
//   - $OTA_USB/scope-settings.json: the U-disk root, when the boot anchor
//     exported it.
//   - $OTA_DIR/scope-settings.json: the stick's ota/ dir — ALWAYS exported by
//     startup.sh and proven writable (the agent keeps state.json + logs/
//     there), and it survives app slot switches. This is the path used on the
//     device.
//   - next to the executable (an app slot on the U-disk when agent-launched;
//     the build dir on a dev box).
func DefaultPath() string {
	if p := os.Getenv("SCOPE_SETTINGS"); p != "" {
		return p
	}
	const name = "scope-settings.json"
	if d := os.Getenv("OTA_USB"); d != "" {
		return filepath.Join(d, name)
	}
	if d := os.Getenv("OTA_DIR"); d != "" {
		return filepath.Join(d, name)
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), name)
	}
	return name
}

// Parse decodes and validates one settings blob. It never panics on hostile
// input; any structural problem is an error (the caller falls back to
// defaults). Value-range problems are NOT errors — Apply routes every value
// through the owning setter, which clamps.
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
func Load(path string, logf func(string, ...any)) (Settings, bool) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
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
func Save(path string, s Settings) error {
	s.Version = Version
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
