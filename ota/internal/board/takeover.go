// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/labeth/open-dso-core/supervisor/agent"
	"github.com/labeth/open-dso-core/supervisor/fdinherit"
	"github.com/labeth/open-dso-core/supervisor/sysinfo"
	"github.com/labeth/open-dso-core/supervisor/vxi11"

	"github.com/labeth/open-sds1000cml/ota/internal/gpmc"
)

// Test seams: production values are the real implementations and every call
// site behaves exactly as before. They exist because the factory SCPI service
// sits behind the fixed (privileged) portmapper port 111, the idle window is
// multiple seconds, and reassertNetwork is a fire-and-forget goroutine —
// none of which an off-device test can observe otherwise.
var (
	vxi11Dial           = vxi11.Dial
	takeoverIdleTimeout = 12 * time.Second
	reassertNetworkFn   = (*Board).reassertNetwork
)

// shellish exe basenames are infrastructure (our own boot chain / shells)
// that may hold the inherited fds but are not the factory app.
var shellish = map[string]bool{
	"sh": true, "ash": true, "hush": true, "bash": true, "busybox": true,
	"login": true, "getty": true, "init": true,
}

// factoryCandidates returns the /dev/Gpmc holders that look like the vendor
// firmware: not us, not our descendants, not pid 1, not shells. The vendor
// launcher may be our ancestor (it ran startup.sh) — ancestors are included.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) factoryCandidates() []fdinherit.Holder {
	self := os.Getpid()
	ourExe, _ := os.Executable()
	ourExe, _ = filepath.EvalSymlinks(ourExe)
	// Our own apps run from the slot root (A, B): one a previous
	// agent generation left running is adopted (superviseLoop), not killed.
	slotRoot, err := filepath.EvalSymlinks(b.cfg.SlotRoot)
	if err != nil {
		slotRoot = filepath.Clean(b.cfg.SlotRoot)
	}

	var out []fdinherit.Holder
	for _, h := range fdinherit.HoldersOf(b.bc.GpmcDev) {
		if h.PID == self || h.PID == 1 || descendsFrom(h.PID, self) {
			continue
		}
		base := filepath.Base(h.Exe)
		if h.Exe != "" {
			if rp, err := filepath.EvalSymlinks(h.Exe); err == nil && rp == ourExe {
				continue // another agent generation
			} else if err == nil && strings.HasPrefix(rp, slotRoot+string(filepath.Separator)) {
				continue // our app, left by another agent generation
			}
		}
		if shellish[base] && !b.matchesFactoryName(h) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// descendsFrom reports whether pid is a descendant of anc, up pid's parent
// chain: a few status reads for a holder, where mapping every process's
// children read the status of all of them.
// TRLC-LINKS: REQ-SDS-027
func descendsFrom(pid, anc int) bool {
	for i := 0; i < 64 && pid > 1; i++ { // bounded against /proc races
		if pid = fdinherit.PPid(pid); pid == anc {
			return true
		}
	}
	return false
}

// TRLC-LINKS: REQ-SDS-027
func (b *Board) matchesFactoryName(h fdinherit.Holder) bool {
	for _, n := range b.bc.FactoryNames {
		if n != "" && (strings.Contains(h.Comm, n) || strings.Contains(h.Exe, n)) {
			return true
		}
	}
	return false
}

// VendorHolders is the factory processes holding /dev/Gpmc.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) VendorHolders() []fdinherit.Holder { return b.factoryCandidates() }

// Land is the spec 01 §2.2 inherit-then-kill sequence up to its point of no
// return. The engine must never be killed mid-frame (that freezes it on the
// GPMC WAIT line — power-cycle only), so the landing is manufactured:
//
//  1. guard: inherited /dev/Gpmc fd present (we are the fd holder that keeps
//     the chip select alive across the kill)
//  2. identify the factory processes holding /dev/Gpmc (dry-run reports this
//     and stops)
//  3. drive the factory app to STOP over its own VXI-11 SCPI service — STOP
//     halts the engine at every timebase (spec 01 §6)
//  4. confirm the idle landing on the inherited fd: version 0x12 == 0x0052
//     AND fill counter 0x46 frozen across consecutive reads. These reads
//     happen while the factory app is still alive, post-STOP.
//
// The supervisor then persists taken_over, calls Takeover (the kill and the
// network re-assert) and acquires the watchdog.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) Land(res *agent.TakeoverResult, opts agent.TakeoverOpts) bool {
	// Gate 1: we must hold the inherited fd or the chip select dies with the
	// factory app (release() frees the CS on the last close).
	if b.gpmcFD < 0 {
		res.Err = "no inherited /dev/Gpmc fd in this process — takeover would drop the chip select; check the boot chain"
		res.Step("gate: inherited fd MISSING")
		return false
	}
	res.Step("gate: inherited gpmc fd=%d", b.gpmcFD)

	res.Candidates = b.factoryCandidates()
	for _, h := range res.Candidates {
		res.Step("candidate pid=%d comm=%s exe=%s", h.PID, h.Comm, h.Exe)
	}
	if opts.DryRun {
		res.OK = true
		res.Step("dry-run: stopping here (no STOP sent, nothing killed)")
		return false
	}

	// Pre-kill network snapshot for post-kill re-assertion.
	b.preIPs = sysinfo.IPv4s()
	res.Step("pre-kill IPs: %v", b.preIPs)

	// Gate 2: drive the factory app to STOP via its own SCPI service.
	if !b.stopGate(res, opts.Force) {
		return false
	}

	// Gate 3: idle landing confirmed on the inherited fd (post-STOP reads).
	if err := b.confirmIdle(res, takeoverIdleTimeout); err != nil {
		if !opts.Force {
			res.Err = "idle landing not confirmed: " + err.Error()
			return false
		}
		res.Step("force: continuing without idle confirm (%v)", err)
	}
	return true
}

