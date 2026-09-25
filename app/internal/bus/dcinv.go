// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// dcinvKO is the vendored D-cache invalidate module (bus/dcinv/README.md).
//
//go:embed dcinv/dcinv.ko
var dcinvKO []byte

// DcinvPath is the device node the EDMA drainer opens for coherency.
const DcinvPath = "/dev/dcinv"

// dcinvEnv is a runner hook so the boot sequence can be unit-tested without
// insmod/mknod on the host.
// TRLC-LINKS: REQ-SDS-081
type dcinvEnv struct {
	exists   func(string) bool
	writeKO  func(dir string) (string, error)
	insmod   func(path string) error
	devices  func() (string, error) // contents of /proc/devices
	mknod    func(path string, major int) error
	sleep    func(time.Duration)
	deadline time.Duration
}

// TRLC-LINKS: REQ-SDS-081
func realDcinvEnv() dcinvEnv {
	return dcinvEnv{
		exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		writeKO: func(dir string) (string, error) {
			p := filepath.Join(dir, "dcinv.ko")
			if b, err := os.ReadFile(p); err == nil && string(b) == string(dcinvKO) {
				return p, nil
			}
			return p, os.WriteFile(p, dcinvKO, 0o644)
		},
		insmod: func(p string) error {
			out, err := exec.Command("/sbin/insmod", p).CombinedOutput()
			if err != nil {
				return fmt.Errorf("insmod: %v: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		devices: func() (string, error) { b, err := os.ReadFile("/proc/devices"); return string(b), err },
		mknod: func(p string, major int) error {
			return syscall.Mknod(p, syscall.S_IFCHR|0o600, int(uint32(major)<<8))
		},
		sleep:    time.Sleep,
		deadline: 2 * time.Second,
	}
}

// EnsureDcinv makes /dev/dcinv available: loads the embedded module (written
// next to the app binary on the U-disk, a reboot-recoverable location) when the
// node is absent, and creates the node from /proc/devices if the kernel did not.
// Failure is reported, not fatal: the EDMA drainer then falls back to a fresh
// buffer per drain (which hardware showed is NOT coherent on this unit — the
// caller logs the consequence).
// TRLC-LINKS: REQ-SDS-081
func EnsureDcinv(logf func(string, ...any)) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return ensureDcinv(realDcinvEnv(), filepath.Dir(exe), logf)
}

// TRLC-LINKS: REQ-SDS-081
func ensureDcinv(e dcinvEnv, dir string, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if e.exists(DcinvPath) {
		logf("bus: %s present", DcinvPath)
		return nil
	}
	major, loaded := dcinvMajor(e)
	if !loaded {
		p, err := e.writeKO(dir)
		if err != nil {
			return fmt.Errorf("dcinv: write module: %w", err)
		}
		if err := e.insmod(p); err != nil {
			return fmt.Errorf("dcinv: %w", err)
		}
		logf("bus: dcinv module loaded from %s", p)
	}
	// devtmpfs/mdev may create the node; otherwise create it from /proc/devices.
	t := time.Duration(0)
	for !e.exists(DcinvPath) && t < e.deadline {
		e.sleep(50 * time.Millisecond)
		t += 50 * time.Millisecond
	}
	if e.exists(DcinvPath) {
		return nil
	}
	if major, loaded = dcinvMajor(e); !loaded {
		return errors.New("dcinv: module loaded but /proc/devices has no dcinv entry")
	}
	if err := e.mknod(DcinvPath, major); err != nil {
		return fmt.Errorf("dcinv: mknod %s (major %d): %w", DcinvPath, major, err)
	}
	logf("bus: created %s (major %d)", DcinvPath, major)
	return nil
}

// dcinvMajor parses /proc/devices for the dcinv character device.
// TRLC-LINKS: REQ-SDS-081
func dcinvMajor(e dcinvEnv) (int, bool) {
	s, err := e.devices()
	if err != nil {
		return 0, false
	}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == "dcinv" {
			if m, err := strconv.Atoi(f[0]); err == nil {
				return m, true
			}
		}
	}
	return 0, false
}
