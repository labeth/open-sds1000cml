// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// appRun is how long this firmware's app runs on the board, and where it
// serves.
const (
	appRun    = 90 * time.Second
	appListen = "http://127.0.0.1:8080"
)

// runApp runs this firmware's app on the board for appRun, as the agent runs
// it (a child that inherits the boot descriptors, with the health path, the
// bus device and a settings directory of its own on the stick), and records
// its log, when it first reported health (three coherent frames), its status
// and its screen every 30 s. Then it stops the app. Whether the app comes up
// and what its status says is the end-to-end answer: this firmware as it
// stands on this population.
// TRLC-LINKS: REQ-SDS-289
func (c *collection) runApp(a *Archive, app string) string {
	settings := filepath.Join(filepath.Dir(c.self), "app-settings")
	if err := os.MkdirAll(settings, 0o755); err != nil { //nolint:gosec // G301: a directory on the stick
		return "app: " + err.Error()
	}
	logPath := filepath.Join(c.outDir, ".sds-diag-app.log")
	logf, err := os.Create(logPath) //nolint:gosec // G304: the app's log on the stick
	if err != nil {
		return "app: " + err.Error()
	}
	health := "/dev/sdsdiag-health"
	_ = os.Remove(health)
	cmd := exec.Command(app) //nolint:noctx // stopped with SIGTERM below
	cmd.Dir = settings
	cmd.Stdout, cmd.Stderr = logf, logf
	// The boot descriptors carry no CLOEXEC flag: the child inherits them.
	cmd.Env = append(os.Environ(), "OTA_HEALTH_PATH="+health, "SCOPE_GPMC=/dev/Gpmc", "OTA_DIR="+settings)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return "app: " + err.Error()
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	var firstHealth time.Duration
	var exitErr error
	gone := false
	next := 30 * time.Second
	tick := time.NewTicker(500 * time.Millisecond)
	for !gone && time.Since(start) < appRun {
		select {
		case exitErr = <-exited:
			gone = true
		case <-tick.C:
			if firstHealth == 0 {
				if _, err := os.Stat(health); err == nil {
					firstHealth = time.Since(start)
				}
			}
			if time.Since(start) >= next {
				t := int(next.Seconds())
				c.fetch(a, fmt.Sprintf("app/status-%03ds.json", t), "/api/status")
				c.fetch(a, fmt.Sprintf("app/screen-%03ds.png", t), "/api/screen.png")
				next += 30 * time.Second
			}
		}
	}
	tick.Stop()
	if !gone {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case exitErr = <-exited:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			exitErr = <-exited
		}
	}
	_ = logf.Close()
	a.AddFile("app/app.log", logPath, 16<<20)
	_ = os.Remove(logPath)
	ran := time.Since(start).Round(time.Second)
	switch {
	case gone:
		return fmt.Sprintf("app: exited after %v (%v) - app/app.log", ran, exitErr)
	case firstHealth == 0:
		return fmt.Sprintf("app: ran %v and never reported health (no coherent frames) - app/app.log", ran)
	}
	return fmt.Sprintf("app: healthy %v after start (three coherent frames), ran %v, stopped", firstHealth.Round(100*time.Millisecond), ran)
}

// fetch stores one of the app's pages in the archive.
// TRLC-LINKS: REQ-SDS-289
func (c *collection) fetch(a *Archive, name, path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appListen+path, nil)
	if err != nil {
		a.Errorf("%s: %v", path, err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.Errorf("%s: %v", path, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		a.Errorf("%s: %v", path, err)
		return
	}
	a.Add(name, b, appListen+path, strings.TrimSpace(resp.Status))
}
