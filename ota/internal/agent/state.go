package agent

import (
	"encoding/json"
	"os"
	"sync"
)

// State is the persisted agent state on the USB stick. TakenOver is written
// BEFORE the factory kill: if the agent dies mid-takeover, its respawned
// successor must immediately re-acquire the watchdog (an unserviced watchdog
// warm-resets the SoC and drops the USB/OTA path — spec 01 §4.1).
type State struct {
	TakenOver    bool `json:"taken_over"`
	AutoTakeover bool `json:"auto_takeover"`
}

type stateFile struct {
	mu   sync.Mutex
	path string
	s    State
}

func loadState(path string) *stateFile {
	sf := &stateFile{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &sf.s)
	}
	return sf
}

func (sf *stateFile) get() State {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.s
}

func (sf *stateFile) update(fn func(*State)) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	fn(&sf.s)
	b, err := json.MarshalIndent(sf.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := sf.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, sf.path)
}
