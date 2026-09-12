package agent

import (
	"fmt"
	"strings"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// runArgv runs a command with a timeout, capturing combined output. It never
// runs on the bus; it is a plain userspace exec for remote orchestration.
func (a *Agent) runArgv(argv []string, dir string, timeout time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		return buf.Bytes(), code, context.DeadlineExceeded
	}
	return buf.Bytes(), code, err
}

// runShell runs a script through /bin/sh -c (busybox ash on the device).
func (a *Agent) runShell(script string, timeout time.Duration) ([]byte, error) {
	out, _, err := a.runArgv([]string{"/bin/sh", "-c", script}, "", timeout)
	return out, err
}

// launchDetached starts a binary as a detached child that inherits the agent's
// open fds (the boot /dev/Gpmc + /dev/fpga_key) and reparents to init, so it
// survives the agent. Used to restore the factory app after a takeover test.
// LaunchOpt selects what console the child gets.  hRestoreFactory's contract is to hand the
// vendor UI "the still-open boot fds", but the default here did the opposite: Stdin nil is
// /dev/null in Go, and Setpgid puts the child in a fresh process group with no controlling
// terminal.  The vendor app opens the front-panel input devices on that console and fails with
// "open keyboard interrupt: Bad address" without it, which is what closed the SRAM read port.
// Which of the two details matters is an empirical question, so both are selectable and the
// default is unchanged.
type LaunchOpt struct {
	Console string `json:"console"` // open this and give it to the child as stdin/out/err
	Inherit bool   `json:"inherit"` // hand the child THIS agent's own stdin/out/err
	Setsid  bool   `json:"setsid"`  // new session and make the console controlling
	NoPgid  bool   `json:"no_pgid"` // stay in the agent's process group
	BootEnv bool   `json:"boot_env"`// run with init's environment, not the agent's
}

// bootEnv returns PID 1's environment -- what /etc/init.d/rcS, and so the vendor app, is
// given at boot.  exec.Command inherits the AGENT's environment instead, and the two differ
// sharply on this unit: init has 4 variables (HOME, TERM=linux, rootwait, ip) while the agent
// carries 21, including TERM=vt102 and the whole OTA_* takeover flag set (OTA_AUTO_TAKEOVER,
// OTA_SLOT_ROOT, OTA_HEALTH_DIR, ...).  Handing the vendor UI our takeover flags and the wrong
// TERM is not "the still-open boot fds" that hRestoreFactory promises.
func bootEnv() ([]string, error) {
	b, err := os.ReadFile("/proc/1/environ")
	if err != nil {
		return nil, fmt.Errorf("boot_env: read /proc/1/environ: %w", err)
	}
	var out []string
	for _, s := range strings.Split(string(b), "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func (a *Agent) launchDetached(path, dir string, opt LaunchOpt) (int, error) {
	cmd := exec.Command(path)
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
		defer f.Close() // the child keeps its own dup; ours must not leak
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
func tailFile(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := int64(0)
	if fi.Size() > int64(n) {
		off = fi.Size() - int64(n)
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// copyExecFile copies src to dst (0755) atomically and returns dst's sha256.
func copyExecFile(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
