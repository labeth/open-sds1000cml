package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"open-sds/ota/internal/fdinherit"
	"open-sds/ota/internal/gpmc"
	"open-sds/ota/internal/sysinfo"
	"open-sds/ota/internal/vxi11"
)

// Takeover implements the spec 01 §2.2 inherit-then-kill sequence. The engine
// must never be killed mid-frame (that freezes it on the GPMC WAIT line —
// power-cycle only), so the landing is manufactured:
//
//  1. guards: inherited /dev/Gpmc fd present (we are the fd holder that keeps
//     the chip select alive across the kill), not already done
//  2. identify the factory processes holding /dev/Gpmc (dry-run reports this
//     and stops)
//  3. drive the factory app to STOP over its own VXI-11 SCPI service — STOP
//     halts the engine at every timebase (spec 01 §6)
//  4. confirm the idle landing on the inherited fd: version 0x12 == 0x0052
//     AND fill counter 0x46 frozen across consecutive reads. These reads
//     happen while the factory app is still alive, post-STOP.
//  5. persist taken_over=true BEFORE the kill (a crashed agent's successor
//     must re-acquire the watchdog immediately, or the SoC warm-resets in
//     ~60 s and the USB/OTA path is lost until a physical power-cycle)
//  6. SIGKILL the factory tree; re-kill respawns for a few rounds
//  7. acquire + pet /dev/watchdog (retrying while the factory fd drains)
//  8. re-assert the LAN address if the kill dropped it
//
// After this returns the supervisor loop launches the app slot (if any).
type TakeoverOpts struct {
	DryRun bool `json:"dry_run"`
	// Force skips the VXI-11 STOP + idle-confirm gates. Only for a unit whose
	// factory app is already dead/hung and unreachable. The engine may be
	// mid-frame: expect a possible wedge; have the power plug ready.
	Force bool `json:"force"`
}

type TakeoverResult struct {
	OK         bool               `json:"ok"`
	Steps      []string           `json:"steps"`
	Candidates []fdinherit.Holder `json:"candidates"`
	Err        string             `json:"err,omitempty"`
}

func (r *TakeoverResult) step(format string, args ...any) {
	r.Steps = append(r.Steps, fmt.Sprintf(format, args...))
}

func (r *TakeoverResult) Summary() string { return strings.Join(r.Steps, "; ") }

// shellish exe basenames are infrastructure (our own boot chain / shells)
// that may hold the inherited fds but are not the factory app.
var shellish = map[string]bool{
	"sh": true, "ash": true, "hush": true, "bash": true, "busybox": true,
	"login": true, "getty": true, "init": true,
}

// factoryCandidates returns the /dev/Gpmc holders that look like the vendor
// firmware: not us, not our descendants, not pid 1, not shells. The vendor
// launcher may be our ancestor (it ran startup.sh) — ancestors are included.
func (a *Agent) factoryCandidates() []fdinherit.Holder {
	self := os.Getpid()
	desc := fdinherit.DescendantsOfSelf()
	ourExe, _ := os.Executable()
	ourExe, _ = filepath.EvalSymlinks(ourExe)

	var out []fdinherit.Holder
	for _, h := range fdinherit.HoldersOf(a.cfg.GpmcDev) {
		if h.PID == self || h.PID == 1 || desc[h.PID] {
			continue
		}
		base := filepath.Base(h.Exe)
		if h.Exe != "" {
			if rp, err := filepath.EvalSymlinks(h.Exe); err == nil && rp == ourExe {
				continue // another agent generation
			}
		}
		if shellish[base] && !a.matchesFactoryName(h) {
			continue
		}
		out = append(out, h)
	}
	return out
}

func (a *Agent) matchesFactoryName(h fdinherit.Holder) bool {
	for _, n := range a.cfg.FactoryNames {
		if n != "" && (strings.Contains(h.Comm, n) || strings.Contains(h.Exe, n)) {
			return true
		}
	}
	return false
}

