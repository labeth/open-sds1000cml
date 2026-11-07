// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/supervisor/fdinherit"
	"github.com/labeth/open-dso-core/supervisor/slots"
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
	_, b := testBoard(t)
	b.bc.FactoryNames = []string{"SDS1000", "phoenix"}
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
		if got := b.matchesFactoryName(tc.h); got != tc.want {
			t.Errorf("matchesFactoryName(%+v) = %v, want %v", tc.h, got, tc.want)
		}
	}
	b.bc.FactoryNames = nil
	if b.matchesFactoryName(fdinherit.Holder{Comm: "SDS1000_arm.app"}) {
		t.Error("no configured names must match nothing")
	}
}

// TRLC-LINKS: REQ-SDS-027
func TestFactoryCandidatesEmptyWhenNobodyHoldsDevice(t *testing.T) {
	// cfg.GpmcDev points into a fresh TempDir; no process on this machine can
	// hold it, so the /proc scan must come back empty — the gate that keeps
	// the supervisor from ever reclaiming a bus nobody owns.
	_, b := testBoard(t)
	if cands := b.factoryCandidates(); len(cands) != 0 {
		t.Errorf("factoryCandidates = %v, want none", cands)
	}
}

// An agent self-update leaves the running app to the next agent generation:
// reparented to init it holds the bus and is no descendant, but it runs from
// a slot and is adopted, never killed as a factory bus holder (that SIGKILLed
// a healthy app mid-acquisition on every update-agent).
// TRLC-LINKS: REQ-SDS-027, REQ-SDS-028
func TestFactoryCandidatesSkipOurOrphanApp(t *testing.T) {
	_, b := testBoard(t)
	store := slots.New(b.SlotsDir())
	_ = store.Init()
	if err := os.WriteFile(b.bc.GpmcDev, nil, 0o644); err != nil {
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
	if err := os.MkdirAll(store.SlotDir(slots.SlotB), 0o755); err != nil {
		t.Fatal(err)
	}
	app := store.BinPath(slots.SlotB)
	if err := os.WriteFile(app, data, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "factory_app")
	if err := os.WriteFile(other, data, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both detach (the shell exits): not descendants of this process.
	for _, bin := range []string{app, other} {
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", bin+" 20 3<"+b.bc.GpmcDev+" >/dev/null 2>&1 &")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, h := range fdinherit.HoldersOf(b.bc.GpmcDev) {
			_ = syscall.Kill(h.PID, syscall.SIGKILL)
		}
	}()
	var cands []fdinherit.Holder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(fdinherit.HoldersOf(b.bc.GpmcDev)) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cands = b.factoryCandidates()
	if len(cands) != 1 || filepath.Base(cands[0].Exe) != "factory_app" {
		t.Fatalf("factoryCandidates = %+v, want only the factory_app (the slot's app is ours)", cands)
	}
}
