// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
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
