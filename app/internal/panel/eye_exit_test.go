package panel

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// namedLeaseEng records the named raw leases asked of it.
type namedLeaseEng struct {
	*fakeEng
	mu     sync.Mutex
	leases []string
}

// TRLC-LINKS: REQ-SDS-215
func (e *namedLeaseEng) LeaseRawFor(holder string, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.leases = append(e.leases, fmt.Sprintf("%s %v", holder, d))
}

// take returns the leases asked since the last take.
// TRLC-LINKS: REQ-SDS-215
func (e *namedLeaseEng) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	l := e.leases
	e.leases = nil
	return l
}

// Leaving the eye ends its raw lease there and then (the engine abandons the
// raw read in flight for it, so the packets come from the live capture at
// once): F2 back to the packets, or another workspace. A frame analysed as
// the eye closes renews nothing, and the eye's lease is the eye's own (a bus
// search's or super-res's is not touched).
// TRLC-LINKS: REQ-SDS-215
func TestLeavingTheEyeEndsItsRawLease(t *testing.T) {
	eng := &namedLeaseEng{fakeEng: newEng()}
	c := New(eng, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.mu.Lock()
	c.decProto = 2 // UART: no detection on entering BUS
	c.mu.Unlock()
	for _, leave := range []struct {
		name string
		do   func()
	}{{"F2 again", c.nextView}, {"F4 MEASURE", func() { c.switchWS(wsview.Measure) }}} {
		c.switchWS(wsview.Bus)
		if !c.snap().ui.busEye {
			c.nextView() // the eye
		}
		eng.take()
		c.leaseEyeRaw() // a capture folded
		if got := eng.take(); len(got) != 1 || got[0] != rawForEye+" 3s" {
			t.Fatalf("%s: in the eye, leases %v", leave.name, got)
		}
		leave.do()
		if got := eng.take(); len(got) != 1 || got[0] != rawForEye+" 0s" {
			t.Fatalf("%s: leaving the eye, leases %v, want the eye's ended", leave.name, got)
		}
		c.leaseEyeRaw() // a frame analysed as the eye closed
		c.View()        // a redraw
		if got := eng.take(); len(got) != 0 {
			t.Fatalf("%s: after the eye, leases %v", leave.name, got)
		}
	}
}
