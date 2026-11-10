// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package streams

import (
	"context"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// halSessionRig is the core's HAL cycle over the CML board with the board's
// decoded streams attached, a decoded capture of the scope's own running
// (decodedTestBus's events queued), as BeginDecodedStream leaves it.
func halSessionRig(t *testing.T, events int) (*core.HALCycle, *acq.Board, *DecodedStreams, *instrument.Model, sramcapture.RecordIdentity) {
	t.Helper()
	capture, err := sramcapture.New(&haltBus{decodedTestBus: decodedTestBus{remaining: events}})
	if err != nil {
		t.Fatal(err)
	}
	b := acq.New(acq.Config{Memory: capture})
	prof, err := scope.Profile(hal.Identity{Vendor: "Siglent", Model: "SDS1102CML+"})
	if err != nil {
		t.Fatal(err)
	}
	b.SetProfile(prof)
	d := NewBoardStreams(b, nil, t.Logf)
	b.SetSession(d)
	m := instrument.New(instrument.State{Channels: make([]hal.Channel, 2), Running: true, Sweep: hal.Auto, TimeDivS: 1e-3}, nil)
	_, c := core.NewHALLoop(core.HALConfig{Board: b, Instrument: m, Logf: t.Logf})
	c.Open()
	id := sramcapture.RecordIdentity{Epoch: 7, Record: 1}
	d.decodedSession = &sramcapture.DecodedCapture{Identity: id}
	d.decodedContact = time.Now()
	return c, b, d, m, id
}

// On the core's HAL cycle the board's decoded capture runs as on the
// engine's owner: the cycle's commit point pumps its events and serves its
// reader's requests, a waiting capture leaves the acquisition to it, the
// pass is the session's while it runs, and its end leaves the run stopped.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestBoardStreamsRunOnTheHALCycle(t *testing.T) {
	c, _, d, m, id := halSessionRig(t, 3)
	if c.Watch(false) != core.Leave {
		t.Fatal("a waiting capture did not leave the acquisition to the session")
	}
	ctx := context.Background()
	if !c.Claim(ctx) {
		t.Fatal("the pass was not the session's")
	}
	total := 0
	for deadline := time.Now().Add(2 * time.Second); total < 3 && time.Now().Before(deadline); {
		got := make(chan int, 1)
		go func() {
			dst := make([]sramcapture.DecodedEvent, 16)
			n, err := d.ReadDecodedBatchInto(ctx, id, dst)
			if err != nil {
				t.Error(err)
			}
			got <- n
		}()
		for n := -1; n < 0; {
			c.Serve() // the commit point: the events pumped, the read served
			select {
			case n = <-got:
				total += n
			case <-time.After(time.Millisecond):
			}
		}
	}
	if total != 3 {
		t.Fatalf("read %d events through the cycle's commit point, want 3", total)
	}
	done := make(chan error, 1)
	go func() { done <- d.EndDecodedCapture(ctx, id) }()
	var err error
	for ended := false; !ended; {
		c.Serve()
		select {
		case err = <-done:
			ended = true
		case <-time.After(time.Millisecond):
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if d.Active() || c.Claim(ctx) {
		t.Fatal("the ended session still takes the pass")
	}
	if m.State().Running {
		t.Fatal("the run is not stopped after the stream ended")
	}
}

// A decoded stream's record goes to the board once its capture has ended,
// as the stopped record the cycle shows whole.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestBoardStreamsHandTheirRecordToTheBoard(t *testing.T) {
	c, b, d, _, _ := halSessionRig(t, 0)
	rec := sramcapture.Metadata{Length: 1024, TriggerIndex: 512, Frozen: true, Ready: true}
	d.adopt = &adoptedRecord{m: rec}
	if d.TakeRecord() {
		t.Fatal("the record was taken while its capture runs")
	}
	d.decodedSession = nil
	if c.Claim(context.Background()) {
		t.Fatal("no session holds the pass")
	}
	if b.Acquirer().State() != hal.Ready {
		t.Fatalf("the board does not hold the stream's record (state %v)", b.Acquirer().State())
	}
	if d.TakeRecord() {
		t.Fatal("the record was handed twice")
	}
}
