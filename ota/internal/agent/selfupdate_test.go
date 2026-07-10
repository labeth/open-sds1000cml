package agent

// agent.update / agent.restart happy paths. Both handlers end in a deliberate
// process exit that hands control back to the startup.sh A/B loop; osExit (a
// var defaulting to os.Exit) captures that exit in-process. The func-var seam
// was chosen over a re-exec subprocess because the whole handler suite is
// already exercised in-process via Dispatch (rpc_test.go), and the exit fires
// from a background goroutine after the RPC reply — trivial to observe on a
// channel, awkward to assert through a re-exec'd binary.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureExit swaps osExit for a recorder for the duration of the test.
func captureExit(t *testing.T) chan int {
	t.Helper()
	ch := make(chan int, 1)
	old := osExit
	osExit = func(code int) { ch <- code }
	t.Cleanup(func() { osExit = old })
	return ch
}

func waitExit(t *testing.T, ch chan int) int {
	t.Helper()
	select {
	case code := <-ch:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("agent never exited")
		return -1
	}
}

func TestAgentUpdateInstallsFlipsAndExitsClean(t *testing.T) {
	a := testAgent(t)
	// OTA_DIR must exist: agent.B, agent.active and agent.intent live there
	// (on the device startup.sh creates it).
	if err := os.MkdirAll(a.cfg.OTADir, 0o755); err != nil {
		t.Fatal(err)
	}
	exitCh := captureExit(t)

	payload := []byte("#!/bin/sh\necho new-agent\n")
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])
	src := filepath.Join(t.TempDir(), "agent.upload")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	req := fmt.Sprintf(`{"cmd":"agent.update","args":{"src":%q,"sha256":%q}}`, src, sumHex)
	resp := a.Dispatch([]byte(req))
	if !resp.OK {
		t.Fatalf("agent.update failed: %s", resp.Err)
	}
	data := resp.Data.(map[string]any)
	// The test binary runs off-slot (AgentSlot "?"), so the update targets B —
	// the same inactive-slot choice a slot-A production agent makes.
	if data["active_agent"] != "B" {
		t.Errorf("active_agent = %v, want B", data["active_agent"])
	}
	if data["sha256"] != sumHex {
		t.Errorf("sha256 = %v, want %s", data["sha256"], sumHex)
	}

	// Install: the inactive agent slot holds the exact payload, executable.
	got, err := os.ReadFile(a.cfg.AgentB)
	if err != nil {
		t.Fatalf("agent.B not installed: %v", err)
	}
	if string(got) != string(payload) {
		t.Error("agent.B content differs from the uploaded binary")
	}
	if fi, err := os.Stat(a.cfg.AgentB); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("agent.B not executable: mode=%v err=%v", fi.Mode(), err)
	}
	// Flip: the active pointer now names the new slot.
	ab, err := os.ReadFile(a.cfg.AgentActive)
	if err != nil || strings.TrimSpace(string(ab)) != "B" {
		t.Errorf("agent.active = %q err=%v, want B", ab, err)
	}

	// Documented exit: reply first, then a clean exit(0) so startup.sh
	// relaunches the flipped slot.
	if code := waitExit(t, exitCh); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	// The deliberate-exit intent marker keeps the respawn loop from counting
	// this as a crash (and reverting the fresh slot).
	ib, err := os.ReadFile(filepath.Join(a.cfg.OTADir, "agent.intent"))
	if err != nil || strings.TrimSpace(string(ib)) != "update" {
		t.Errorf("agent.intent = %q err=%v, want update", ib, err)
	}
	// Clean-stop path ran before the exit (watchdog disarm + stopped closed).
	select {
	case <-a.stopped:
	default:
		t.Error("Stop() was not called before the exit")
	}
}

func TestAgentUpdateRefusesShaMismatchWithoutExiting(t *testing.T) {
	a := testAgent(t)
	if err := os.MkdirAll(a.cfg.OTADir, 0o755); err != nil {
		t.Fatal(err)
	}
	exitCh := captureExit(t)

	src := filepath.Join(t.TempDir(), "agent.upload")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := fmt.Sprintf(`{"cmd":"agent.update","args":{"src":%q,"sha256":"%s"}}`,
		src, strings.Repeat("0", 64))
	resp := a.Dispatch([]byte(req))
	if resp.OK || !strings.Contains(resp.Err, "sha256 mismatch") {
		t.Errorf("corrupt upload must be refused: %+v", resp)
	}
	if _, err := os.Stat(a.cfg.AgentB); !os.IsNotExist(err) {
		t.Error("refused update must not install into the inactive slot")
	}
	select {
	case code := <-exitCh:
		t.Errorf("refused update must not exit the agent (got exit %d)", code)
	case <-time.After(1200 * time.Millisecond): // > the 750ms exit delay
	}
}

func TestAgentRestartExitsCleanWithIntent(t *testing.T) {
	a := testAgent(t)
	if err := os.MkdirAll(a.cfg.OTADir, 0o755); err != nil {
		t.Fatal(err)
	}
	exitCh := captureExit(t)

	resp := a.Dispatch([]byte(`{"cmd":"agent.restart"}`))
	if !resp.OK {
		t.Fatalf("agent.restart failed: %s", resp.Err)
	}
	data := resp.Data.(map[string]any)
	if data["ok"] != true {
		t.Errorf("restart data = %v", data)
	}

	if code := waitExit(t, exitCh); code != 0 {
		t.Errorf("exit code = %d, want 0 (clean respawn)", code)
	}
	ib, err := os.ReadFile(filepath.Join(a.cfg.OTADir, "agent.intent"))
	if err != nil || strings.TrimSpace(string(ib)) != "restart" {
		t.Errorf("agent.intent = %q err=%v, want restart", ib, err)
	}
	select {
	case <-a.stopped:
	default:
		t.Error("Stop() was not called before the exit")
	}
}
