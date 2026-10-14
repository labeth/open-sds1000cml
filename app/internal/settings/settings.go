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
	"fmt"
	"open-sds/app/internal/engine"
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
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Channel struct {
	VdivV     float64 `json:"vdiv_v"`        // volts/div (must be a ladder detent to restore)
	OffsetV   float64 `json:"offset_v"`      // input-referred offset volts
	OffsetSet bool    `json:"offset_set"`    // false = boot-inherited offset untouched (never restore 0 V over it)
	Coupling  int     `json:"coupling"`      // 0=DC 1=AC 2=GND (analog.Cpl*)
	Probe     float64 `json:"probe"`         // probe attenuation: 1, 10 or 100
	BWL       bool    `json:"bwl,omitempty"` // 20 MHz bandwidth limit
}

// Trigger is the persisted trigger setup.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Trigger struct {
	Qualifiers engine.TrigQual `json:"qualifiers,omitempty"`
	LevelCode  int             `json:"level_code"`         // trigger-level DAC code; 0 = boot comparator untouched
	Rising     bool            `json:"rising"`             // edge slope
	Source     int             `json:"source"`             // 0=C1 1=C2
	Type       int             `json:"type"`               // 0=edge 1=pulse 2=slope 3=video
	Norm       bool            `json:"norm"`               // trigger mode: true=NORM false=AUTO
	HoldoffS   float64         `json:"holdoff_s"`          // 0 = off
	PosFrac    float64         `json:"pos_frac,omitempty"` // trigger position on screen; 0 = not saved
}

// Acq is the persisted acquisition mode.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Acq struct {
	PrecisionRateHz float64 `json:"precision_rate_hz,omitempty"`
	Mode            int     `json:"mode"` // 0=normal 1=average 2=eres 3=peak
	AvgCount        int     `json:"avg_count"`
	EresLen         int     `json:"eres_len"`
	RollStream      bool    `json:"roll_stream,omitempty"` // roll on the stream image (gap-free)
	MemDepth        int     `json:"mem_depth,omitempty"`   // requested depth (samples); 0 = not saved
}

// Decode is the device protocol-decode setup (controller-owned; historically
// reset to Off on every app restart).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Decode struct {
	Proto  int  `json:"proto"` // 0=off 2=uart 3=i2c 4=spi 5..11 the bit-rate and SENT decoders; 1, Auto, loads as a detection
	Baud   int  `json:"baud"`
	ChA    int  `json:"cha"` // UART source; I2C SCL; SPI CLK (0=C1 1=C2)
	ChB    int  `json:"chb"` // I2C SDA; SPI DATA
	CPOL   bool `json:"cpol"`
	CPHA   bool `json:"cpha"`
	LSB    bool `json:"lsb,omitempty"` // SPI least significant bit first
	Format int  `json:"format"`        // 0=hex 1=ascii 2=both
	// Decoded-stream trigger (BUS ▸ Stream): 0=error 1=value 2=manual, and
	// the value it waits for.
	StreamTrig  int `json:"stream_trig,omitempty"`
	StreamValue int `json:"stream_value,omitempty"`
}

// Settings is the whole persisted setup. Every field is comparable, so the
// saver detects change with plain ==.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Settings struct {
	Version  int        `json:"version"`
	TdivS    float64    `json:"tdiv_s"`
	Ch       [2]Channel `json:"ch"`
	VertSet  bool       `json:"vert_set"` // vertical front end was driven; false = keep the seed-don't-emit boot rule
	Trigger  Trigger    `json:"trigger"`
	Acq      Acq        `json:"acq"`
	Decode   Decode     `json:"decode"`
	ViewMode int        `json:"view_mode"` // 0=Y-T 1=X-Y 2=FFT 3=Bode 4=Spgm
	Display  Display    `json:"display"`
	UI       UI         `json:"ui"`
	Serial   Serial     `json:"serial"`
	Bode     Bode       `json:"bode,omitzero"`
	Screen   Screen     `json:"screen,omitzero"` // the instrument's, never in a saved setup
}

// Bode is the frequency response's reference (DUT input) and DUT output
// channels, 0 = C1 (the VIEW tab's Bode rows). Set marks a saved block: an
// older file has none and keeps C1 / C2.
// TRLC-LINKS: REQ-SDS-066, REQ-SDS-089
type Bode struct {
	Set bool `json:"set"`
	Ref int  `json:"ref"`
	Dut int  `json:"dut"`
}