// stopGate sends the factory app STOP when it holds the bus, and reports
// whether the takeover may go on: a failed STOP stops it unless forced.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) stopGate(res *agent.TakeoverResult, force bool) bool {
	if len(res.Candidates) == 0 {
		res.Step("no factory candidates hold %s — engine assumed idle/absent", b.bc.GpmcDev)
		return true
	}
	err := b.factoryStop()
	if err == nil {
		res.Step("factory STOP sent")
		return true
	}
	res.Step("VXI-11 STOP failed: %v", err)
	if !force {
		res.Err = "could not command factory STOP (use force only if the factory app is dead): " + err.Error()
		return false
	}
	res.Step("force: continuing without STOP")
	return true
}

// Reclaim re-drives the takeover kill sequence when a factory process is
// found still holding /dev/Gpmc after the device is already marked
// taken_over (a crash in the persist→kill window, or an init-respawned
// factory app). It is the idempotent tail of the takeover: STOP →
// idle-confirm → kill.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) Reclaim(res *agent.TakeoverResult) {
	if b.gpmcFD < 0 {
		b.logf("reclaimBus: no inherited gpmc fd; killing factory holders without idle-confirm")
	} else {
		if err := b.factoryStop(); err != nil {
			b.logf("reclaimBus: factory STOP failed: %v", err)
		}
		if err := b.confirmIdle(res, 8*time.Second); err != nil {
			b.logf("reclaimBus: idle-confirm: %v (killing anyway — already taken over)", err)
		}
	}
	b.killFactory(res)
}

// factoryStop speaks the factory app's own VXI-11 SCPI (spec 11): both the
// momentary STOP verb and TRMD STOP for good measure. Always destroy_link.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) factoryStop() error {
	cl, err := vxi11Dial("127.0.0.1", 5*time.Second)
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
		b.logf("factory SAST? -> %q", strings.TrimSpace(s))
	}
	return nil
}

// confirmIdle polls until version 0x12 reads 0x0052 AND the fill counter
// 0x46 is frozen across 3 consecutive pairs 50 ms apart (spec 01 §6: the
// reliable halted signal is the frozen fill counter, not a status bit).
// TRLC-LINKS: REQ-SDS-027
func (b *Board) confirmIdle(res *agent.TakeoverResult, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		v, ok := b.gpmc.VerifyVersion()
		if !ok {
			lastErr = fmt.Errorf("version 0x12 read 0x%04x, want 0x%04x", v, gpmc.VersionMagic)
			time.Sleep(300 * time.Millisecond)
			continue
		}
		frozen, seen, err := b.gpmc.FillFrozen(3, 50*time.Millisecond)
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if frozen {
			if st, err := b.gpmc.Read(gpmc.PlaneCS1, gpmc.SelStatus); err == nil {
				res.Step("idle confirmed: version=0x0052 fill frozen at %v status38=0x%04x", seen, st)
			} else {
				res.Step("idle confirmed: version=0x0052 fill frozen at %v", seen)
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
// TRLC-LINKS: REQ-SDS-027
func (b *Board) killFactory(res *agent.TakeoverResult) int {
	killed := 0
	for round := range 4 {
		cands := b.factoryCandidates()
		if round == 0 {
			cands = res.Candidates // use the reported set for the first pass
		}
		if len(cands) == 0 {
			return killed
		}
		for _, h := range cands {
			if err := syscall.Kill(h.PID, syscall.SIGKILL); err == nil {
				killed++
				res.Step("SIGKILL pid=%d comm=%s (round %d)", h.PID, h.Comm, round+1)
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	if left := b.factoryCandidates(); len(left) > 0 {
		res.Step("WARNING: %d factory holder(s) survived/respawned: %v", len(left), left)
	}
	return killed
}

// reassertNetwork restores the pre-kill IPv4 config if it disappears after
// the factory kill (the vendor app may own network management). Uses busybox
// ifconfig from the device's BusyBox userland.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) reassertNetwork(preIPs []string) {
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
			b.logf("network re-assert: %s lost %s — reconfiguring", ifname, cidr)
			out, err := agent.RunShell(fmt.Sprintf("ifconfig %s %s netmask %s up", ifname, ip, mask), 10*time.Second)
			b.logf("ifconfig: err=%v out=%s", err, strings.TrimSpace(string(out)))
			b.event("net.reassert", map[string]any{"if": ifname, "ip": ip, "err": fmt.Sprint(err)})
		}
	}
}

// TRLC-LINKS: REQ-SDS-027
func splitCIDR(cidr string) (ip, mask string, ok bool) {
	before, after, ok := strings.Cut(cidr, "/")
	if !ok {
		return "", "", false
	}
	ip = before
	var bits int
	if _, err := fmt.Sscanf(after, "%d", &bits); err != nil || bits < 0 || bits > 32 {
		return "", "", false
	}
	v := ^uint32(0) << (32 - bits)
	if bits == 0 {
		v = 0
	}
	mask = fmt.Sprintf("%d.%d.%d.%d", byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	return ip, mask, true
}
