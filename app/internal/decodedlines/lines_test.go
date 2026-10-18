// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package decodedlines

import (
	"strings"
	"testing"

	"open-sds/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-018
func uart(text string, start uint64) []sramcapture.DecodedEvent {
	var ev []sramcapture.DecodedEvent
	for i := 0; i < len(text); i++ {
		ev = append(ev, sramcapture.DecodedEvent{Kind: sramcapture.EventData, Value: uint32(text[i]), Sample: start + uint64(i)*4340})
	}
	return ev
}

// A stream armed inside a character reads its rest as an error, and on a
// busy line re-syncs over a few more (garbled units, another error) before
// its first good frame - all inside the record's history, where the FPGA's
// trigger is not live, so a stream stopping on errors runs on: the
// transcript must not show errors it could not stop on. Errors after the
// history count and fill their slots; a software-decoded stream (protocols
// past the tenth but MIDI) has no such window.
// TRLC-LINKS: REQ-SDS-018
func TestErrorsInTheStartHistoryAreTheCutFrame(t *testing.T) {
	for _, proto := range []int{1, 2, 3, 4, 6, 9, 13, 11} {
		tr := New(proto, 100)
		ev := []sramcapture.DecodedEvent{{Kind: sramcapture.EventError, Sample: 900}}
		ev = append(ev, uart("A", 5000)...)
		ev = append(ev, sramcapture.DecodedEvent{Kind: sramcapture.EventError, Sample: 9000})
		ev = append(ev, uart("B", 900+HistorySamples-10)...)
		ev = append(ev, sramcapture.DecodedEvent{Kind: sramcapture.EventError, Sample: 900 + HistorySamples})
		ev = append(ev, uart("C", 900+HistorySamples+4000)...)
		tr.Add(ev)
		tr.Flush()
		errs, cut := uint64(1), uint64(2)
		if proto == 11 {
			errs, cut = 3, 0
		}
		if tr.Errors != errs || tr.Cut != cut || tr.Units != 3 {
			t.Fatalf("protocol %d: errors %d cut %d units %d", proto, tr.Errors, tr.Cut, tr.Units)
		}
	}
}

// Lines built in one transcript cross the wire and re-form, indices and
// counters intact, in another; the formatting matches.
// TRLC-LINKS: REQ-SDS-018
func TestEncodeDecodeAppend(t *testing.T) {
	w := New(1, 64)
	w.Add(uart("Hello, scope! 0123456789abcdef", 1000))
	w.Add([]sramcapture.DecodedEvent{{Kind: sramcapture.EventError, Sample: 1000 + HistorySamples}, {Kind: sramcapture.EventRetained}})
	w.Flush()
	buf := make([]byte, 4096)
	b, err := DecodeLines(buf[:w.EncodeLines(0, buf)])
	if err != nil {
		t.Fatal(err)
	}
	if b.Base != 0 || b.Next != 2 || len(b.Lines) != 2 || !b.TriggerHint || b.Counters.Units != 30 || b.Counters.Errors != 1 {
		t.Fatalf("batch %+v", b)
	}
	g := New(1, 64)
	for i, l := range b.Lines {
		g.Append(b.Indices[i], l)
	}
	g.SetCounters(b.Counters)
	for i := 0; i < 2; i++ {
		if g.Format(i) != w.Format(i) {
			t.Fatalf("line %d: %q vs %q", i, g.Format(i), w.Format(i))
		}
	}
	if !strings.HasSuffix(g.Format(1), "23456789abcdef!") {
		t.Fatalf("line 1 %q", g.Format(1))
	}
}

// A batch too small for every line stops at a line boundary, and the next
// request continues from there.
// TRLC-LINKS: REQ-SDS-018
func TestEncodePagesAndGaps(t *testing.T) {
	w := New(1, 1024)
	for i := 0; i < 100; i++ {
		w.Add(uart("0123456789abcdef", uint64(i)*100000))
	}
	buf := make([]byte, batchHeader+3*(18+16))
	b, _ := DecodeLines(buf[:w.EncodeLines(0, buf)])
	if len(b.Lines) != 3 || b.Indices[2] != 2 {
		t.Fatalf("page of %d lines", len(b.Lines))
	}
	g := New(1, 1024)
	g.Append(0, b.Lines[0])
	g.Append(5, b.Lines[1]) // a gap of four lines
	if g.Next() != 6 || g.At(2).Note != "GAP" {
		t.Fatalf("gap: next %d note %q", g.Next(), g.At(2).Note)
	}
}

// Wide units (MIL-1553 words) keep four bytes; byte units travel as one.
// TRLC-LINKS: REQ-SDS-018
func TestEncodeWidths(t *testing.T) {
	w := New(7, 16)
	w.Add([]sramcapture.DecodedEvent{{Kind: sramcapture.EventData, Value: 0xa55a}, {Kind: sramcapture.EventData, Value: 0x12}})
	w.Flush()
	buf := make([]byte, 256)
	n := w.EncodeLines(0, buf)
	b, err := DecodeLines(buf[:n])
	if err != nil || len(b.Lines) != 1 || b.Lines[0].Vals[0] != 0xa55a || b.Lines[0].Vals[1] != 0x12 || n != batchHeader+18+8 {
		t.Fatalf("wide: %v %+v n=%d", err, b, n)
	}
	u := New(1, 16)
	u.Add(uart("ab", 0))
	u.Flush()
	if n := u.EncodeLines(0, buf); n != batchHeader+18+2 {
		t.Fatalf("byte line %d bytes", n)
	}
}

// The rate is units per second of signal time: a 115200 Bd byte every
// 4340 two-nanosecond samples is 115200/10 bytes a second, whatever wall
// clock the stream took to start.
// TRLC-LINKS: REQ-SDS-018
func TestRateIsSignalTime(t *testing.T) {
	w := New(1, 64)
	if _, ok := w.Rate(); ok {
		t.Fatal("rate on an empty transcript")
	}
	w.Add(uart(strings.Repeat("Hi U....", 40), 5000))
	r, ok := w.Rate()
	want := 1 / (4340 * 2e-9)
	if !ok || r < want*0.99 || r > want*1.01 {
		t.Fatalf("rate %.0f/s ok=%v, want %.0f/s", r, ok, want)
	}
	// Lines built by the worker arrive with its counters, which also count
	// units decoded before the first line held here (HW: 26 k/s on 8.8 k/s).
	g := New(1, 64)
	for i := 0; i < w.Count(); i++ {
		g.Append(w.Base()+i, *w.At(i))
	}
	g.SetCounters(Counters{Units: 3 * w.Units})
	if r, ok := g.Rate(); !ok || r < want*0.99 || r > want*1.01 {
		t.Fatalf("line-mode rate %.0f/s ok=%v, want %.0f/s", r, ok, want)
	}
}
