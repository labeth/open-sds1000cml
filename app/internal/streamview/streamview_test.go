// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package streamview

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/sramcapture"
)

type fakeSource struct {
	mu        sync.Mutex
	events    []sramcapture.DecodedEvent
	triggered bool
	ended     bool
	adopt     bool
	meta      sramcapture.Metadata
	late      []sramcapture.DecodedEvent
	worker    *decodedlines.Transcript // set: line mode, the worker builds lines
	frozen    bool
}

// TRLC-LINKS: REQ-SDS-018
func (f *fakeSource) BeginDecodedStream(context.Context, engine.SerialParams, engine.StreamTrigger, int) (sramcapture.RecordIdentity, error) {
	return sramcapture.RecordIdentity{Epoch: 1, Record: 1}, nil
}

// TRLC-LINKS: REQ-SDS-018
func (f *fakeSource) ReadDecodedBatchInto(_ context.Context, _ sramcapture.RecordIdentity, dst []sramcapture.DecodedEvent) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := copy(dst, f.events)
	f.events = f.events[n:]
	return n, nil
}

// TRLC-LINKS: REQ-SDS-018
func (f *fakeSource) DecodedCaptureStatus(context.Context, sramcapture.RecordIdentity) (sramcapture.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sramcapture.Metadata{Triggered: f.triggered && len(f.events) == 0, Frozen: true, Ready: true, Length: 8}, nil
}

// TRLC-LINKS: REQ-SDS-018
func (f *fakeSource) FreezeDecodedStream(context.Context, sramcapture.RecordIdentity) (sramcapture.Metadata, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adopt = true
	if f.worker != nil {
		f.worker.Add(append(f.events, f.late...)) // drained before the freeze
		f.events, f.frozen = nil, true
	}
	f.events = append(f.events, f.late...) // queued when the record froze
	return f.meta, true, nil
}

// TRLC-LINKS: REQ-SDS-018
func (f *fakeSource) ReadDecodedLines(_ context.Context, _ sramcapture.RecordIdentity, from int, flush bool) (decodedlines.Batch, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.worker == nil {
		return decodedlines.Batch{}, false, nil // event mode
	}
	if !f.frozen {
		f.worker.Add(f.events) // the worker drains what the FPGA sent
		f.events = nil
	}
	if flush {
		f.worker.Flush()
	}
	buf := make([]byte, 64*1024)
	b, err := decodedlines.DecodeLines(buf[:f.worker.EncodeLines(from, buf)])
	return b, true, err
}

// TRLC-LINKS: REQ-SDS-018
func (f *fakeSource) EndDecodedCapture(context.Context, sramcapture.RecordIdentity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = true
	return nil
}

