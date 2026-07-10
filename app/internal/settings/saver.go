package settings

import (
	"sync"
	"time"
)

// Saver watches the live setup and persists it, debounced. It is the ONE
// observation point for every mutation path — panel knobs/menus, web
// /api/set, /api/panel injection and SCPI all end in the same authoritative
// state (engine stats + front-end shadows + controller view), so polling that
// state catches them all without sprinkling save hooks through the setters.
//
// Policy: poll once a second (cheap snapshot copies, never on the acquisition
// hot path), and write only after the setup has been STABLE for the settle
// window (default 2 s) and actually differs from what is on disk. A knob
// sweep therefore costs one write, ~2–3 s after the last detent.
type Saver struct {
	path   string
	snap   func() Settings
	logf   func(string, ...any)
	poll   time.Duration
	settle time.Duration

	now  func() time.Time             // injectable clock (tests)
	save func(string, Settings) error // injectable sink (tests)

	mu         sync.Mutex
	primed     bool
	last       Settings  // latest observed state
	lastChange time.Time // when `last` last changed
	saved      Settings  // what we believe is on disk
	fails      int       // consecutive save failures (log throttle)
}

// NewSaver builds a saver for path; snap assembles the current setup
// (typically a Collect closure). Call Run on a goroutine after the restore
// has been applied — the first poll primes the "on disk" shadow from the live
// state, so an unchanged setup never rewrites the file.
func NewSaver(path string, snap func() Settings, logf func(string, ...any)) *Saver {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Saver{
		path: path, snap: snap, logf: logf,
		poll: time.Second, settle: 2 * time.Second,
		now: time.Now, save: Save,
	}
}

// Run polls until stop closes, then flushes any pending change.
func (s *Saver) Run(stop <-chan struct{}) {
	t := time.NewTicker(s.poll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			s.Flush()
			return
		case <-t.C:
			s.step()
		}
	}
}

// step is one poll: observe, debounce, maybe save.
func (s *Saver) step() {
	cur := s.snap()
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.primed {
		// Baseline: the state as of saver start IS the restored state, so
		// nothing is dirty yet. (If the restore was partial — e.g. a ladder
		// value the engine rejected — the next real change resyncs the file.)
		s.last, s.saved, s.primed = cur, cur, true
		return
	}
	if cur != s.last {
		s.last, s.lastChange = cur, now // still changing — restart the settle window
		return
	}
	if cur == s.saved || now.Sub(s.lastChange) < s.settle {
		return
	}
	s.persistLocked(cur)
}

// Flush writes immediately when the current state differs from the file —
// the shutdown path, so a change still inside the debounce window survives an
// agent-driven restart (SIGTERM → relaunch).
func (s *Saver) Flush() {
	cur := s.snap()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.primed && cur == s.saved {
		return
	}
	s.persistLocked(cur)
	s.last, s.primed = cur, true
}

// persistLocked writes cur and updates the disk shadow. Failures retry on the
// next poll (the stick can flake to read-only under load); the log is
// throttled so a persistently broken path cannot flood agent.log.
func (s *Saver) persistLocked(cur Settings) {
	if err := s.save(s.path, cur); err != nil {
		s.fails++
		if s.fails == 1 || s.fails%60 == 0 {
			s.logf("settings: save %s: %v (attempt %d)", s.path, err, s.fails)
		}
		return
	}
	if s.fails > 0 {
		s.logf("settings: save recovered after %d failures", s.fails)
	}
	s.fails = 0
	s.saved = cur
	s.logf("settings: saved %s", s.path)
}
