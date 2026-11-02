// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/labeth/open-sds1000cml/ota/internal/fdinherit"
)

// superviseLoop is the app lifecycle state machine. It only acts once the
// device is taken over. Order of preference each round:
//
//  1. adopt an orphan app left by a previous agent generation (agent
//     self-update must not restart a healthy app),
//  2. launch the active slot binary as a direct child so it inherits the
//     boot fds (spec 01 §2.3),
//  3. classify the outcome; stable+healthy confirms the slot, repeated
//     failure rolls back active→confirmed.
//
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) superviseLoop() {
	for {
		select {
		case <-a.stopped:
			return
		default:
		}
		st := a.st.get()
		a.appMu.Lock()
		paused := a.paused
		a.appMu.Unlock()
		if !st.TakenOver || paused {
			a.idleWait(time.Second)
			continue
		}

		// Bus-ownership safety gate (spec 01 §3: exactly one GPMC owner).
		// Never exec the clean-room app while a factory process still holds
		// /dev/Gpmc — this covers a crash in the takeover persist→kill window
		// (a taken_over restart would otherwise launch straight onto a live
		// factory bus) and an init-respawned factory app. Re-drive the kill
		// first; two live bus owners wedge the device (power-cycle only).
		if cands := a.factoryCandidates(); len(cands) > 0 {
			a.log.Printf("supervise: %d factory bus holder(s) still present; reclaiming the bus before launch", len(cands))
			a.reclaimBus()
			a.idleWait(time.Second)
			continue
		}

		if pid := a.orphanAppPid(); pid > 0 {
			a.superviseAdopted(pid)
			continue
		}

		slot := a.pickSlot()
		if slot == "" {
			a.setAppState(func(s *appState) {
				*s = appState{Running: false, LastExit: "no app binary in any slot"}
			})
			a.idleWait(2 * time.Second)
			continue
		}
		a.runAppOnce(slot)
	}
}

// idleWait sleeps but still services control requests so app.start / restart
// respond while idle/paused.
// TRLC-LINKS: REQ-SDS-117
func (a *Agent) idleWait(d time.Duration) {
	select {
	case <-a.stopped:
	case m := <-a.ctl:
		switch m.op {
		case opStart:
			a.appMu.Lock()
			a.paused = false
			a.appMu.Unlock()
			m.reply <- nil
		case opStop:
			a.appMu.Lock()
			a.paused = true
			a.appMu.Unlock()
			m.reply <- nil
		case opRestart:
			a.appMu.Lock()
			a.paused = false
			a.appMu.Unlock()
			m.reply <- nil
		default:
			m.reply <- fmt.Errorf("unknown op %q", m.op)
		}
	case <-time.After(d):
	}
}

// pickSlot chooses what to run: the active slot, else the confirmed slot.
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) pickSlot() string {
	active := a.store.Active()
	if a.store.HasBinary(active) {
		return active
	}
	if conf := a.store.Confirmed(); conf != active && a.store.HasBinary(conf) {
		a.log.Printf("active slot %s has no binary; falling back to confirmed %s", active, conf)
		return conf
	}
	return ""
}

// orphanAppPid returns a live app pid from a previous agent generation.
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) orphanAppPid() int {
	b, err := os.ReadFile(a.pidPath(appPidFile))
	if err != nil {
		return -1
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 || !fdinherit.Alive(pid) {
		return -1
	}
	return pid
}

// superviseAdopted monitors an app we cannot wait() on (it reparented to init
// when the previous agent exited). Health verdicts still apply; any failure
// or control request tears it down so the normal child path takes over.
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) superviseAdopted(pid int) {
	a.log.Printf("adopted running app pid=%d from previous agent generation", pid)
	h := &healthWatcher{path: a.cfg.HealthPath(), started: time.Now().Add(-time.Hour)}
	// The adopted app may have reported healthy long ago; seed by polling once
	// and treating existing content as the baseline. First change marks it
	// healthy; absence of change is judged against staleness from now.
	h.poll()
	h.healthyOnce = h.lastSig.seen
	h.lastChange = time.Now()
	a.setAppState(func(s *appState) {
		*s = appState{Running: true, Adopted: true, PID: pid, StartedAt: time.Now().Format(time.RFC3339)}
	})

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-a.stopped:
			return
		case m := <-a.ctl:
			// Any control op on an adopted app tears it down first.
			a.log.Printf("control %q on adopted app: terminating pid=%d", m.op, pid)
			a.killPid(pid)
			_ = os.Remove(a.pidPath(appPidFile))
			if m.op == opStop {
				a.appMu.Lock()
				a.paused = true
				a.appMu.Unlock()
			}
			m.reply <- nil
			return
		case <-tick.C:
			if !fdinherit.Alive(pid) {
				a.log.Printf("adopted app pid=%d exited", pid)
				_ = os.Remove(a.pidPath(appPidFile))
				a.bumpFail("adopted app exited")
				return
			}
			h.poll()
			a.setAppState(func(s *appState) { s.Health = h.status() })
			if ok, why := h.verdict(a.cfg.AppGrace, a.cfg.HealthTimeout); !ok {
				a.log.Printf("adopted app unhealthy (%s): terminating pid=%d", why, pid)
				a.killPid(pid)
				_ = os.Remove(a.pidPath(appPidFile))
				a.bumpFail("adopted: " + why)
				return
			}
		}
	}
}

