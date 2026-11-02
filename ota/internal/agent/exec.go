// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/labeth/open-sds1000cml/ota/internal/slots"
)

// runArgv runs a command with a timeout, capturing combined output. It never
// runs on the bus; it is a plain userspace exec for remote orchestration.
// TRLC-LINKS: REQ-SDS-115
func (a *Agent) runArgv(argv []string, dir string, timeout time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // running the operator's command is the exec RPC's purpose
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return buf.Bytes(), code, context.DeadlineExceeded
	}
	return buf.Bytes(), code, err
}

// runShell runs a script through /bin/sh -c (busybox ash on the device).
// TRLC-LINKS: REQ-SDS-115
func (a *Agent) runShell(script string, timeout time.Duration) ([]byte, error) {
	out, _, err := a.runArgv([]string{"/bin/sh", "-c", script}, "", timeout)
	return out, err
}

// LaunchOpt selects the console, session and environment a detached child
// gets. The default gives it /dev/null as stdin and a fresh process group
// with no controlling terminal. The vendor app opens the front-panel input
// devices on its console and fails with "open keyboard interrupt: Bad
// address" without one, which leaves the SRAM read port closed.
// TRLC-LINKS: REQ-SDS-120
type LaunchOpt struct {
	Console string `json:"console"`  // open this and give it to the child as stdin/out/err
	Inherit bool   `json:"inherit"`  // hand the child THIS agent's own stdin/out/err
	Setsid  bool   `json:"setsid"`   // new session and make the console controlling
	NoPgid  bool   `json:"no_pgid"`  // stay in the agent's process group
	BootEnv bool   `json:"boot_env"` // run with init's environment, not the agent's
}

// bootEnv returns PID 1's environment -- what /etc/init.d/rcS, and so the vendor app, is
// given at boot.  exec.Command inherits the AGENT's environment instead, and the two differ
// sharply on this unit: init has 4 variables (HOME, TERM=linux, rootwait, ip) while the agent
// carries 21, including TERM=vt102 and the whole OTA_* takeover flag set (OTA_AUTO_TAKEOVER,
// OTA_SLOT_ROOT, OTA_HEALTH_DIR, ...).  Handing the vendor UI our takeover flags and the wrong
// TERM is not "the still-open boot fds" that hRestoreFactory promises.
// TRLC-LINKS: REQ-SDS-120
func bootEnv() ([]string, error) {
	b, err := os.ReadFile("/proc/1/environ")
	if err != nil {
		return nil, fmt.Errorf("boot_env: read /proc/1/environ: %w", err)
	}
	var out []string
	for s := range strings.SplitSeq(string(b), "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// launchDetached starts a binary as a detached child that inherits the agent's
// open fds (the boot /dev/Gpmc + /dev/fpga_key) and reparents to init, so it
// survives the agent. Used to restore the factory app after a takeover test.
// TRLC-LINKS: REQ-SDS-120
func (a *Agent) launchDetached(path, dir string, opt LaunchOpt) (int, error) {
	cmd := exec.Command(path) //nolint:noctx // the child must outlive every context here
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if opt.Inherit {
		cmd.Stdin = os.Stdin
	}
	if opt.Console != "" {
		f, err := os.OpenFile(opt.Console, os.O_RDWR, 0)
		if err != nil {
			return 0, fmt.Errorf("restore-factory: console %s: %w", opt.Console, err)
		}
		defer func() { _ = f.Close() }() // the child keeps its own dup; ours must not leak
		cmd.Stdin, cmd.Stdout, cmd.Stderr = f, f, f
	}
	if opt.BootEnv {
		env, err := bootEnv()
		if err != nil {
			return 0, err
		}
		cmd.Env = env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: !opt.NoPgid}
	if opt.Setsid {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: opt.Console != "", Ctty: 0}
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Reap asynchronously if it stays our child; if it reparents to init that
	// is fine too. We do not wait on it — it must outlive this handler.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// tailFile returns the last n bytes of a file.
// TRLC-LINKS: REQ-SDS-115
func tailFile(path string, n int) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the logs RPC reads the file the operator names, like its exec RPC could
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := int64(0)
	if fi.Size() > int64(n) {
		off = fi.Size() - int64(n)
	}
	// One read into a buffer of the tail's size: io.ReadAll grows from 512
	// bytes, copying the tail over and over.
	buf := make([]byte, fi.Size()-off)
	m, err := f.ReadAt(buf, off)
	if errors.Is(err, io.EOF) {
		err = nil // the file shrank since the stat
	}
	return buf[:m], err
}

// copyExecFile copies src to dst (0755) atomically and returns dst's sha256.
// TRLC-LINKS: REQ-SDS-119
func copyExecFile(src, dst string) (string, error) { return slots.CopyExecutable(src, dst) }
