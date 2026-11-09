// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
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
		data := make([]byte, sramcapture.DecodedEventBytes)
		_ = sramcapture.MarshalEvent(sramcapture.DecodedEvent{Epoch: 7, Sequence: b.sequence, Protocol: 1, Kind: sramcapture.EventData, Value: b.sequence}, data)
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
	e := testEngine(&Engine{sram: capture}, func(d *DecodedStreams) {
		d.decodedSession = &sramcapture.DecodedCapture{Identity: sramcapture.RecordIdentity{Epoch: 7, Record: 1}}
	})
	before := e.Beats()
	e.streams().pumpDecodedEvents()
	if e.Beats() <= before {
		t.Fatal("active decoded capture did not report liveness")
	}
	before = e.Beats()
	e.streams().pumpDecodedEvents()
	if e.Beats() <= before {
		t.Fatal("idle signal did not report successful hardware polling")
	}
	before = e.Beats()
	b.fail = true
	e.streams().pumpDecodedEvents()
	if e.Beats() != before || e.streams().decodedError == nil {
		t.Fatal("failed bus access reported healthy")
	}
	e.streams().pumpDecodedEvents()
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
	e := testEngine(&Engine{sram: capture}, func(d *DecodedStreams) {
		d.decodedSession = &sramcapture.DecodedCapture{Identity: sramcapture.RecordIdentity{Epoch: 7, Record: 1}}
		d.decodedError = failure
	})
	before := e.Beats()
	e.streams().pumpDecodedEvents()
	if e.Beats() <= before || b.remaining != 5 || !errors.Is(e.streams().decodedError, failure) {
		t.Fatal("failed transcript lost health, consumed data or hid its error")
	}
	b.fail = true
	before = e.Beats()
	e.streams().pumpDecodedEvents()
	if e.Beats() != before {
		t.Fatal("failed hardware probe reported healthy")
	}
}

// TRLC-LINKS: REQ-SDS-013
func (b *decodedTestBus) RawWrite(s, _ uint16) error { return fmt.Errorf("unexpected write %d", s) }

