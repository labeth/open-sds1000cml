// Package settings is where the SDS1000CML+ keeps its setup: the settings
// file and the numbered setups beside it in the stick's ota/ directory, the
// only persistent writable store on this unit (/tmp is read-only and the
// internal firmware paths are not ours to write; the boot anchor remounts
// the stick read-write, and the OTA agent keeps its state and logs there
// too). The setup, its file format and its loader, saver, slots, collect
// and apply are the core's (open-dso-core/settings); the files keep the
// bytes they always had (ADR-HAL-SETTINGS-FORMAT-KEPT). Storage is the
// board's hal.Storage over that directory.
// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings

import (
	"path/filepath"

	"github.com/labeth/open-dso-core/hal"
	core "github.com/labeth/open-dso-core/settings"
)

// The setup's parts are the core's.
type (
	// Channel is the core's settings.Channel.
	Channel = core.Channel
	// Trigger is the core's settings.Trigger.
	Trigger = core.Trigger
	// Acq is the core's settings.Acq.
	Acq = core.Acq
	// Decode is the core's settings.Decode.
	Decode = core.Decode
	// Settings is the core's settings.Settings.
	Settings = core.Settings
	// Bode is the core's settings.Bode.
	Bode = core.Bode
	// Sound is the core's settings.Sound.
	Sound = core.Sound
	// Screen is the core's settings.Screen.
	Screen = core.Screen
	// Serial is the core's settings.Serial.
	Serial = core.Serial
	// UI is the core's settings.UI.
	UI = core.UI
	// Display is the core's settings.Display.
	Display = core.Display
)

// Version is the setup's schema version, the core's.
const Version = core.Version

// PathIn is the settings file in the stick's ota/ directory (OTA_DIR, which
// startup.sh exports and the agent keeps its state and logs in): writable,
// and kept across app slot switches.
// TRLC-LINKS: REQ-SDS-089
func PathIn(otaDir string) string { return filepath.Join(otaDir, core.FileKey) }

// Storage is the board's storage: the settings file and the setups
// (setup03.json, named files) in one directory, written as the files have
// always been (temporary file, fsync, rename). Captures have no directory
// of their own (the hardcopy goes to the U-disk's root); Cal names the
// calibration the front end runs on.
type Storage struct {
	Dir string
	Cal hal.CalSource
}

var _ hal.Storage = Storage{}

// Settings is the settings file's directory.
// TRLC-LINKS: REQ-SDS-089
func (s Storage) Settings() hal.KV { return core.FileKV{Dir: s.Dir} }

// Setups are the numbered and named setups beside the settings file; slot
// n is SlotKey(n).
// TRLC-LINKS: REQ-SDS-089
func (s Storage) Setups() (hal.KV, bool) { return core.FileKV{Dir: s.Dir}, true }

// Captures has no directory of its own.
// TRLC-LINKS: REQ-SDS-089
func (Storage) Captures() (string, error) { return "", nil }

// Calibration is the calibration the front end runs on.
// TRLC-LINKS: REQ-SDS-089
func (s Storage) Calibration() hal.CalSource { return s.Cal }

// SlotKey is slot n's file: setup00.json … setup99.json.
// TRLC-LINKS: REQ-SDS-089
func SlotKey(n int) string { return core.SlotFile(n) }
