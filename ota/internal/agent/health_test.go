package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHealthWatcherFirstReportGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.health")
	h := newHealthWatcher(path)

	// No token yet: healthy while within grace, unhealthy past it.
	if ok, _ := h.verdict(100*time.Millisecond, time.Second); !ok {
		t.Error("should be healthy within grace before first report")
	}
	h.started = time.Now().Add(-time.Second) // simulate grace elapsed
	if ok, why := h.verdict(100*time.Millisecond, time.Second); ok {
		t.Errorf("should be unhealthy after grace with no report; why=%q", why)
	}
}

func TestHealthWatcherLivenessAndStaleness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.health")
	h := newHealthWatcher(path)

	// App writes the token (first genuine capture).
	os.WriteFile(path, []byte("1"), 0o644)
	h.poll()
	if !h.status().HealthyOnce {
		t.Fatal("first token change should mark healthy_once")
	}
	if ok, _ := h.verdict(time.Second, 200*time.Millisecond); !ok {
		t.Error("fresh token should be healthy")
	}

	// Token goes stale (no change) past the staleness window.
	h.lastChange = time.Now().Add(-time.Second)
	if ok, why := h.verdict(time.Second, 200*time.Millisecond); ok {
		t.Errorf("stale token should be unhealthy; why=%q", why)
	}

	// App touches the token again with new content -> healthy again.
	os.WriteFile(path, []byte("2"), 0o644)
	h.poll()
	if ok, _ := h.verdict(time.Second, 200*time.Millisecond); !ok {
		t.Error("re-touched token should be healthy again")
	}
}

func TestHealthWatcherIgnoresPreexistingToken(t *testing.T) {
	// A token left from a previous run must be removed so the first-report
	// gate is meaningful (spec 01 §4.2: do not rubber-stamp a wedged boot).
	path := filepath.Join(t.TempDir(), "app.health")
	os.WriteFile(path, []byte("stale"), 0o644)
	h := newHealthWatcher(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("newHealthWatcher must remove a pre-existing token")
	}
	if h.status().HealthyOnce {
		t.Error("should not be healthy from a leftover token")
	}
}
