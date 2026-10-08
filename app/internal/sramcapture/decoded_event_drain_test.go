// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type eventWriter func([]byte) (int, error)

// TRLC-LINKS: REQ-SDS-013
func (f eventWriter) Write(b []byte) (int, error) { return f(b) }

// TRLC-LINKS: REQ-SDS-013
func TestDecodedEventDrain(t *testing.T) {
	e := DecodedEvent{Epoch: 7, Kind: EventLoss, Count: 19, Sample: 100}
	raw, _ := e.MarshalBinary()
	b := &eventBus{enabled: true, epoch: 7, data: raw, fail: -1}
	c := &Capture{bus: b}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got []byte
	count, err := c.DrainDecodedEvents(ctx, 7, eventWriter(func(p []byte) (int, error) {
		// Writer must execute outside capture.mu, or client stalls deadlock control.
		if !c.mu.TryLock() {
			t.Fatal("writer holds capture lock")
		}
		c.mu.Unlock()
		got = append(got, p...)
		cancel()
		return len(p), nil
	}))
	if count != 1 || !errors.Is(err, context.Canceled) || !bytes.Equal(got, raw) {
		t.Fatalf("drain count=%d err=%v data=%x", count, err, got)
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedEventDrainRejectsMissingSequenceAndShortWrite(t *testing.T) {
	for _, sequence := range []uint32{0, 1} {
		e := DecodedEvent{Epoch: 7, Kind: EventData, Protocol: 1, Sequence: sequence}
		raw, _ := e.MarshalBinary()
		c := &Capture{bus: &eventBus{enabled: true, epoch: 7, data: raw, fail: -1}}
		count, err := c.DrainDecodedEvents(context.Background(), 7, eventWriter(func(p []byte) (int, error) {
			if sequence != 0 {
				t.Fatal("published discontinuous stream")
			}
			return 1, nil
		}))
		if count != 0 || err == nil {
			t.Fatal("bad stream accepted")
		}
		if sequence == 0 && !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
	}
}
