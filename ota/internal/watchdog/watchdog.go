// Package watchdog is the SDS1102CML+'s /dev/watchdog as a hal.Watchdog; the
// core supervisor's keeper (open-dso-core/supervisor/watchdog) acquires it
// after the factory takeover and pets it.
//
// Contract (spec 01 §4.1, trap 17): the AM335x has a ~60 s hardware watchdog
// the factory firmware services. Once the factory app is killed, the agent —
// the rarely-changed trusted base, not the supervised app — must acquire it
// (retrying while the factory fd drains), pet it on a fixed interval well
// under 60 s with write("\x01") PLUS ioctl(WDIOC_KEEPALIVE), and disarm with
// the magic byte 'V' before close on a clean stop. An unserviced watchdog
// warm-resets the SoC, which drops USB hotplug and loses the OTA path until a
// physical power-cycle.
// ENGMODEL-OWNER-UNIT: FU-OTA-WATCHDOG
package watchdog

import (
	"fmt"
	"sync"

	"golang.org/x/sys/unix"
)

const wdiocKeepalive = 0x80045705 // WDIOC_KEEPALIVE (spec 01 §4.1)

// Device is the watchdog device node: closed from New, held from Open until
// Close.
// TRLC-LINKS: REQ-SDS-026, REQ-SDS-086
type Device struct {
	dev string

	mu sync.Mutex
	fd int
}

// New is the watchdog at the device node dev, not yet opened.
// TRLC-LINKS: REQ-SDS-086
func New(dev string) *Device {
	return &Device{dev: dev, fd: -1}
}

// Open opens the device with O_RDWR, which arms it. The descriptor is
// close-on-exec: an app holding it would keep the next agent generation
// from acquiring the watchdog after an agent self-update (EBUSY) while
// nobody pets it.
// TRLC-LINKS: REQ-SDS-086
func (w *Device) Open() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fd >= 0 {
		return nil
	}
	fd, err := unix.Open(w.dev, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", w.dev, err)
	}
	w.fd = fd
	return nil
}

// Pet issues both keepalives: the write is the primary, the ioctl is
// belt-and-suspenders across driver variants (spec 01 §4.1). It fails only
// when both do.
// TRLC-LINKS: REQ-SDS-026, REQ-SDS-086
func (w *Device) Pet() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fd < 0 {
		return fmt.Errorf("pet %s: not open", w.dev)
	}
	_, werr := unix.Write(w.fd, []byte{0x01})
	_, _, ierrno := unix.Syscall(unix.SYS_IOCTL, uintptr(w.fd), wdiocKeepalive, 0)
	if werr != nil && ierrno != 0 {
		return fmt.Errorf("write: %w, ioctl: %w", werr, ierrno)
	}
	return nil
}

// Close writes the magic byte 'V' then closes, so the driver disarms instead
// of resetting (no-op when not open). Used only on a clean agent stop — the
// respawned agent re-opens and re-arms.
// TRLC-LINKS: REQ-SDS-086
func (w *Device) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fd < 0 {
		return nil
	}
	_, _ = unix.Write(w.fd, []byte{'V'})
	err := unix.Close(w.fd)
	w.fd = -1
	return err
}