// Screen is the screen saver (ADR-SCREEN-SAVER): minutes without input
// before the saver and before the screen goes off, 0 for never. It belongs
// to the instrument, so the setup memories leave it out and their recall
// leaves it as it is. Set marks a saved block (an older file has none and
// keeps the factory times).
// TRLC-LINKS: REQ-SDS-221, REQ-SDS-089
type Screen struct {
	Set      bool `json:"set"`
	SaverMin int  `json:"saver_min"`
	OffMin   int  `json:"off_min"`
}

// Serial is the serial trigger: its match as the panel writes it ("55 AA",
// "A? ??"; empty = any byte), the I2C address and direction it waits for, and
// whether it is armed. Set marks a saved block: an older file has none and
// boots with the trigger disarmed.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-089
type Serial struct {
	Set   bool   `json:"set"`
	Armed bool   `json:"armed"`
	Match string `json:"match,omitempty"`
	Addr  int    `json:"addr"` // -1 = any address
	RW    int    `json:"rw"`   // 0 write, 1 read, 2 either
}

// UI is where the panel's task workspaces were (ADR-TASK-WORKSPACES): the
// workspace, each one's view, and the panel-held choices beside them, so a
// restart or a recalled setup returns there. Set marks a saved block (an
// older file has none and keeps the power-on workspaces).
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-089
type UI struct {
	Set       bool   `json:"set"`
	Workspace int    `json:"workspace"`          // 0 SCOPE, 1 BUS, 2 SPECTRUM, 3 MEASURE, 4 COMPARE
	Views     [5]int `json:"views"`              // each workspace's display view
	BusEye    bool   `json:"bus_eye,omitempty"`  // BUS on the eye diagram
	MeasAll   bool   `json:"meas_all,omitempty"` // MEASURE on all values
	// Meas holds the chosen readouts in order: channel*32 + parameter + 1
	// (0 = none).
	Meas          [12]int `json:"meas"`
	FFTSource     int     `json:"fft_source,omitempty"`
	MasterDepth   int     `json:"master_depth,omitempty"`
	RemoteNotices bool    `json:"remote_notices"`
	// The mask build's frames and tolerance, and super-res's channel, grid and
	// stop, kept over a restart. Zero is the factory value (an older file);
	// the indexes are stored plus one.
	MaskFrames int     `json:"mask_frames,omitempty"`
	MaskTol    int     `json:"mask_tol,omitempty"` // preset index + 1
	SrCh       int     `json:"sr_ch,omitempty"`    // channel + 1
	SrGrid     int     `json:"sr_grid,omitempty"`
	SrStop     int     `json:"sr_stop,omitempty"` // stop mode + 1
	SrTarget   float64 `json:"sr_target,omitempty"`
}

// Display is the LCD's display setup beyond the view mode. Zero values are the
// power-on state (both channels shown), so an older file reads as defaults.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Display struct {
	MathMode int     `json:"math_mode"` // 0 off, 1 C1+C2, 2 C1-C2, 3 C2-C1, 4 C1xC2
	ShowMeas bool    `json:"show_meas"`
	Persist  bool    `json:"persist"`
	Hidden   [2]bool `json:"hidden"` // channel trace off (DISPLAY CH1/CH2)
	// Set marks the blocks below (an older file has none: invert off, no
	// zoom, the cursors where they were). A recalled setup brings them back.
	Set     bool       `json:"set,omitempty"`
	Invert  [2]bool    `json:"invert"`   // the channel drawn upside down (the CH tabs' Invert)
	CurMode int        `json:"cur_mode"` // the cursors: 0 off, 1 time, 2 volts
	CurX    [2]float64 `json:"cur_x"`    // A and B, screen fractions across
	CurY    [2]float64 `json:"cur_y"`    // A and B, screen fractions up
	CurSel  int        `json:"cur_sel"`  // the cursor Adjust moves: 0 A, 1 B
	Zoom    int        `json:"zoom"`     // horizontal magnification (1 none)
	ZoomOff float64    `json:"zoom_off"` // the zoom window's pan
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
//
// TRLC-LINKS: REQ-SDS-089
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
// TRLC-LINKS: REQ-SDS-089
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
