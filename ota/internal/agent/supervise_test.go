package agent

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"open-sds/ota/internal/fdinherit"
	"open-sds/ota/internal/slots"
)

// writeSlotBinary drops a runnable /bin/sh script into a slot so the real
// launch path (exec + process group + health watcher) can run against it.
func writeSlotBinary(t *testing.T, a *Agent, slot, script string) {
	t.Helper()
	if err := os.MkdirAll(a.store.SlotDir(slot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.store.BinPath(slot), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func appOf(a *Agent) appState {
	a.appMu.Lock()
	defer a.appMu.Unlock()
	return a.app
}

func pausedOf(a *Agent) bool {
	a.appMu.Lock()
	defer a.appMu.Unlock()
	return a.paused
}

// ---- pickSlot ladder --------------------------------------------------------

func TestPickSlotNothingInstalled(t *testing.T) {
	a := testAgent(t)
	if err := a.store.Init(); err != nil {
		t.Fatal(err)
	}
	if slot, em := a.pickSlot(); slot != "" || em {
		t.Errorf("pickSlot = (%q, %v), want empty", slot, em)
	}
}

func TestPickSlotActive(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 0")
	if slot, em := a.pickSlot(); slot != slots.SlotA || em {
		t.Errorf("pickSlot = (%q, %v), want (A, false)", slot, em)
	}
}

func TestPickSlotFallsBackToConfirmed(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 0")
	if err := a.store.SetActive(slots.SlotB); err != nil { // B has no binary
		t.Fatal(err)
	}
	if err := a.store.SetConfirmed(slots.SlotA); err != nil {
		t.Fatal(err)
	}
	if slot, em := a.pickSlot(); slot != slots.SlotA || em {
		t.Errorf("pickSlot = (%q, %v), want confirmed fallback (A, false)", slot, em)
	}
}

func TestPickSlotEmergencyBackstop(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotEmergency, "exit 0")
	if slot, em := a.pickSlot(); slot != slots.SlotEmergency || !em {
		t.Errorf("pickSlot = (%q, %v), want (emergency, true)", slot, em)
	}
}

func TestPickSlotForcedEmergencyWinsOverActive(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 0")
	writeSlotBinary(t, a, slots.SlotEmergency, "exit 0")
	a.appMu.Lock()
	a.useEmergency = true
	a.appMu.Unlock()
	if slot, em := a.pickSlot(); slot != slots.SlotEmergency || !em {
		t.Errorf("pickSlot = (%q, %v), want forced (emergency, true)", slot, em)
	}
	a.clearEmergency()
	if slot, em := a.pickSlot(); slot != slots.SlotA || em {
		t.Errorf("after clearEmergency pickSlot = (%q, %v), want (A, false)", slot, em)
	}
}

// ---- crash-loop counting -> rollback ladder ---------------------------------

func TestRollbackBelowThresholdDoesNothing(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 0")
	writeSlotBinary(t, a, slots.SlotB, "exit 0")
	_ = a.store.SetActive(slots.SlotB)
	_ = a.store.SetConfirmed(slots.SlotA)

	for i := 0; i < a.cfg.MaxFails-1; i++ {
		a.bumpFail("crash")
	}
	a.rollbackIfNeeded(slots.SlotB, false)
	if got := a.store.Active(); got != slots.SlotB {
		t.Errorf("active flipped to %s below the fail threshold", got)
	}
	if f := appOf(a).Fails; f != a.cfg.MaxFails-1 {
		t.Errorf("Fails = %d, want %d (must carry across launches)", f, a.cfg.MaxFails-1)
	}
}

func TestRollbackToConfirmedAtThreshold(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 0")
	writeSlotBinary(t, a, slots.SlotB, "exit 0")
	_ = a.store.SetActive(slots.SlotB)
	_ = a.store.SetConfirmed(slots.SlotA)

	for i := 0; i < a.cfg.MaxFails; i++ {
		a.bumpFail("crash")
	}
	a.rollbackIfNeeded(slots.SlotB, false)
	if got := a.store.Active(); got != slots.SlotA {
		t.Errorf("active = %s, want rollback to confirmed A", got)
	}
	if f := appOf(a).Fails; f != 0 {
		t.Errorf("Fails = %d, want reset to 0 after rollback", f)
	}
}

func TestRollbackToEmergencyWhenConfirmedCrashLoops(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 0") // active == confirmed == A
	writeSlotBinary(t, a, slots.SlotEmergency, "exit 0")

	for i := 0; i < a.cfg.MaxFails; i++ {
		a.bumpFail("crash")
	}
	a.rollbackIfNeeded(slots.SlotA, false)
	a.appMu.Lock()
	forced, fails := a.useEmergency, a.app.Fails
	a.appMu.Unlock()
	if !forced {
		t.Error("confirmed-slot crash loop must force the emergency binary")
	}
	if fails != 0 {
		t.Errorf("Fails = %d, want reset to 0", fails)
	}
	if slot, em := a.pickSlot(); slot != slots.SlotEmergency || !em {
		t.Errorf("pickSlot after ladder = (%q, %v), want emergency", slot, em)
	}
	// Active pointer is untouched — an OTA push clears the flag instead.
	if got := a.store.Active(); got != slots.SlotA {
		t.Errorf("active = %s, emergency fallback must not rewrite pointers", got)
	}
}

func TestRollbackExhaustedKeepsRetrying(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 0") // no other binary anywhere

	for i := 0; i < a.cfg.MaxFails; i++ {
		a.bumpFail("crash")
	}
	a.rollbackIfNeeded(slots.SlotA, false)
	a.appMu.Lock()
	forced, fails := a.useEmergency, a.app.Fails
	a.appMu.Unlock()
	if forced {
		t.Error("no emergency binary exists — must not force it")
	}
	if fails != a.cfg.MaxFails {
		t.Errorf("Fails = %d, want %d (nothing to roll back to; count stands)", fails, a.cfg.MaxFails)
	}
	if got := a.store.Active(); got != slots.SlotA {
		t.Errorf("active = %s, want unchanged A", got)
	}
}

func TestRollbackWhileOnEmergencyOnlyLogs(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotEmergency, "exit 0")
	for i := 0; i < a.cfg.MaxFails; i++ {
		a.bumpFail("crash")
	}
	a.rollbackIfNeeded(slots.SlotEmergency, true)
	a.appMu.Lock()
	forced := a.useEmergency
	a.appMu.Unlock()
	if forced {
		t.Error("emergency slot failing must not re-force emergency (would mask the crash loop)")
	}
}

// ---- orphan adoption --------------------------------------------------------

func TestOrphanAppPid(t *testing.T) {
	a := testAgent(t)
	pidFile := a.pidPath(appPidFile)

	if pid := a.orphanAppPid(); pid != -1 {
		t.Errorf("no pid file: got %d, want -1", pid)
	}
	os.WriteFile(pidFile, []byte("zork\n"), 0o644)
	if pid := a.orphanAppPid(); pid != -1 {
		t.Errorf("garbage pid file: got %d, want -1", pid)
	}
	os.WriteFile(pidFile, []byte("1\n"), 0o644)
	if pid := a.orphanAppPid(); pid != -1 {
		t.Errorf("pid 1 must never be adopted: got %d", pid)
	}
	os.WriteFile(pidFile, []byte("999999999\n"), 0o644) // beyond pid_max, never alive
	if pid := a.orphanAppPid(); pid != -1 {
		t.Errorf("dead pid: got %d, want -1", pid)
	}
	os.WriteFile(pidFile, []byte("  "+itoa64(int64(os.Getpid()))+" \n"), 0o644)
	if pid := a.orphanAppPid(); pid != os.Getpid() {
		t.Errorf("live pid: got %d, want %d", pid, os.Getpid())
	}
}

func TestSuperviseAdoptedTornDownByControl(t *testing.T) {
	a := testAgent(t)
	// A live process standing in for an app left by a previous agent
	// generation. Reap it promptly so /proc/<pid> disappears on SIGTERM.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	pid := cmd.Process.Pid
	// Seed a health token so the adopted app polls healthy and only the
	// control request can end the loop.
	if err := os.WriteFile(a.cfg.HealthPath(), []byte("adopted-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(a.pidPath(appPidFile), []byte(itoa64(int64(pid))+"\n"), 0o644)

	done := make(chan struct{})
	go func() { a.superviseAdopted(pid); close(done) }()

	if err := a.ctlRequest("stop", 5*time.Second); err != nil {
		t.Fatalf("ctl stop on adopted app: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("superviseAdopted did not return after ctl stop")
	}
	if !pausedOf(a) {
		t.Error("stop must pause the supervisor")
	}
	if _, err := os.Stat(a.pidPath(appPidFile)); !os.IsNotExist(err) {
		t.Error("pid file must be removed on teardown")
	}
}

func TestSuperviseAdoptedKillsAfterGraceWhenNoHealthArrives(t *testing.T) {
	t.Setenv("OTA_APP_GRACE", "0.8")
	a := testAgent(t)
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	pid := cmd.Process.Pid
	// No health token ever arrives. The adopted app gets the normal AppGrace
	// from adoption (started = now, NOT a backdated already-expired deadline);
	// only once that grace lapses without a first report is it torn down.
	start := time.Now()
	done := make(chan struct{})
	go func() { a.superviseAdopted(pid); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("unhealthy adopted app was not torn down after grace")
	}
	// A backdated started would have killed it on the very first tick (~500ms,
	// before the 800ms grace). Honoring the grace means it survives past it.
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
		t.Errorf("adopted app torn down after %s — grace was not honored (backdated?)", elapsed)
	}
	st := appOf(a)
	if st.Fails != 1 {
		t.Errorf("Fails = %d, want 1", st.Fails)
	}
	if !strings.HasPrefix(st.LastExit, "adopted:") {
		t.Errorf("LastExit = %q, want an adopted: reason", st.LastExit)
	}
}

// A self-update that adopts an app mid-startup (before its first health token)
// must NOT kill the still-initializing healthy app: it gets the full AppGrace
// from adoption, not a backdated deadline that expires on the first tick.
func TestSuperviseAdoptedGivesStartingAppTheFullGrace(t *testing.T) {
	t.Setenv("OTA_APP_GRACE", "5")
	t.Setenv("OTA_HEALTH_TIMEOUT", "5")
	a := testAgent(t)
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	pid := cmd.Process.Pid
	defer syscall.Kill(pid, syscall.SIGKILL)
	// No token yet: the app is still coming up (withholds the token until >=3
	// coherent frames). Adoption must give it grace, not a spurious failure.
	os.WriteFile(a.pidPath(appPidFile), []byte(itoa64(int64(pid))+"\n"), 0o644)

	done := make(chan struct{})
	go func() { a.superviseAdopted(pid); close(done) }()

	// Comfortably inside the 5s grace: the app must still be alive and uncharged.
	time.Sleep(1500 * time.Millisecond)
	if !fdinherit.Alive(pid) {
		t.Fatal("adopted app was killed during its startup grace window")
	}
	if f := appOf(a).Fails; f != 0 {
		t.Errorf("Fails = %d, want 0 (no spurious failure charged during grace)", f)
	}

	// Tear down cleanly via a control op (the adopted-app teardown path).
	if err := a.ctlRequest("stop", 5*time.Second); err != nil {
		t.Fatalf("ctl stop on adopted app: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("superviseAdopted did not return after ctl stop")
	}
}

// ---- control plumbing -------------------------------------------------------

func TestIdleWaitServicesControlOps(t *testing.T) {
	a := testAgent(t)

	go a.idleWait(5 * time.Second)
	if err := a.ctlRequest("stop", 2*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !pausedOf(a) {
		t.Error("stop must set paused")
	}

	go a.idleWait(5 * time.Second)
	if err := a.ctlRequest("start", 2*time.Second); err != nil {
		t.Fatalf("start: %v", err)
	}
	if pausedOf(a) {
		t.Error("start must clear paused")
	}

	go a.idleWait(5 * time.Second)
	if err := a.ctlRequest("restart", 2*time.Second); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if pausedOf(a) {
		t.Error("restart must clear paused")
	}

	go a.idleWait(5 * time.Second)
	if err := a.ctlRequest("frobnicate", 2*time.Second); err == nil ||
		!strings.Contains(err.Error(), "unknown op") {
		t.Errorf("unknown op error = %v", err)
	}
}

func TestCtlRequestTimesOutWithoutSupervisor(t *testing.T) {
	a := testAgent(t)
	start := time.Now()
	err := a.ctlRequest("stop", 200*time.Millisecond)
	if err == nil {
		t.Fatal("ctlRequest with no supervisor must time out")
	}
	if !strings.Contains(err.Error(), "supervisor") {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("timeout took far longer than requested")
	}
}

// ---- runAppOnce outcome classification (real exec of throwaway scripts) -----

func TestRunAppOnceStableRunConfirmsSlot(t *testing.T) {
	t.Setenv("OTA_STABLE", "0.3")
	t.Setenv("OTA_APP_GRACE", "10")
	t.Setenv("OTA_HEALTH_TIMEOUT", "10")
	a := testAgent(t)
	_ = a.store.Init()
	// App proves liveness by rewriting the token, runs past STABLE, exits 0.
	writeSlotBinary(t, a, slots.SlotB,
		`i=0
while [ $i -lt 8 ]; do
  echo tick$i > "$OTA_HEALTH_PATH"
  i=$((i+1))
  sleep 0.1
done`)
	_ = a.store.SetActive(slots.SlotB)
	_ = a.store.SetConfirmed(slots.SlotA) // so the confirm transition is observable

	a.runAppOnce(slots.SlotB, false)

	if got := a.store.Confirmed(); got != slots.SlotB {
		t.Errorf("confirmed = %s, want B after a stable healthy run", got)
	}
	st := appOf(a)
	if st.Fails != 0 {
		t.Errorf("Fails = %d, want 0", st.Fails)
	}
	if st.LastExit != "exited" {
		t.Errorf("LastExit = %q, want exited", st.LastExit)
	}
}

func TestRunAppOnceCrashCountsAsFailure(t *testing.T) {
	t.Setenv("OTA_APP_GRACE", "10")
	a := testAgent(t)
	_ = a.store.Init()
	writeSlotBinary(t, a, slots.SlotA, "exit 3")

	a.runAppOnce(slots.SlotA, false)

	st := appOf(a)
	if st.Fails != 1 {
		t.Errorf("Fails = %d, want 1", st.Fails)
	}
	if !strings.Contains(st.LastExit, "exit status 3") {
		t.Errorf("LastExit = %q, want it to carry the exit status", st.LastExit)
	}
	if got := a.store.Confirmed(); got != slots.SlotA {
		t.Errorf("confirmed = %s — a crash must not confirm anything", got)
	}
}

func TestRunAppOnceStaleHealthGetsTerminated(t *testing.T) {
	t.Setenv("OTA_APP_GRACE", "10")
	t.Setenv("OTA_HEALTH_TIMEOUT", "0.3")
	t.Setenv("OTA_STABLE", "30")
	a := testAgent(t)
	_ = a.store.Init()
	// One health report, then silence: the watcher must declare it stale and
	// the supervisor must SIGTERM the process group.
	writeSlotBinary(t, a, slots.SlotA,
		`echo alive > "$OTA_HEALTH_PATH"
sleep 30`)

	start := time.Now()
	a.runAppOnce(slots.SlotA, false)
	if time.Since(start) > 15*time.Second {
		t.Error("stale app was not terminated promptly")
	}
	st := appOf(a)
	if st.Fails != 1 {
		t.Errorf("Fails = %d, want 1", st.Fails)
	}
	if !strings.Contains(st.LastExit, "health token stale") {
		t.Errorf("LastExit = %q, want the staleness reason", st.LastExit)
	}
}