// TRLC-LINKS: REQ-SDS-018
func wait(t *testing.T, c *Controller, s State) View {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v := c.Snapshot(); v.State == s {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state %v, want %v", c.Snapshot().State, s)
	return View{}
}

// TRLC-LINKS: REQ-SDS-018
func uart(text string, start uint64) []sramcapture.DecodedEvent {
	var ev []sramcapture.DecodedEvent
	for i := 0; i < len(text); i++ {
		ev = append(ev, sramcapture.DecodedEvent{Kind: sramcapture.EventData, Value: uint32(text[i]), Sample: start + uint64(i)*4340, Protocol: 1})
	}
	return ev
}

// A stream lays units out as a hexdump - time, 16 hex units, text - and on
// the trigger marks the trigger line and the lines inside the record.
// TRLC-LINKS: REQ-SDS-018
func TestStreamHexdumpAndTriggerMarks(t *testing.T) {
	f := &fakeSource{triggered: true}
	f.events = uart("Hello, scope! 0123456789abcdef", 1000)
	f.events = append(f.events, sramcapture.DecodedEvent{Kind: sramcapture.EventError, Sample: 1000 + 30*4340, Protocol: 1})
	trig := uint64(1000 + 16*4340) // the second line's first unit
	f.meta = sramcapture.Metadata{HasSampleTimeline: true, SampleFirst: trig - 2000, SampleLast: trig + 200000, TriggerIndex: 1000}
	c := New(f, t.Logf)
	if err := c.Start(engine.SerialParams{Proto: 1}, engine.StreamOnError, 0, "UART 115200 Bd C2"); err != nil {
		t.Fatal(err)
	}
	v := wait(t, c, Triggered)
	if !v.RecordAvailable || v.Units != 30 || v.Errors != 1 || v.Lines != 2 || v.TriggerLine != 1 {
		t.Fatalf("view %+v", v)
	}
	p := c.Window(10, -1)
	if len(p.Lines) != 2 || !strings.Contains(p.Header, "hex") || !strings.Contains(p.Header, "text") {
		t.Fatalf("page %+v", p)
	}
	l0, l1 := p.Lines[0], p.Lines[1]
	if !strings.Contains(l0, "48 65 6C 6C 6F 2C 20 73 63 6F 70 65 21 20 30 31") || !strings.HasSuffix(l0, "Hello, scope! 01") || l0[0] != ' ' || l0[1] != ' ' {
		t.Fatalf("line 0 %q", l0)
	}
	if l1[0] != '>' || l1[1] != '|' || !strings.Contains(l1, "+0.0000") || !strings.HasSuffix(l1, "23456789abcdef!") || !strings.Contains(l1, "66 !!") {
		t.Fatalf("line 1 %q", l1)
	}
	if !strings.Contains(l0, "-0.1389") { // 16 units at 4340 samples before the trigger, 2 ns each
		t.Fatalf("line 0 time %q", l0)
	}
}

// A manual stop ends the stream and still hands over the record.
// TRLC-LINKS: REQ-SDS-018
func TestStreamManualStop(t *testing.T) {
	f := &fakeSource{}
	c := New(f, t.Logf)
	if err := c.Start(engine.SerialParams{Proto: 2}, engine.StreamManual, 0, "I2C"); err != nil {
		t.Fatal(err)
	}
	wait(t, c, Streaming)
	c.Stop()
	if v := c.Snapshot(); v.State != Stopped || !f.adopt {
		t.Fatalf("after stop: %+v adopt=%v", v, f.adopt)
	}
}

// The history keeps the newest lines, and a window clamps to it.
// TRLC-LINKS: REQ-SDS-018
func TestHistoryAndWindow(t *testing.T) {
	tr := decodedlines.New(1, historyLines)
	for i := 0; i < historyLines+10; i++ {
		tr.Add(uart("0123456789abcdef", uint64(i)*100000))
	}
	if tr.Count() != historyLines {
		t.Fatalf("held %d lines", tr.Count())
	}
	if first := tr.At(0).Sample; first != 10*100000 {
		t.Fatalf("oldest line starts at %d", first)
	}
	c := &Controller{t: tr}
	if p := c.Window(40, 1<<30); p.Top != historyLines-40 || len(p.Lines) != 40 {
		t.Fatalf("past-the-end window %d %d", p.Top, len(p.Lines))
	}
	if p := c.Window(40, 5); p.Top != 5 {
		t.Fatalf("window top %d", p.Top)
	}
}

// I2C addresses open a line with the address noted; MIL-1553 words show as
// four hex digits, eight a line, without text.
// TRLC-LINKS: REQ-SDS-018
func TestUnitLayouts(t *testing.T) {
	tr := decodedlines.New(2, historyLines)
	tr.Add([]sramcapture.DecodedEvent{{Kind: sramcapture.EventData, Value: 0x48, Count: 1}, {Kind: sramcapture.EventData, Value: 0x55}, {Kind: sramcapture.EventData, Value: 0x41, Count: 2}, {Kind: sramcapture.EventEnd}})
	if l := tr.Format(0); !strings.Contains(l, "@24W") || !strings.Contains(l, " 55 41 ") || !strings.HasSuffix(l, "U~") {
		t.Fatalf("I2C line %q", l)
	}
	mil := decodedlines.New(7, historyLines)
	mil.Add([]sramcapture.DecodedEvent{{Kind: sramcapture.EventData, Value: 0xa55a}})
	mil.Flush()
	if l := mil.Format(0); !strings.HasSuffix(strings.TrimRight(l, " "), "A55A") {
		t.Fatalf("MIL line %q", l)
	}
}

// Units still queued when the record freezes reach the transcript.
// TRLC-LINKS: REQ-SDS-018
func TestStopDrainsQueuedUnits(t *testing.T) {
	f := &fakeSource{late: uart("tail", 9_000_000)}
	c := New(f, t.Logf)
	if err := c.Start(engine.SerialParams{Proto: 1}, engine.StreamManual, 0, "UART"); err != nil {
		t.Fatal(err)
	}
	wait(t, c, Streaming)
	c.Stop()
	if v := c.Snapshot(); v.Units != 4 || !f.ended {
		t.Fatalf("after stop: %+v ended=%v", v, f.ended)
	}
	if p := c.Window(5, -1); len(p.Lines) != 1 || !strings.HasSuffix(p.Lines[0], "tail") {
		t.Fatalf("lines %q", p.Lines)
	}
}

// USB lines carry the packet's PID; an error ending a data-less packet marks
// that PID instead of adding a blank error unit.
// TRLC-LINKS: REQ-SDS-018
func TestUSBPIDNotes(t *testing.T) {
	tr := decodedlines.New(9, historyLines)
	tr.Add([]sramcapture.DecodedEvent{
		{Kind: sramcapture.EventStart, Value: 0xC3}, {Kind: sramcapture.EventData, Value: 0xFF}, {Kind: sramcapture.EventEnd, Value: 0xC3},
		{Kind: sramcapture.EventStart, Value: 0xD2}, {Kind: sramcapture.EventError, Value: 0xD2},
	})
	if l := tr.Format(0); !strings.Contains(l, "DATA0") || !strings.Contains(l, " FF") {
		t.Fatalf("data line %q", l)
	}
	if l := tr.Format(1); !strings.Contains(l, "ACK!") || strings.Contains(l, "!!") {
		t.Fatalf("handshake line %q", l)
	}
}

// Lines follows the transcript by absolute index across the ring's wrap.
// TRLC-LINKS: REQ-SDS-018
func TestLinesFollowsAcrossWrap(t *testing.T) {
	c := &Controller{t: decodedlines.New(1, historyLines)}
	for i := 0; i < historyLines+5; i++ {
		c.t.Add(uart("0123456789abcdef", uint64(i)*100000))
	}
	p := c.Lines(0, 3)
	if p.Base != 5 || len(p.Lines) != 3 || p.Lines[0].Index != 5 || p.Next != 8 || len(p.Lines[0].Units) != 16 {
		t.Fatalf("page %+v", p)
	}
	if q := c.Lines(p.Next, 1<<20); q.Lines[0].Index != 8 || q.Next != historyLines+5 {
		t.Fatalf("next page from %d next %d", q.Lines[0].Index, q.Next)
	}
}

// In line mode the controller takes the worker's finished lines, totals
// included, and on STOP drains the line the worker still had open.
// TRLC-LINKS: REQ-SDS-018
func TestLineModeFollowsWorker(t *testing.T) {
	f := &fakeSource{worker: decodedlines.New(1, 1024), late: uart("tail", 9_000_000)}
	f.events = uart("Hello, scope! 0123456789abcdef", 1000)
	c := New(f, t.Logf)
	if err := c.Start(engine.SerialParams{Proto: 1}, engine.StreamManual, 0, "UART"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for c.Snapshot().Units < 30 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	c.Stop()
	v := c.Snapshot()
	if v.State != Stopped || v.Units != 34 || v.Lines != 3 {
		t.Fatalf("view %+v", v)
	}
	p := c.Window(5, -1)
	text := ""
	col := strings.Index(p.Header, "text")
	for _, l := range p.Lines {
		text += l[col:]
	}
	if !strings.Contains(text, "Hello, scope! 0123456789abcdeftail") {
		t.Fatalf("lines %q", p.Lines)
	}
}