// runAppOnce launches one app generation and blocks until it ends, then
// classifies the outcome (confirm / fail / rollback).
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) runAppOnce(slot string) {
	bin := a.store.BinPath(slot)
	h := newHealthWatcher(a.cfg.HealthPath())
	a.confirmedRun, a.confirmFailed = "", ""

	cmd := exec.Command(bin) //nolint:noctx,gosec // the app outlives this call (it is adopted across agent generations); bin is the store's slot binary
	cmd.Dir = a.store.SlotDir(slot)
	cmd.Stdout = os.Stdout // agent stdout -> agent.log via startup.sh
	cmd.Stderr = os.Stderr
	// The agent<->app runtime contract (spec 01 §2.3). The boot fds (Gpmc,
	// fpga_key) carry no CLOEXEC flag — they were opened by the boot chain —
	// so the child inherits them without ExtraFiles plumbing.
	cmd.Env = append(os.Environ(),
		"OTA_HEALTH_PATH="+a.cfg.HealthPath(),
		"SCOPE_GPMC="+a.cfg.GpmcDev,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		a.log.Printf("app start %s: %v", bin, err)
		a.bumpFail("start: " + err.Error())
		a.idleWait(2 * time.Second)
		return
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(a.pidPath(appPidFile), fmt.Appendf(nil, "%d\n", pid), 0o600)
	a.log.Printf("app started slot=%s pid=%d bin=%s", slot, pid, bin)
	a.appMu.Lock()
	prevFails := a.app.Fails // carry the consecutive-failure count across launches
	a.app = appState{
		Running: true, PID: pid, Slot: slot,
		StartedAt: time.Now().Format(time.RFC3339), Fails: prevFails,
	}
	a.appMu.Unlock()
	a.event("app.start", map[string]any{keySlot: slot, "pid": pid})

	exit := make(chan error, 1)
	go func() { exit <- cmd.Wait() }()

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var exitErr error
	reason := ""
loop:
	for {
		select {
		case <-a.stopped:
			// Agent clean stop: leave the app running for adoption.
			return
		case exitErr = <-exit:
			reason = "exited"
			break loop
		case m := <-a.ctl:
			reason = "ctl:" + string(m.op)
			a.terminate(cmd, exit)
			if m.op == opStop {
				a.appMu.Lock()
				a.paused = true
				a.appMu.Unlock()
			}
			m.reply <- nil
			break loop
		case <-tick.C:
			h.poll()
			hs := h.status()
			a.setAppState(func(s *appState) { s.Health = hs })
			if ok, why := h.verdict(a.cfg.AppGrace, a.cfg.HealthTimeout); !ok {
				reason = "unhealthy: " + why
				a.terminate(cmd, exit)
				break loop
			}
			// A slot that has run healthy for STABLE seconds is confirmed while it
			// still runs: an app normally runs until the next update restarts it,
			// and that update overwrites the other slot. Confirming only at exit
			// would leave the pointer on the slot the update is about to
			// overwrite, so a crash-looping update could not roll back to the
			// version that ran before it.
			a.confirmIfStable(slot, hs, time.Since(start))
		}
	}

	ran := time.Since(start)
	_ = os.Remove(a.pidPath(appPidFile))
	hs := h.status()
	a.log.Printf("app ended slot=%s ran=%s reason=%s exit=%v healthy_once=%v",
		slot, ran.Round(time.Millisecond), reason, exitErr, hs.HealthyOnce)

	switch {
	case strings.HasPrefix(reason, "ctl:"):
		// Operator action, not a failure.
		a.setAppState(func(s *appState) { *s = appState{LastExit: reason, Fails: 0} })
	case hs.HealthyOnce && ran >= a.cfg.Stable:
		a.confirmIfStable(slot, hs, ran)
		a.setAppState(func(s *appState) { *s = appState{LastExit: reason, Fails: 0} })
		a.idleWait(time.Second) // stable run that ended: relaunch after a beat
	default:
		a.bumpFail(fmt.Sprintf("%s (exit=%v, ran=%s)", reason, exitErr, ran.Round(time.Second)))
		a.rollbackIfNeeded(slot)
		a.idleWait(2 * time.Second)
	}
}

// confirmIfStable points the confirmed slot at slot once it has been healthy
// for STABLE seconds.
//
// The slot found confirmed is remembered for the run (confirmedRun, set by
// the supervisor alone, the pointer's only writer): otherwise a healthy
// app's every tick reads both pointer files off the stick. A write that fails
// (the stick flipped read-only) is not remembered: the next tick tries again,
// and the run's first failure is logged and sent once - a confirmed pointer
// left on the old slot is the slot the next update overwrites.
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) confirmIfStable(slot string, hs healthStatus, ran time.Duration) {
	if !hs.HealthyOnce || ran < a.cfg.Stable || a.confirmedRun == slot {
		return
	}
	if a.store.Confirmed() != slot {
		if err := a.store.SetConfirmed(slot); err != nil {
			if a.confirmFailed != slot {
				a.confirmFailed = slot
				a.log.Printf("confirm %s: %v (retrying)", slot, err)
				a.event("app.confirm_failed", map[string]any{keySlot: slot, "error": err.Error()})
			}
			return
		}
		a.event("app.confirmed", map[string]any{keySlot: slot})
	}
	a.confirmedRun, a.confirmFailed = slot, ""
}

