// Package watchdog owns /dev/watchdog after the factory takeover.
//
// Contract (spec 01 §4.1, trap 17): the AM335x has a ~60 s hardware watchdog
// the factory firmware services. Once the factory app is killed, the agent —
// the rarely-changed trusted base, not the supervised app — must acquire it
// (retrying while the factory fd drains), pet it on a fixed interval well
// under 60 s with write("\x01") PLUS ioctl(WDIOC_KEEPALIVE), and disarm with
// the magic byte 'V' before close on a clean stop. An unserviced watchdog
// warm-resets the SoC, which drops USB hotplug and loses the OTA path until a
// physical power-cycle.
package watchdog

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const wdiocKeepalive = 0x80045705 // WDIOC_KEEPALIVE (spec 01 §4.1)

type Watchdog struct {
	dev string

	mu      sync.Mutex
	fd      int
	stop    chan struct{}
	lastPet time.Time
	petErr  error
}

func New(dev string) *Watchdog {
	return &Watchdog{dev: dev, fd: -1}
}

// Acquire opens the device with O_RDWR, retrying until timeout (the factory
// app's inherited fd needs a moment to drain after the kill), pets once so
// the countdown is fresh, and starts the pet loop.
func (w *Watchdog) Acquire(timeout, petEvery time.Duration) error {
	w.mu.Lock()
	if w.fd >= 0 {
		w.mu.Unlock()
		return nil // already armed
	}
	w.mu.Unlock()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		fd, err := unix.Open(w.dev, unix.O_RDWR, 0)
		if err == nil {
			w.mu.Lock()
			w.fd = fd
			w.stop = make(chan struct{})
			w.mu.Unlock()
			w.pet() // fresh countdown immediately on acquire
			go w.loop(petEvery)
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("watchdog: open %s: %w", w.dev, lastErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (w *Watchdog) loop(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		w.mu.Lock()
		stop := w.stop
		w.mu.Unlock()
		select {
		case <-t.C:
			w.pet()
		case <-stop:
			return
		}
	}
}

// pet issues both keepalives: the write is the primary, the ioctl is
// belt-and-suspenders across driver variants (spec 01 §4.1).
func (w *Watchdog) pet() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fd < 0 {
		return
	}
	_, werr := unix.Write(w.fd, []byte{0x01})
	_, _, ierrno := unix.Syscall(unix.SYS_IOCTL, uintptr(w.fd), wdiocKeepalive, 0)
	w.lastPet = time.Now()
	if werr != nil && ierrno != 0 {
		w.petErr = fmt.Errorf("write: %v, ioctl: %v", werr, ierrno)
	} else {
		w.petErr = nil
	}
}

// Disarm writes the magic byte 'V' then closes, so the driver disarms instead
// of resetting (no-op if never acquired). Used only on a clean agent stop —
// the respawned agent re-acquires and re-arms.
func (w *Watchdog) Disarm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fd < 0 {
		return
	}
	close(w.stop)
	_, _ = unix.Write(w.fd, []byte{'V'})
	_ = unix.Close(w.fd)
	w.fd = -1
}

type Status struct {
	Armed   bool      `json:"armed"`
	LastPet time.Time `json:"last_pet,omitzero"`
	PetErr  string    `json:"pet_err,omitempty"`
}

func (w *Watchdog) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := Status{Armed: w.fd >= 0, LastPet: w.lastPet}
	if w.petErr != nil {
		s.PetErr = w.petErr.Error()
	}
	return s
}
