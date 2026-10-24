package engine

import (
	"testing"
	"time"

	"open-sds/app/internal/sramcapture"
)

// A raw lease is its holder's own: ending the eye's leaves the web's (and a
// bus search's) standing and the raw read they want in flight; ending the
// last abandons the live raw read at once, so the next capture is the live
// view instead of seconds of a record nobody wants.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-018
func TestRawLeaseEndAbandonsTheReadNobodyWants(t *testing.T) {
	e, clk := newTestEngine(t, newFakeBus())
	cancelled := 0
	inFlight := func() {
		e.rawMu.Lock()
		e.rawCancel = func() { cancelled++ }
		e.rawMu.Unlock()
	}
	e.LeaseRawFor("eye", 3*time.Second)
	e.LeaseDecodeView(3 * time.Second) // the web's eye
	if !e.rawLeased() || e.envelopeBlock(&sramcapture.Config{}, trigParams{}) != blockRaw {
		t.Fatalf("leased: raw %v, live readout block %q", e.rawLeased(), e.envelopeBlock(&sramcapture.Config{}, trigParams{}))
	}
	inFlight()
	e.LeaseRawFor("eye", 0)
	if cancelled != 0 || !e.rawLeased() {
		t.Fatalf("the LCD's eye ended while the web's leases: read cancelled %d, leased %v", cancelled, e.rawLeased())
	}
	e.LeaseRawFor("bus search", 3*time.Second)
	e.LeaseDecodeView(0)
	if cancelled != 0 {
		t.Fatalf("the web ended while a bus search leases: the read was cancelled")
	}
	e.LeaseRawFor("bus search", 0)
	if cancelled != 1 || e.rawLeased() {
		t.Fatalf("the last lease ended: read cancelled %d times, leased %v", cancelled, e.rawLeased())
	}
	// an expired lease is no lease, and ending one with no read in flight is quiet
	e.LeaseRawFor("eye", time.Second)
	clk.mu.Lock()
	clk.t = clk.t.Add(2 * time.Second)
	clk.mu.Unlock()
	if e.rawLeased() {
		t.Fatalf("an expired lease still leases")
	}
	e.LeaseRawFor("eye", 0)
	if cancelled != 1 {
		t.Fatalf("no read in flight, yet a cancel")
	}
}