// terminate asks the app to land the engine (SIGTERM), then SIGKILLs the
// process group after a grace period.
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) terminate(cmd *exec.Cmd, exit chan error) {
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM) // whole group
	select {
	case <-exit: //nolint:errcheck // a terminated app is classified by its reason, not its exit status
		return
	case <-time.After(3 * time.Second):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	select {
	case <-exit: //nolint:errcheck // as above
	case <-time.After(2 * time.Second):
	}
}

// TRLC-LINKS: REQ-SDS-028
func (a *Agent) killPid(pid int) {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !fdinherit.Alive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// TRLC-LINKS: REQ-SDS-028
func (a *Agent) bumpFail(why string) {
	a.appMu.Lock()
	a.app.Fails++
	a.app.Running = false
	a.app.PID = 0
	a.app.LastExit = why
	a.appMu.Unlock()
}

// rollbackIfNeeded applies the recovery ladder after MaxFails consecutive
// failures: active→confirmed; when the confirmed slot itself is the failure
// the loop keeps retrying with backoff — the agent itself stays up so the OTA
// path can push a fix.
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) rollbackIfNeeded(slot string) {
	a.appMu.Lock()
	fails := a.app.Fails
	a.appMu.Unlock()
	if fails < a.cfg.MaxFails {
		return
	}
	confirmed := a.store.Confirmed()
	if slot != confirmed && a.store.HasBinary(confirmed) {
		a.log.Printf("rollback: %s failed %d times -> reverting to confirmed %s", slot, fails, confirmed)
		_ = a.store.SetActive(confirmed)
		a.event("app.rollback", map[string]any{"from": slot, "to": confirmed, "fails": fails})
		a.appMu.Lock()
		a.app.Fails = 0
		a.appMu.Unlock()
		return
	}
	a.event("app.crash_loop", map[string]any{keySlot: slot, "fails": fails})
}

// clearFails starts the failure count over (after an OTA push).
// TRLC-LINKS: REQ-SDS-028
func (a *Agent) clearFails() {
	a.appMu.Lock()
	a.app.Fails = 0
	a.appMu.Unlock()
}

// TRLC-LINKS: REQ-SDS-028
func (a *Agent) setAppState(fn func(*appState)) {
	a.appMu.Lock()
	fn(&a.app)
	a.appMu.Unlock()
}

// ctlRequest sends a control op to the supervisor and waits.
// TRLC-LINKS: REQ-SDS-117
func (a *Agent) ctlRequest(op ctlOp, timeout time.Duration) error {
	m := ctlMsg{op: op, reply: make(chan error, 1)}
	select {
	case a.ctl <- m:
	case <-time.After(timeout):
		return errors.New("supervisor busy")
	}
	select {
	case err := <-m.reply:
		return err
	case <-time.After(timeout):
		return errors.New("supervisor did not answer")
	}
}
