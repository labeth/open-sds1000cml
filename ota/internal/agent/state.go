// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// State is the persisted agent state on the USB stick. TakenOver is written
// BEFORE the factory kill: if the agent dies mid-takeover, its respawned
// successor must immediately re-acquire the watchdog (an unserviced watchdog
// warm-resets the SoC and drops the USB/OTA path — spec 01 §4.1).
// TRLC-LINKS: REQ-SDS-104
type State struct {
	TakenOver    bool `json:"taken_over"`
	AutoTakeover bool `json:"auto_takeover"`
}

// TRLC-LINKS: REQ-SDS-104
type stateFile struct {
	mu   sync.Mutex
	path string
	s    State
}

// TRLC-LINKS: REQ-SDS-104
func loadState(path string) *stateFile {
	sf := &stateFile{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &sf.s)
	}
	return sf
}

// TRLC-LINKS: REQ-SDS-104
func (sf *stateFile) get() State {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.s
}

// TRLC-LINKS: REQ-SDS-104
func (sf *stateFile) update(fn func(*State)) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	fn(&sf.s)
	b, err := json.MarshalIndent(sf.s, "", "  ")
	if err != nil {
		return err
	}
	// Durable on a hard power cut: the state lives on the VFAT stick, and the
	// operator's next step after `untakeover` is often a mains power cycle a
	// few seconds later -- without fsync the write sits in the page cache
	// (default 30 s writeback) and the OLD state boots (seen 2026-09-05: the
	// agent came back taken_over=true and killed the factory app).
	tmp := sf.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, sf.path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(sf.path)); err == nil {
		_ = d.Sync() // directory entry (the rename) -- best effort, VFAT may not support it
		d.Close()
	}
	return nil
}
