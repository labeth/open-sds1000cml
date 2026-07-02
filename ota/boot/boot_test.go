package boot

// These tests drive the real startup.sh with stub agents and a stub commands
// file via env overrides, then assert on the boot log — the same contract the
// device relies on. They run under /bin/sh to stay POSIX (the device uses
// busybox ash). The agent loop is bounded with OTA_AGENT_RUNS so each test
// terminates.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func scriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("startup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("startup.sh not found: %v", err)
	}
	return p
}

// run executes startup.sh with env and returns the boot log after the agent
// loop has finished (RUNS_LIMIT bounds it). Because the loop is backgrounded
// with `( agent_loop & )`, we poll the log for the terminal marker.
func run(t *testing.T, env map[string]string, wantMarker string, timeout time.Duration) (bootLog, agentLog string) {
	t.Helper()
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	bootLogPath := filepath.Join(logDir, "boot.log")
	agentLogPath := filepath.Join(logDir, "agent.log")

	base := map[string]string{
		"OTA_USB":          dir,
		"OTA_DIR":          filepath.Join(dir, "ota"),
		"OTA_BOOT_LOG":     bootLogPath,
		"OTA_RESPAWN":      "0", // don't sleep between respawns
		"OTA_AGENT_STABLE": "1",
	}
	for k, v := range env {
		base[k] = v
	}
	// OTA_DIR must exist for pointer files unless the test overrides paths.
	_ = os.MkdirAll(base["OTA_DIR"], 0o755)

	cmd := exec.Command("/bin/sh", scriptPath(t))
	cmd.Env = os.Environ()
	for k, v := range base {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("startup.sh failed: %v\n%s", err, out)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(bootLogPath)
		if strings.Contains(string(b), wantMarker) {
			a, _ := os.ReadFile(agentLogPath)
			return string(b), string(a)
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := os.ReadFile(bootLogPath)
	t.Fatalf("marker %q not seen within %s; boot.log:\n%s", wantMarker, timeout, b)
	return "", ""
}

// writeStub writes an executable shell stub.
func writeStub(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRespawnLoopBounded(t *testing.T) {
	dir := t.TempDir()
	otaDir := filepath.Join(dir, "ota")
	agentA := filepath.Join(otaDir, "agent.A")
	writeStub(t, agentA, `echo "hi from A" ; exit 0`) // fast exit

	boot, agent := run(t, map[string]string{
		"OTA_USB":        dir,
		"OTA_DIR":        otaDir,
		"OTA_AGENT_A":    agentA,
		"OTA_AGENT_RUNS": "3",
	}, "agent-loop-stop", 5*time.Second)

	if got := strings.Count(boot, "agent-start slot=A"); got != 3 {
		t.Errorf("expected 3 agent starts, got %d\n%s", got, boot)
	}
	if !strings.Contains(agent, "hi from A") {
		t.Errorf("agent stdout not captured to agent.log:\n%s", agent)
	}
	if !strings.Contains(boot, "agent-fastexit") {
		t.Errorf("fast exit not detected:\n%s", boot)
	}
}

func TestConfirmStableSlot(t *testing.T) {
	dir := t.TempDir()
	otaDir := filepath.Join(dir, "ota")
	agentA := filepath.Join(otaDir, "agent.A")
	// Runs longer than STABLE(=1s) so the slot gets confirmed, then exits.
	writeStub(t, agentA, `sleep 2 ; exit 0`)

	boot, _ := run(t, map[string]string{
		"OTA_USB":          dir,
		"OTA_DIR":          otaDir,
		"OTA_AGENT_A":      agentA,
		"OTA_AGENT_RUNS":   "1",
		"OTA_AGENT_STABLE": "1",
	}, "agent-loop-stop", 8*time.Second)

	if !strings.Contains(boot, "agent-confirmed slot=A") {
		t.Errorf("stable slot A not confirmed:\n%s", boot)
	}
	confirmed, _ := os.ReadFile(filepath.Join(otaDir, "agent.confirmed"))
	if strings.TrimSpace(string(confirmed)) != "A" {
		t.Errorf("confirmed file = %q, want A", confirmed)
	}
}

func TestAgentSelfUpdateActivatesNewSlot(t *testing.T) {
	dir := t.TempDir()
	otaDir := filepath.Join(dir, "ota")
	activeFile := filepath.Join(otaDir, "agent.active")
	agentA := filepath.Join(otaDir, "agent.A")
	agentB := filepath.Join(otaDir, "agent.B")
	// A simulates agent.update: writes B to the active pointer, then exits.
	writeStub(t, agentA, `echo A-ran ; printf 'B\n' > "`+activeFile+`" ; exit 0`)
	writeStub(t, agentB, `echo B-ran ; exit 0`)

	boot, agent := run(t, map[string]string{
		"OTA_USB":          dir,
		"OTA_DIR":          otaDir,
		"OTA_AGENT_A":      agentA,
		"OTA_AGENT_B":      agentB,
		"OTA_AGENT_ACTIVE": activeFile,
		"OTA_AGENT_RUNS":   "3",
	}, "agent-loop-stop", 5*time.Second)

	if !strings.Contains(boot, "agent-active-changed A -> B") {
		t.Errorf("loop did not pick up the flipped active pointer:\n%s", boot)
	}
	if !strings.Contains(boot, "agent-start slot=B") {
		t.Errorf("slot B never launched after self-update:\n%s", boot)
	}
	if !strings.Contains(agent, "B-ran") {
		t.Errorf("new slot binary never ran:\n%s", agent)
	}
}

func TestCrashLoopRevertsToConfirmed(t *testing.T) {
	dir := t.TempDir()
	otaDir := filepath.Join(dir, "ota")
	activeFile := filepath.Join(otaDir, "agent.active")
	confirmedFile := filepath.Join(otaDir, "agent.confirmed")
	agentA := filepath.Join(otaDir, "agent.A")
	agentB := filepath.Join(otaDir, "agent.B")
	writeStub(t, agentA, `echo A-good ; exit 0`)
	writeStub(t, agentB, `echo B-bad ; exit 1`) // always crashes fast

	// Start with B active but A confirmed (a bad B was just pushed).
	if err := os.MkdirAll(otaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(activeFile, []byte("B\n"), 0o644)
	os.WriteFile(confirmedFile, []byte("A\n"), 0o644)

	boot, _ := run(t, map[string]string{
		"OTA_USB":             dir,
		"OTA_DIR":             otaDir,
		"OTA_AGENT_A":         agentA,
		"OTA_AGENT_B":         agentB,
		"OTA_AGENT_ACTIVE":    activeFile,
		"OTA_AGENT_CONFIRMED": confirmedFile,
		"OTA_AGENT_MAXFAILS":  "3",
		"OTA_AGENT_RUNS":      "6",
	}, "agent-loop-stop", 6*time.Second)

	if !strings.Contains(boot, "agent-revert B -> A") {
		t.Errorf("no revert after crash loop:\n%s", boot)
	}
	if !strings.Contains(boot, "agent-start slot=A") {
		t.Errorf("did not fall back to confirmed slot A:\n%s", boot)
	}
	active, _ := os.ReadFile(activeFile)
	if strings.TrimSpace(string(active)) != "A" {
		t.Errorf("active pointer = %q after revert, want A", active)
	}
}

func TestIntentMarkerIsNeutralNoRevert(t *testing.T) {
	// A freshly-activated slot B that exits fast but drops an intent marker
	// each time (a deliberate agent.restart) must NOT be reverted to confirmed
	// A, even past MAXFAILS.
	dir := t.TempDir()
	otaDir := filepath.Join(dir, "ota")
	activeFile := filepath.Join(otaDir, "agent.active")
	confirmedFile := filepath.Join(otaDir, "agent.confirmed")
	agentB := filepath.Join(otaDir, "agent.B")
	if err := os.MkdirAll(otaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// B drops the intent marker (deliberate restart) then exits fast.
	writeStub(t, agentB, `printf 'restart\n' > "`+filepath.Join(otaDir, "agent.intent")+`" ; exit 0`)
	os.WriteFile(activeFile, []byte("B\n"), 0o644)
	os.WriteFile(confirmedFile, []byte("A\n"), 0o644)

	boot, _ := run(t, map[string]string{
		"OTA_USB":             dir,
		"OTA_DIR":             otaDir,
		"OTA_AGENT_B":         agentB,
		"OTA_AGENT_ACTIVE":    activeFile,
		"OTA_AGENT_CONFIRMED": confirmedFile,
		"OTA_AGENT_MAXFAILS":  "3",
		"OTA_AGENT_RUNS":      "5",
	}, "agent-loop-stop", 6*time.Second)

	if strings.Contains(boot, "agent-revert") {
		t.Errorf("deliberate restarts must not revert the slot:\n%s", boot)
	}
	if !strings.Contains(boot, "agent-intent restart") {
		t.Errorf("intent marker not recognized:\n%s", boot)
	}
	active, _ := os.ReadFile(activeFile)
	if strings.TrimSpace(string(active)) != "B" {
		t.Errorf("active = %q, want B preserved", active)
	}
}

func TestCommandsFileRuns(t *testing.T) {
	dir := t.TempDir()
	otaDir := filepath.Join(dir, "ota")
	agentA := filepath.Join(otaDir, "agent.A")
	writeStub(t, agentA, `exit 0`)
	marker := filepath.Join(dir, "commands-ran")
	writeStub(t, filepath.Join(dir, "commands"), `touch "`+marker+`"`)

	run(t, map[string]string{
		"OTA_USB":        dir,
		"OTA_DIR":        otaDir,
		"OTA_AGENT_A":    agentA,
		"OTA_AGENT_RUNS": "1",
	}, "boot-done", 5*time.Second)

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("commands file did not run: %v", err)
	}
}