func (a *Agent) Takeover(opts TakeoverOpts) *TakeoverResult {
	a.tkMu.Lock()
	defer a.tkMu.Unlock()
	res := &TakeoverResult{}

	if a.st.get().TakenOver {
		res.OK = true
		res.step("already taken over (idempotent)")
		// Make sure the watchdog side is running (restart path does this too).
		go a.acquireWatchdogForever()
		return res
	}

	// Gate 1: we must hold the inherited fd or the chip select dies with the
	// factory app (release() frees the CS on the last close).
	if a.gpmcFD < 0 {
		res.Err = "no inherited /dev/Gpmc fd in this process — takeover would drop the chip select; check the boot chain"
		res.step("gate: inherited fd MISSING")
		return res
	}
	res.step("gate: inherited gpmc fd=%d", a.gpmcFD)

	res.Candidates = a.factoryCandidates()
	for _, h := range res.Candidates {
		res.step("candidate pid=%d comm=%s exe=%s", h.PID, h.Comm, h.Exe)
	}
	if opts.DryRun {
		res.OK = true
		res.step("dry-run: stopping here (no STOP sent, nothing killed)")
		return res
	}

	// Pre-kill network snapshot for post-kill re-assertion.
	preIPs := sysinfo.IPv4s()
	res.step("pre-kill IPs: %v", preIPs)

	// Gate 2: drive the factory app to STOP via its own SCPI service.
	if len(res.Candidates) > 0 {
		if err := a.factoryStop(); err != nil {
			res.step("VXI-11 STOP failed: %v", err)
			if !opts.Force {
				res.Err = "could not command factory STOP (use force only if the factory app is dead): " + err.Error()
				return res
			}
			res.step("force: continuing without STOP")
		} else {
			res.step("factory STOP sent")
		}
	} else {
		res.step("no factory candidates hold %s — engine assumed idle/absent", a.cfg.GpmcDev)
	}

	// Gate 3: idle landing confirmed on the inherited fd (post-STOP reads).
	if err := a.confirmIdle(res, 12*time.Second); err != nil {
		if !opts.Force {
			res.Err = "idle landing not confirmed: " + err.Error()
			return res
		}
		res.step("force: continuing without idle confirm (%v)", err)
	}

	// Point of no return: persist first (see doc comment).
	if err := a.st.update(func(s *State) { s.TakenOver = true }); err != nil {
		res.Err = "persist state: " + err.Error()
		return res
	}
	res.step("state persisted: taken_over=true")

	// Kill the factory tree; fight init respawns for a few rounds.
	killed := a.killFactory(res)
	res.step("killed %d factory processes", killed)

	// Watchdog: from this moment nothing else pets it.
	if err := a.wd.Acquire(15*time.Second, a.cfg.WdPet); err != nil {
		res.step("watchdog acquire failed (%v) — background retry armed", err)
		go a.acquireWatchdogForever()
	} else {
		res.step("watchdog acquired, petting every %s", a.cfg.WdPet)
	}

	// Network re-assert (async): the kill may take the vendor's network
	// management with it; the address usually persists in the kernel, but if
	// it drops, remote access dies with it.
	go a.reassertNetwork(preIPs)

	a.event("takeover.done", map[string]any{"killed": killed})
	res.OK = true
	res.step("takeover complete — supervisor will launch the app slot if present")
	return res
}

// factoryStop speaks the factory app's own VXI-11 SCPI (spec 11): both the
// momentary STOP verb and TRMD STOP for good measure. Always destroy_link.
func (a *Agent) factoryStop() error {
	cl, err := vxi11.Dial("127.0.0.1", 5*time.Second)
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := cl.Send("STOP"); err != nil {
		return err
	}
	_ = cl.Send("TRMD STOP") // belt-and-suspenders; harmless if redundant
	// Read the acquisition status back if possible (diagnostic only).
	if s, err := cl.Query("SAST?"); err == nil {
		a.log.Printf("factory SAST? -> %q", strings.TrimSpace(s))
	}
	return nil
}

