package agent

import (
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
func (a *Agent) launchDetached(path, dir string) (int, error) {
	cmd := exec.Command(path)
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
