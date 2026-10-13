// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings

import (
	"fmt"
	"path/filepath"
)

// Slots is numbered setup memory (SCPI *SAV/*RCL): each slot is a settings
// file beside the persisted setup, so saved setups survive a restart. Collect
// and Apply are the same paths the boot restore and the saver use.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
type Slots struct {
	Dir     string
	Collect func() Settings
	Apply   func(Settings)
}

// slotPath is a slot's file.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func (s Slots) slotPath(n int) string { return filepath.Join(s.Dir, fmt.Sprintf("setup%02d.json", n)) }

// SaveSetup stores the current setup in slot n.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func (s Slots) SaveSetup(n int) error {
	if s.Collect == nil {
		return fmt.Errorf("setup memory unavailable")
	}
	return Save(s.slotPath(n), s.setup())
}

// RecallSetup applies the setup stored in slot n; an empty slot is an error.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func (s Slots) RecallSetup(n int) error {
	st, ok := Load(s.slotPath(n), nil)
	if !ok {
		return fmt.Errorf("setup slot %d is empty", n)
	}
	if s.Apply == nil {
		return fmt.Errorf("setup memory unavailable")
	}
	st.Screen = Screen{} // the screen saver is the instrument's
	s.Apply(st)
	return nil
}

// CurrentSetup is the setup a save would write now (the panel's Save/Recall
// lamp compares it with the one last saved or recalled).
// TRLC-LINKS: REQ-SDS-136
func (s Slots) CurrentSetup() Settings {
	if s.Collect == nil {
		return Settings{}
	}
	return s.setup()
}

// setup is the current setup as a setup memory keeps it: without the
// instrument's screen saver.
// TRLC-LINKS: REQ-SDS-221
func (s Slots) setup() Settings {
	st := s.Collect()
	st.Screen = Screen{}
	return st
}

// PeekSetup reads slot n without applying it (the panel's FILE rows show
// what each slot holds).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func (s Slots) PeekSetup(n int) (Settings, bool) {
	return Load(s.slotPath(n), nil)
}

// SaveSetupFile stores the current setup as a named file (SCPI STPN). The
// name is a bare file name the caller has validated.
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func (s Slots) SaveSetupFile(name string) error {
	if s.Collect == nil || name != filepath.Base(name) {
		return fmt.Errorf("setup file unavailable")
	}
	return Save(filepath.Join(s.Dir, name), s.setup())
}

// RecallSetupFile applies a setup file stored by SaveSetupFile (SCPI RCPN).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func (s Slots) RecallSetupFile(name string) error {
	if name != filepath.Base(name) {
		return fmt.Errorf("setup file unavailable")
	}
	st, ok := Load(filepath.Join(s.Dir, name), nil)
	if !ok {
		return fmt.Errorf("no setup file %s", name)
	}
	if s.Apply == nil {
		return fmt.Errorf("setup memory unavailable")
	}
	st.Screen = Screen{} // the screen saver is the instrument's
	s.Apply(st)
	return nil
}
