// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import (
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// slowScope applies a timebase only when the test lets it, as the engine's
// band switch does seconds later.
type slowScope struct {
	fakeScope
	mu      sync.Mutex
	pending float64
}

func (s *slowScope) SetTdiv(t float64) (engine.Band, bool) {
	b, ok := engine.PlanTdiv(t)
	if ok {
		s.mu.Lock()
		s.pending = b.TdivS
		s.mu.Unlock()
	}
	return b, ok
}

func (s *slowScope) Snapshot() engine.Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *slowScope) apply() {
	s.mu.Lock()
	s.stats.TdivS = s.pending
	s.mu.Unlock()
}

// TDIV? answers the set timebase while the engine still switches to it, and
// *OPC? waits for the switch (bug hunt #2: TDIV? kept the old value for 3 s
// and *OPC? answered at once).
// TRLC-LINKS: REQ-SDS-024
func TestTdivPendingAndOpc(t *testing.T) {
	sc := &slowScope{}
	sc.stats.TdivS = 5e-9
	h := New(sc, nil, nil, nil, t.Logf)
	h.HandleLine([]byte("TDIV 10S"))
	if got := string(h.HandleLine([]byte("TDIV?"))); got != "TDIV 1.00E+01s\n" {
		t.Fatalf("TDIV? while switching: %q", got)
	}
	go func() { time.Sleep(80 * time.Millisecond); sc.apply() }()
	t0 := time.Now()
	if got := string(h.HandleLine([]byte("*OPC?"))); got != "1\n" {
		t.Fatalf("*OPC? %q", got)
	}
	if time.Since(t0) < 60*time.Millisecond {
		t.Fatal("*OPC? did not wait for the timebase")
	}
	if got := string(h.HandleLine([]byte("TDIV?"))); got != "TDIV 1.00E+01s\n" {
		t.Fatalf("TDIV? after the switch: %q", got)
	}
}

// HCSU states the one hardcopy format there is and refuses others (bug hunt
// #2: any argument was a silent success and HCSU? was undefined).
// TRLC-LINKS: REQ-SDS-024
func TestHCSUFixedBMP(t *testing.T) {
	h := New(&fakeScope{}, nil, nil, nil, t.Logf)
	for cmd, want := range map[string]string{
		"HCSU?": "HCSU DEV,BMP\n", "HCSU DEV,BMP": "", "HCSU DEV,PNG": "Data out of range\n",
		"HCSU DEV": "Command header error\n", "HCSU PSIZE,A4": "Data out of range\n",
	} {
		if got := string(h.HandleLine([]byte(cmd))); got != want {
			t.Errorf("%s: %q, want %q", cmd, got, want)
		}
	}
}