// confirmIdle polls until version 0x12 reads 0x0052 AND the fill counter
// 0x46 is frozen across 3 consecutive pairs 50 ms apart (spec 01 §6: the
// reliable halted signal is the frozen fill counter, not a status bit).
func (a *Agent) confirmIdle(res *TakeoverResult, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		v, ok := a.gpmc.VerifyVersion()
		if !ok {
			lastErr = fmt.Errorf("version 0x12 read 0x%04x, want 0x%04x", v, gpmc.VersionMagic)
			time.Sleep(300 * time.Millisecond)
			continue
		}
		frozen, seen, err := a.gpmc.FillFrozen(3, 50*time.Millisecond)
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if frozen {
			if st, err := a.gpmc.Read(gpmc.PlaneCS1, gpmc.SelStatus); err == nil {
				res.step("idle confirmed: version=0x0052 fill frozen at %v status38=0x%04x", seen, st)
			} else {
				res.step("idle confirmed: version=0x0052 fill frozen at %v", seen)
			}
			return nil
		}
		lastErr = fmt.Errorf("fill counter still advancing: %v", seen)
		time.Sleep(300 * time.Millisecond)
	}
	return lastErr
}

// killFactory SIGKILLs the candidates and re-scans a few rounds in case init
// respawns the vendor app.
func (a *Agent) killFactory(res *TakeoverResult) int {
	killed := 0
	for round := 0; round < 4; round++ {
		cands := a.factoryCandidates()
		if round == 0 {
			cands = res.Candidates // use the reported set for the first pass
		}
		if len(cands) == 0 {
			return killed
		}
		for _, h := range cands {
			if err := syscall.Kill(h.PID, syscall.SIGKILL); err == nil {
				killed++
				res.step("SIGKILL pid=%d comm=%s (round %d)", h.PID, h.Comm, round+1)
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	if left := a.factoryCandidates(); len(left) > 0 {
		res.step("WARNING: %d factory holder(s) survived/respawned: %v", len(left), left)
	}
	return killed
}

// reassertNetwork restores the pre-kill IPv4 config if it disappears after
// the factory kill (the vendor app may own network management). Uses busybox
// ifconfig from the device's BusyBox userland.
func (a *Agent) reassertNetwork(preIPs []string) {
	for _, wait := range []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second} {
		time.Sleep(wait)
		now := map[string]bool{}
		for _, s := range sysinfo.IPv4s() {
			now[s] = true
		}
		for _, want := range preIPs {
			if now[want] {
				continue
			}
			// want is "ifname ip/prefix"
			var ifname, cidr string
			if _, err := fmt.Sscanf(want, "%s %s", &ifname, &cidr); err != nil {
				continue
			}
			ip, mask, ok := splitCIDR(cidr)
			if !ok {
				continue
			}
			a.log.Printf("network re-assert: %s lost %s — reconfiguring", ifname, cidr)
			out, err := a.runShell(fmt.Sprintf("ifconfig %s %s netmask %s up", ifname, ip, mask), 10*time.Second)
			a.log.Printf("ifconfig: err=%v out=%s", err, strings.TrimSpace(string(out)))
			a.event("net.reassert", map[string]any{"if": ifname, "ip": ip, "err": fmt.Sprint(err)})
		}
	}
}

func splitCIDR(cidr string) (ip, mask string, ok bool) {
	i := strings.IndexByte(cidr, '/')
	if i < 0 {
		return "", "", false
	}
	ip = cidr[:i]
	var bits int
	if _, err := fmt.Sscanf(cidr[i+1:], "%d", &bits); err != nil || bits < 0 || bits > 32 {
		return "", "", false
	}
	v := ^uint32(0) << (32 - bits)
	if bits == 0 {
		v = 0
	}
	mask = fmt.Sprintf("%d.%d.%d.%d", byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	return ip, mask, true
}
