// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"open-sds/ota/internal/fdinherit"
	"open-sds/ota/internal/slots"
)

// TRLC-LINKS: REQ-SDS-027
func TestSplitCIDR(t *testing.T) {
	cases := []struct {
		in       string
		ip, mask string
		ok       bool
	}{
		{"192.168.1.209/24", "192.168.1.209", "255.255.255.0", true},
		{"10.0.0.5/16", "10.0.0.5", "255.255.0.0", true},
		{"1.2.3.4/32", "1.2.3.4", "255.255.255.255", true},
		{"1.2.3.4/0", "1.2.3.4", "0.0.0.0", true},
		{"1.2.3.4/8", "1.2.3.4", "255.0.0.0", true},
		{"1.2.3.4", "", "", false},    // no prefix
		{"1.2.3.4/33", "", "", false}, // out of range
		{"1.2.3.4/-1", "", "", false},
		{"1.2.3.4/ab", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		ip, mask, ok := splitCIDR(tc.in)
		if ip != tc.ip || mask != tc.mask || ok != tc.ok {
			t.Errorf("splitCIDR(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, ip, mask, ok, tc.ip, tc.mask, tc.ok)
		}
	}
}

// TRLC-LINKS: REQ-SDS-027
func TestMatchesFactoryName(t *testing.T) {
	a := testAgent(t)
	a.cfg.FactoryNames = []string{"SDS1000", "phoenix"}
	cases := []struct {
		h    fdinherit.Holder
		want bool
	}{
		{fdinherit.Holder{Comm: "SDS1000_arm.app"}, true},
		{fdinherit.Holder{Exe: "/usr/bin/siglent/SDS1000_arm.app"}, true},
		{fdinherit.Holder{Comm: "phoenix_ui"}, true},
		{fdinherit.Holder{Comm: "sh", Exe: "/bin/sh"}, false},
		{fdinherit.Holder{}, false},
	}
	for _, tc := range cases {
		if got := a.matchesFactoryName(tc.h); got != tc.want {
			t.Errorf("matchesFactoryName(%+v) = %v, want %v", tc.h, got, tc.want)
		}
	}
	a.cfg.FactoryNames = nil
	if a.matchesFactoryName(fdinherit.Holder{Comm: "SDS1000_arm.app"}) {
		t.Error("no configured names must match nothing")
	}
}

// TRLC-LINKS: REQ-SDS-027
func TestFactoryCandidatesEmptyWhenNobodyHoldsDevice(t *testing.T) {
	// cfg.GpmcDev points into a fresh TempDir; no process on this machine can
	// hold it, so the /proc scan must come back empty — the gate that keeps
	// the supervisor from ever reclaiming a bus nobody owns.
	a := testAgent(t)
	if cands := a.factoryCandidates(); len(cands) != 0 {
		t.Errorf("factoryCandidates = %v, want none", cands)
	}
}

// TRLC-LINKS: REQ-SDS-027
func TestTakeoverResultSteps(t *testing.T) {
	r := &TakeoverResult{}
	r.step("gate: inherited gpmc fd=%d", 7)
	r.step("plain")
	if len(r.Steps) != 2 || r.Steps[0] != "gate: inherited gpmc fd=7" {
		t.Errorf("Steps = %v", r.Steps)
	}
	if got := r.Summary(); got != "gate: inherited gpmc fd=7; plain" {
		t.Errorf("Summary = %q", got)
	}
}

// TRLC-LINKS: REQ-SDS-027
func TestTakeoverIdempotentWhenAlreadyTakenOver(t *testing.T) {
	a := testAgent(t)
	if err := a.st.update(func(s *State) { s.TakenOver = true }); err != nil {
		t.Fatal(err)
	}
	res := a.Takeover(TakeoverOpts{})
	if !res.OK {
		t.Fatalf("second takeover must be idempotent: %+v", res)
	}
	if !strings.Contains(res.Summary(), "already taken over") {
		t.Errorf("summary = %q", res.Summary())
	}
	// It re-arms the watchdog in the background (against the temp-file device
	// from testAgent) — give it a beat and confirm nothing exploded.
	time.Sleep(100 * time.Millisecond)
}

// TRLC-LINKS: REQ-SDS-104
func TestUntakeoverReleasesControl(t *testing.T) {
	a := testAgent(t)
	if err := a.st.update(func(s *State) { s.TakenOver = true; s.AutoTakeover = true }); err != nil {
		t.Fatal(err)
	}
	_ = a.store.Init()
	// Run the real supervisor so untakeover's internal ctl "stop" is served.
	// No slot has a binary and no factory process holds the temp GpmcDev, so
	// the loop just idles.
	go a.superviseLoop()
	defer a.Stop()

	resp := a.Dispatch([]byte(`{"cmd":"untakeover"}`))
	if !resp.OK {
		t.Fatalf("untakeover failed: %s", resp.Err)
	}
	st := a.st.get()
	if st.TakenOver || st.AutoTakeover {
		t.Errorf("state after untakeover = %+v, want both flags cleared", st)
	}
	if !pausedOf(a) {
		t.Error("untakeover must pause the supervisor")
	}
	// The cleared state must be persisted for the next agent generation.
	a2 := New(a.cfg)
	if got := a2.st.get(); got.TakenOver || got.AutoTakeover {
		t.Errorf("persisted state = %+v, want cleared", got)
	}
}

// An agent self-update leaves the running app to the next agent generation:
// reparented to init it holds the bus and is no descendant, but it runs from
// a slot and is adopted, never killed as a factory bus holder (that SIGKILLed
// a healthy app mid-acquisition on every update-agent).
// TRLC-LINKS: REQ-SDS-027, REQ-SDS-028
func TestFactoryCandidatesSkipOurOrphanApp(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	if err := os.WriteFile(a.cfg.GpmcDev, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	data, err := os.ReadFile(sleepBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.store.SlotDir(slots.SlotB), 0o755); err != nil {
		t.Fatal(err)
	}
	app := a.store.BinPath(slots.SlotB)
	if err := os.WriteFile(app, data, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "factory_app")
	if err := os.WriteFile(other, data, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both detach (the shell exits): not descendants of this process.
	for _, bin := range []string{app, other} {
		cmd := exec.Command("/bin/sh", "-c", bin+" 20 3<"+a.cfg.GpmcDev+" >/dev/null 2>&1 &")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, h := range fdinherit.HoldersOf(a.cfg.GpmcDev) {
			_ = syscall.Kill(h.PID, syscall.SIGKILL)
		}
	}()
	var cands []fdinherit.Holder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(fdinherit.HoldersOf(a.cfg.GpmcDev)) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cands = a.factoryCandidates()
	if len(cands) != 1 || filepath.Base(cands[0].Exe) != "factory_app" {
		t.Fatalf("factoryCandidates = %+v, want only the factory_app (the slot's app is ours)", cands)
	}
}
