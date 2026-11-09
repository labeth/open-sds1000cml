// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"testing"

	"github.com/labeth/open-dso-core/superres"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// stackMem is a fabric whose stacking session fails at its start.
type stackMem struct {
	*fakeMem
	begun int
}

func (m *stackMem) StackBegin(sramcapture.StackConfig) (int, error) {
	m.begun++
	return 0, errors.New("no stacker answers")
}
func (m *stackMem) SupportsPanel() (bool, error) { return false, nil }

// stackSwitcher is the switcher with the stacking image built in.
type stackSwitcher struct{ switcher }

func (s *stackSwitcher) HasStack() bool { return true }

// A stacking session is refused without the stacking image or with a
// record count out of range, and always ends on the general image, even
// when the session fails.
// TRLC-LINKS: REQ-SDS-141
func TestFPGAStackSession(t *testing.T) {
	b, fm, _ := newTestBoard()
	m := &stackMem{fakeMem: fm}
	b.mem = m
	b.panel.bus = nil
	req := superres.StackRequest{Records: 2}
	b.SetImages(&switcher{})
	if _, err := b.StackSession(context.Background(), sramcapture.Config{}, req); err == nil {
		t.Fatal("stacked without the stacking image")
	}
	sw := &stackSwitcher{}
	b.SetImages(sw)
	if _, err := b.StackSession(context.Background(), sramcapture.Config{}, superres.StackRequest{}); err == nil {
		t.Fatal("stacked no records")
	}
	_, err := b.StackSession(context.Background(), sramcapture.Config{}, req)
	if err == nil || m.begun != 1 || sw.loaded != "general" || b.imageLoaded != ImageGeneral || m.halted != 1 {
		t.Fatalf("a failed session: err %v, begun %d, image %q, halts %d", err, m.begun, sw.loaded, m.halted)
	}
}
