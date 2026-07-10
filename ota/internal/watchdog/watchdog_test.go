package watchdog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests never open a real /dev node: a plain temp file stands in for the
// watchdog device. unix.Open/Write behave identically; the WDIOC_KEEPALIVE
// ioctl fails with ENOTTY, which exercises the belt-and-suspenders contract
// (pet is healthy as long as the write half succeeds).

func fakeDev(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watchdog")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAcquirePetsImmediately(t *testing.T) {
	dev := fakeDev(t)
	w := New(dev)
	if err := w.Acquire(time.Second, time.Hour); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer w.Disarm()

	st := w.Status()
	if !st.Armed {
		t.Error("should be armed after acquire")
	}
	if st.LastPet.IsZero() {
		t.Error("acquire must pet immediately (fresh countdown)")
	}
	if st.PetErr != "" {
		t.Errorf("pet on a writable device must not error: %q", st.PetErr)
	}
	b, err := os.ReadFile(dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 1 || b[0] != 0x01 {
		t.Errorf("device content = %q, want the 0x01 keepalive byte", b)
	}
}

func TestPetLoopFeedsOnInterval(t *testing.T) {
	dev := fakeDev(t)
	w := New(dev)
	if err := w.Acquire(time.Second, 20*time.Millisecond); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	w.Disarm()

	b, err := os.ReadFile(dev)
	if err != nil {
		t.Fatal(err)
	}
	// 1 immediate pet + ~15 ticker pets in 300ms at 20ms; be generous against
	// scheduler jitter but require that the loop demonstrably ran.
	if pets := bytes.Count(b, []byte{0x01}); pets < 4 {
		t.Errorf("only %d keepalive bytes written in 300ms at 20ms interval", pets)
	}
	if b[len(b)-1] != 'V' {
		t.Errorf("last byte = %#x, want the magic disarm byte 'V'", b[len(b)-1])
	}
	if w.Status().Armed {
		t.Error("should not be armed after disarm")
	}
}

func TestPetLoopStopsAfterDisarm(t *testing.T) {
	dev := fakeDev(t)
	w := New(dev)
	if err := w.Acquire(time.Second, 10*time.Millisecond); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	w.Disarm()
	fi1, _ := os.Stat(dev)
	time.Sleep(100 * time.Millisecond)
	fi2, _ := os.Stat(dev)
	if fi2.Size() != fi1.Size() {
		t.Errorf("device grew from %d to %d bytes after disarm — pet loop still running", fi1.Size(), fi2.Size())
	}
}

func TestAcquireIdempotentWhileArmed(t *testing.T) {
	dev := fakeDev(t)
	w := New(dev)
	if err := w.Acquire(time.Second, time.Hour); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer w.Disarm()
	// Second acquire must be a cheap no-op (already armed), not a re-open.
	start := time.Now()
	if err := w.Acquire(time.Second, time.Hour); err != nil {
		t.Fatalf("re-acquire while armed: %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("re-acquire while armed should return immediately")
	}
}

func TestAcquireTimesOutWhenDeviceMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	w := New(missing)
	err := w.Acquire(300*time.Millisecond, time.Hour)
	if err == nil {
		t.Fatal("acquire of a missing device must fail")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error should name the device: %v", err)
	}
	if w.Status().Armed {
		t.Error("must not be armed after a failed acquire")
	}
}

func TestAcquireRetriesUntilDeviceAppears(t *testing.T) {
	// The takeover contract: the factory app's fd needs a moment to drain
	// after the kill, so Acquire retries the open until the timeout.
	dev := filepath.Join(t.TempDir(), "watchdog-late")
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(dev, nil, 0o644)
	}()
	w := New(dev)
	if err := w.Acquire(3*time.Second, time.Hour); err != nil {
		t.Fatalf("acquire should retry until the device appears: %v", err)
	}
	defer w.Disarm()
	if !w.Status().Armed {
		t.Error("should be armed once the device appeared")
	}
}

func TestDisarmWithoutAcquireIsNoop(t *testing.T) {
	w := New(filepath.Join(t.TempDir(), "never-opened"))
	w.Disarm() // must not panic or create anything
	w.Disarm() // idempotent
	if w.Status().Armed {
		t.Error("never-acquired watchdog reports armed")
	}
}

func TestReacquireAfterDisarm(t *testing.T) {
	// A respawned agent generation re-acquires; the fd/stop-channel cycle must
	// survive disarm -> acquire.
	dev := fakeDev(t)
	w := New(dev)
	if err := w.Acquire(time.Second, time.Hour); err != nil {
		t.Fatal(err)
	}
	w.Disarm()
	if err := w.Acquire(time.Second, time.Hour); err != nil {
		t.Fatalf("re-acquire after disarm: %v", err)
	}
	defer w.Disarm()
	if !w.Status().Armed {
		t.Error("should be armed after re-acquire")
	}
}
