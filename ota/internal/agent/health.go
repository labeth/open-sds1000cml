package agent

import (
	"os"
	"sync"
	"time"
)

// healthWatcher implements the agent side of the spec 01 §4.2 health-file
// contract. The app writes/touches the token file at OTA_HEALTH_PATH; the
// watcher tracks *change*, not absolute mtime age, because the device clock
// is unreliable. Staleness is judged on a monotonic clock:
//
//   - first change after launch  => healthyOnce (the app reached >=3 coherent
//     frames; it must NOT write the token at launch, so seeing it change means
//     genuine capture)
//   - no change for > staleness while the process lives => stale => relaunch
type healthWatcher struct {
	path string

	mu          sync.Mutex
	lastSig     string
	lastChange  time.Time // monotonic
	healthyOnce bool
	started     time.Time
	lastContent []byte
}

func newHealthWatcher(path string) *healthWatcher {
	// Remove any stale token from a previous run: the first-report gate is
	// only meaningful if the file starts absent.
	_ = os.Remove(path)
	return &healthWatcher{path: path, started: time.Now()}
}

// poll checks the token once; call every ~500ms.
func (h *healthWatcher) poll() {
	fi, err := os.Stat(h.path)
	if err != nil {
		return // not written yet (or removed)
	}
	sig := fi.ModTime().String() + "/" + itoa64(fi.Size())
	// mtime granularity can be coarse; include a content sample so a same-
	// second rewrite still counts as a change.
	content, _ := readPrefix(h.path, 256)
	h.mu.Lock()
	defer h.mu.Unlock()
	if sig != h.lastSig || string(content) != string(h.lastContent) {
		h.lastSig = sig
		h.lastContent = content
		h.lastChange = time.Now()
		h.healthyOnce = true
	}
}

type healthStatus struct {
	HealthyOnce bool          `json:"healthy_once"`
	SinceChange time.Duration `json:"since_change_ms"`
	Token       string        `json:"token,omitempty"`
}

func (h *healthWatcher) status() healthStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := healthStatus{HealthyOnce: h.healthyOnce, Token: string(h.lastContent)}
	if h.healthyOnce {
		s.SinceChange = time.Since(h.lastChange) / time.Millisecond * time.Millisecond
	}
	return s
}

// verdict returns (healthy, reason). grace = deadline for the FIRST report;
// staleness = max quiet interval afterwards.
func (h *healthWatcher) verdict(grace, staleness time.Duration) (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.healthyOnce {
		if time.Since(h.started) > grace {
			return false, "no first health report within grace"
		}
		return true, "waiting first report"
	}
	if time.Since(h.lastChange) > staleness {
		return false, "health token stale"
	}
	return true, "ok"
}

func readPrefix(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, _ := f.Read(buf)
	return buf[:m], nil
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
