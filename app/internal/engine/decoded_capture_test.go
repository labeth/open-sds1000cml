// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"open-sds/app/internal/sramcapture"
	"testing"
)

type decodedTestBus struct {
	fail      bool
	sequence  uint32
	cursor    int
	remaining int
}

// TRLC-LINKS: REQ-SDS-013
func (b *decodedTestBus) Read(_ uint8, s uint16) (uint16, error) {
	if b.fail {
		return 0, errors.New("bus failed")
	}
	switch s {
	case 74:
		return 0, nil
	case 0:
		return sramcapture.FabricID, nil
	case 13:
		return 10, nil
	case 14:
		return sramcapture.QualifiedMapID, nil
	case 57:
		return 1, nil
	case 61:
		return 7, nil
	case 62:
		return 0, nil
	case 63:
		return 0x4501, nil
	case 73:
		return 0x5201, nil
	case 58:
		if b.remaining > 0 {
			return 1 | uint16(b.cursor<<4), nil
		}
		return 0, nil
	case 59:
		data, _ := (sramcapture.DecodedEvent{Epoch: 7, Sequence: b.sequence, Protocol: 1, Kind: sramcapture.EventData, Value: b.sequence}).MarshalBinary()
		word := binary.LittleEndian.Uint16(data[b.cursor*2:])
		b.cursor++
		if b.cursor == 16 {
			b.cursor = 0
			b.sequence++
			b.remaining--
		}
		return word, nil
	}
	return 0, fmt.Errorf("unexpected register %d", s)
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func TestDecodedHeartbeatRequiresSuccessfulHardwarePoll(t *testing.T) {
	b := &decodedTestBus{remaining: 1}
	capture, err := sramcapture.New(b)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{sram: capture, decodedSession: &sramcapture.DecodedCapture{Identity: sramcapture.RecordIdentity{Epoch: 7, Record: 1}}}
	before := e.Beats()
	e.pumpDecodedEvents()
	if e.Beats() <= before {
		t.Fatal("active decoded capture did not report liveness")
	}
	before = e.Beats()
	e.pumpDecodedEvents()
	if e.Beats() <= before {
		t.Fatal("idle signal did not report successful hardware polling")
	}
	before = e.Beats()
	b.fail = true
	e.pumpDecodedEvents()
	if e.Beats() != before || e.decodedError == nil {
		t.Fatal("failed bus access reported healthy")
	}
	e.pumpDecodedEvents()
	if e.Beats() != before {
		t.Fatal("failed session kept reporting healthy")
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-025
func TestDecodedTranscriptFailurePreservesHardwareLiveness(t *testing.T) {
	b := &decodedTestBus{remaining: 5}
	capture, err := sramcapture.New(b)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("transcript incomplete")
	e := &Engine{sram: capture, decodedSession: &sramcapture.DecodedCapture{Identity: sramcapture.RecordIdentity{Epoch: 7, Record: 1}}, decodedError: failure}
	before := e.Beats()
	e.pumpDecodedEvents()
	if e.Beats() <= before || b.remaining != 5 || e.decodedError != failure {
		t.Fatal("failed transcript lost health, consumed data or hid its error")
	}
	b.fail = true
	before = e.Beats()
	e.pumpDecodedEvents()
	if e.Beats() != before {
		t.Fatal("failed hardware probe reported healthy")
	}
}

// TRLC-LINKS: REQ-SDS-013
func (b *decodedTestBus) RawWrite(s, v uint16) error { return fmt.Errorf("unexpected write %d", s) }

// TRLC-LINKS: REQ-SDS-013
func TestDecodedOwnerBufferOverflowIsExplicit(t *testing.T) {
	b := &decodedTestBus{remaining: decodedBufferLimit + 52}
	capture, err := sramcapture.New(b)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{sram: capture, decodedSession: &sramcapture.DecodedCapture{Identity: sramcapture.RecordIdentity{Epoch: 7, Record: 1}}}
	for i := 0; i < decodedBufferLimit+52; i++ {
		e.pumpDecodedEvents()
	}
	if e.decodedError == nil || len(e.decodedBuffer) != decodedBufferLimit {
		t.Fatalf("buffer=%d error=%v", len(e.decodedBuffer), e.decodedError)
	}
	for i, event := range e.decodedBuffer {
		if event.Sequence != uint32(i) {
			t.Fatal("reordered event")
		}
	}
	before := b.remaining
	e.pumpDecodedEvents()
	if b.remaining != before {
		t.Fatal("kept consuming failed transcript")
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func TestDecodedOwnerSkipsCancelledRequest(t *testing.T) {
	b := &decodedTestBus{}
	capture, err := sramcapture.New(b)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{sram: capture, decodedRequests: make(chan decodedRequest, 1), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	called := false
	go func() {
		_, err := e.decodedCall(ctx, func() (any, error) { called = true; return nil, nil })
		result <- err
	}()
	req := <-e.decodedRequests
	cancel()
	e.decodedRequests <- req
	e.serviceDecodedRequests()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if called {
		t.Fatal("cancelled queued request ran")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedBatchRingWrapAndCancelledReuse(t *testing.T) {
	capture, err := sramcapture.New(&decodedTestBus{})
	if err != nil {
		t.Fatal(err)
	}
	id := sramcapture.RecordIdentity{Epoch: 7, Record: 1}
	e := &Engine{sram: capture, decodedRequests: make(chan decodedRequest, 1), done: make(chan struct{}), decodedSession: &sramcapture.DecodedCapture{Identity: id}, decodedBuffer: make([]sramcapture.DecodedEvent, decodedBufferLimit), decodedHead: decodedBufferLimit - 1, decodedCount: 2}
	e.decodedBuffer[decodedBufferLimit-1] = sramcapture.DecodedEvent{Sequence: 40, Value: 0xaa}
	e.decodedBuffer[0] = sramcapture.DecodedEvent{Sequence: 41, Value: 0x55}
	dst := make([]sramcapture.DecodedEvent, 2)
	result := make(chan error, 1)
	go func() {
		n, err := e.ReadDecodedBatchInto(context.Background(), id, dst)
		if err == nil && n != 2 {
			err = fmt.Errorf("batch size %d", n)
		}
		result <- err
	}()
	req := <-e.decodedRequests
	e.decodedRequests <- req
	e.serviceDecodedRequests()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if dst[0].Sequence != 40 || dst[1].Sequence != 41 || e.decodedCount != 0 || e.decodedHead != 1 {
		t.Fatal("ring order or accounting", dst, e.decodedCount, e.decodedHead)
	}
	e.decodedBuffer[0].Value = 0
	if dst[1].Value != 0x55 {
		t.Fatal("batch aliases producer memory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, err := e.ReadDecodedBatchInto(ctx, id, dst); result <- err }()
	req = <-e.decodedRequests
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dst[0].Value = 0x1234
	// Even a request selected just before cancellation cannot write into a
	// destination that the caller has already reclaimed.
	if _, err := req.run(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if dst[0].Value != 0x1234 {
		t.Fatal("cancelled request changed reused storage")
	}
}
