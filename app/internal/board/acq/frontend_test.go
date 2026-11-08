// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"testing"

	"github.com/labeth/open-dso-core/hal"
)

// writerFE is a front end whose setters leave their writes due.
type writerFE struct {
	frontEnd
	due     bool
	flushes int
}

func (f *writerFE) Pending() bool { return f.due }

func (f *writerFE) Flush() (bool, error) {
	if !f.due {
		return false, nil
	}
	f.due = false
	f.flushes++
	return true, nil
}

// The relay and gain words the front end's setters leave go to the
// hardware at a commit point with no capture armed, or at the next Arm (a
// commit arm), never under an armed capture; an owner leaving its capture
// to another owner of the memory lets them go.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-015
func TestFrontEndWritesWithNothingArmed(t *testing.T) {
	b, _, _ := newTestBoard()
	fe := &writerFE{}
	b.SetFrontEnd(fe)
	fe.due = true
	if !b.FrontEndPending() {
		t.Fatal("a due write not pending")
	}
	b.Platform().Service()
	if fe.flushes != 1 || b.FrontEndPending() {
		t.Fatalf("idle commit point: %d flushes", fe.flushes)
	}
	if err := b.Acquirer().Arm(); err != nil {
		t.Fatal(err)
	}
	n := b.CommitArms()
	fe.due = true
	b.Platform().Service()
	if fe.flushes != 1 {
		t.Fatal("wrote under an armed capture")
	}
	if _, err := b.Acquirer().Wait(context.Background(), 0); err == nil {
		t.Fatal("the fake froze at its first poll")
	}
	b.ReleaseCapture()
	b.Platform().Service()
	if fe.flushes != 2 || b.Acquirer().State() != hal.Idle {
		t.Fatalf("after leaving the capture: %d flushes, state %d", fe.flushes, b.Acquirer().State())
	}
	fe.due = true
	if err := b.Acquirer().Arm(); err != nil {
		t.Fatal(err)
	}
	if fe.flushes != 3 || b.CommitArms() != n+1 {
		t.Fatalf("Arm: %d flushes, %d commit arms", fe.flushes, b.CommitArms()-n)
	}
}