// TRLC-LINKS: REQ-SDS-013
func TestDecodedOwnerBufferOverflowIsExplicit(t *testing.T) {
	b := &decodedTestBus{remaining: decodedBufferLimit + 52}
	capture, err := sramcapture.New(b)
	if err != nil {
		t.Fatal(err)
	}
	e := testEngine(&Engine{sram: capture}, func(d *DecodedStreams) {
		d.decodedSession = &sramcapture.DecodedCapture{Identity: sramcapture.RecordIdentity{Epoch: 7, Record: 1}}
	})
	for range decodedBufferLimit + 52 {
		e.streams().pumpDecodedEvents()
	}
	if e.streams().decodedError == nil || len(e.streams().decodedBuffer) != decodedBufferLimit {
		t.Fatalf("buffer=%d error=%v", len(e.streams().decodedBuffer), e.streams().decodedError)
	}
	for i, event := range e.streams().decodedBuffer {
		if event.Sequence != uint32(i) {
			t.Fatal("reordered event")
		}
	}
	before := b.remaining
	e.streams().pumpDecodedEvents()
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
	e := testEngine(&Engine{sram: capture, done: make(chan struct{})}, func(d *DecodedStreams) {
		d.decodedRequests = make(chan core.OwnerJob, 1)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	called := false
	go func() {
		_, err := e.streams().decodedCall(ctx, func() (any, error) { called = true; return nil, nil })
		result <- err
	}()
	req := <-e.streams().decodedRequests
	cancel()
	e.streams().decodedRequests <- req
	e.streams().serviceDecodedRequests()
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
	e := testEngine(&Engine{sram: capture, done: make(chan struct{})}, func(d *DecodedStreams) {
		d.decodedRequests = make(chan core.OwnerJob, 1)
		d.decodedSession = &sramcapture.DecodedCapture{Identity: id}
		d.decodedBuffer = make([]sramcapture.DecodedEvent, decodedBufferLimit)
		d.decodedHead = decodedBufferLimit - 1
		d.decodedCount = 2
	})
	e.streams().decodedBuffer[decodedBufferLimit-1] = sramcapture.DecodedEvent{Sequence: 40, Value: 0xaa}
	e.streams().decodedBuffer[0] = sramcapture.DecodedEvent{Sequence: 41, Value: 0x55}
	dst := make([]sramcapture.DecodedEvent, 2)
	result := make(chan error, 1)
	go func() {
		n, err := e.ReadDecodedBatchInto(context.Background(), id, dst)
		if err == nil && n != 2 {
			err = fmt.Errorf("batch size %d", n)
		}
		result <- err
	}()
	req := <-e.streams().decodedRequests
	e.streams().decodedRequests <- req
	e.streams().serviceDecodedRequests()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if dst[0].Sequence != 40 || dst[1].Sequence != 41 || e.streams().decodedCount != 0 || e.streams().decodedHead != 1 {
		t.Fatal("ring order or accounting", dst, e.streams().decodedCount, e.streams().decodedHead)
	}
	e.streams().decodedBuffer[0].Value = 0
	if dst[1].Value != 0x55 {
		t.Fatal("batch aliases producer memory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, err := e.ReadDecodedBatchInto(ctx, id, dst); result <- err }()
	req = <-e.streams().decodedRequests
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dst[0].Value = 0x1234
	// Even a request selected just before cancellation cannot write into a
	// destination that the caller has already reclaimed.
	if _, err := req.Run(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if dst[0].Value != 0x1234 {
		t.Fatal("cancelled request changed reused storage")
	}
}

// haltBus answers a halt and a decoded-event disable: STATUS acknowledges each
// command and reads ready; writes are taken.
type haltBus struct {
	decodedTestBus
	ack uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b *haltBus) Read(p uint8, s uint16) (uint16, error) {
	if s == 1 {
		return 4 | b.ack, nil
	}
	return b.decodedTestBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func (b *haltBus) RawWrite(s, _ uint16) error {
	if s == 1 {
		b.ack ^= 512
	}
	return nil
}

// webSessionEngine is an engine holding a decoded capture, a web page's or the
// scope's stream's, last touched by a client contact ago.
// TRLC-LINKS: REQ-SDS-013
func webSessionEngine(t *testing.T, web bool, contact time.Duration) *Engine {
	t.Helper()
	capture, err := sramcapture.New(&haltBus{})
	if err != nil {
		t.Fatal(err)
	}
	e := testEngine(&Engine{
		sram: capture, done: make(chan struct{}), logf: t.Logf,
	}, func(d *DecodedStreams) {
		d.decodedRequests = make(chan core.OwnerJob, 1)
		d.decodedSession = &sramcapture.DecodedCapture{Identity: sramcapture.RecordIdentity{Epoch: 7, Record: 1}}
		d.decodedWeb = web
		d.decodedContact = time.Now().Add(-contact)
	})
	e.streams().stats.DecodedWeb = web
	e.SetRunning(true)
	return e
}

// A web page's decoded capture no client touches for decodedAbandonAfter ends
// by itself, stopped for RUN, so a page closed mid-session does not keep the
// acquisition waiting. A recent client keeps it. The scope's own stream, read
// without pause while it runs, stays while read and ends unread past
// decodedOrphanAfter (a reader that failed left it open), capturing again.
// TRLC-LINKS: REQ-SDS-013
func TestAbandonedWebDecodedCaptureEnds(t *testing.T) {
	e := webSessionEngine(t, true, decodedAbandonAfter+time.Second)
	e.streams().expireDecoded()
	if e.streams().decodedSession != nil || e.streams().decodedWeb || e.streams().stats.DecodedWeb || e.running.Load() {
		t.Fatalf("abandoned session kept: session %v web %v running %v", e.streams().decodedSession != nil, e.streams().decodedWeb, e.running.Load())
	}
	for _, c := range []struct {
		web     bool
		contact time.Duration
	}{{true, 5 * time.Second}, {false, decodedOrphanAfter - 5*time.Second}} {
		e := webSessionEngine(t, c.web, c.contact)
		e.streams().expireDecoded()
		if e.streams().decodedSession == nil {
			t.Fatalf("web %v, client %s ago: ended", c.web, c.contact)
		}
	}
	e = webSessionEngine(t, false, decodedOrphanAfter+time.Second)
	e.streams().expireDecoded()
	if e.streams().decodedSession != nil || !e.running.Load() {
		t.Fatalf("the scope's stream unread past %s: session kept %v, running %v", decodedOrphanAfter, e.streams().decodedSession != nil, e.running.Load())
	}
}

// The scope's run-control keys end a web page's decoded capture (they always
// get the acquisition back) and report it; the scope's own stream stays.
// TRLC-LINKS: REQ-SDS-013
func TestScopeKeysEndAWebDecodedCapture(t *testing.T) {
	for _, web := range []bool{true, false} {
		e := webSessionEngine(t, web, time.Second)
		got := make(chan bool, 1)
		go func() {
			ended, err := e.EndWebDecodedCapture(context.Background())
			if err != nil {
				t.Error(err)
			}
			got <- ended
		}()
		var ended bool
		for done := false; !done; {
			e.streams().serviceDecodedRequests()
			select {
			case ended = <-got:
				done = true
			case <-time.After(time.Millisecond):
			}
		}
		if ended != web || (e.streams().decodedSession == nil) != web {
			t.Fatalf("web %v: ended %v, session left %v", web, ended, e.streams().decodedSession != nil)
		}
	}
}

// A web page's decoded capture is refused while the scope's own stream runs
// and is read (it would take the fabric from under the scope's reader); a
// web session, or the scope's stream gone unread, gives way, and the scope's
// own start is never refused here.
// TRLC-LINKS: REQ-SDS-013
func TestWebDecodedCaptureLeavesTheScopesStream(t *testing.T) {
	for _, c := range []struct {
		sessionWeb bool
		contact    time.Duration
		web, taken bool
	}{
		{false, time.Second, true, true},
		{false, decodedOrphanAfter + time.Second, true, false},
		{true, time.Second, true, false},
		{false, time.Second, false, false},
	} {
		e := webSessionEngine(t, c.sessionWeb, c.contact)
		if err := e.streams().decodedTaken(c.web); (err != nil) != c.taken {
			t.Errorf("session web %v read %s ago, start web %v: %v, want taken %v", c.sessionWeb, c.contact, c.web, err, c.taken)
		}
	}
	if err := testEngine(&Engine{}, nil).streams().decodedTaken(true); err != nil {
		t.Errorf("no session: %v", err)
	}
}

// A decoded capture whose configuration the fabric refuses is refused
// before anything is halted: the session running keeps running.
// TRLC-LINKS: REQ-SDS-013
func TestRefusedDecodedCaptureLeavesTheSession(t *testing.T) {
	e := webSessionEngine(t, true, time.Second)
	before := e.streams().decodedSession
	got := make(chan error, 1)
	go func() {
		_, err := e.BeginDecodedCapture(context.Background(), sramcapture.Config{})
		got <- err
	}()
	var err error
	for done := false; !done; {
		e.streams().serviceDecodedRequests()
		select {
		case err = <-got:
			done = true
		case <-time.After(time.Millisecond):
		}
	}
	if err == nil || !strings.Contains(err.Error(), "requires a configured hardware decoder") || e.streams().decodedSession != before {
		t.Fatalf("refused start: err %v, session kept %v", err, e.streams().decodedSession == before)
	}
}

// testEngine gives an engine a test builds as a literal its decoded
// streams (New makes them), set as set says.
func testEngine(e *Engine, set func(*DecodedStreams)) *Engine {
	if e.ds == nil {
		e.ds = newDecodedStreams(engineHost{e}, e.done)
	}
	if set != nil {
		set(e.ds)
	}
	return e
}
