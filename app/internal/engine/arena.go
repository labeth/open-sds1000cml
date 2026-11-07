// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"sync"

	"github.com/labeth/open-dso-core/sample"
)

// The engine publishes sample.Frame: two traces of 8-bit codes, with
// optional Q8.8 precision words beside them. The three arena frames are
// reused in place: every producer path must set or clear ALL metadata every
// frame (spec 01 §2: a stale flag renders wrong output from correct data).

// codeFormat is the converters' code format as the frames carry it: 8-bit
// codes, the screen's centre line at 128.
var codeFormat = sample.CodeFormat{Bits: 8, Storage: sample.U8, Max: 255, Mid: 128}

// codesPerDiv is a record's codes per vertical division.
const codesPerDiv = 25

// RollCodesPerDiv is the codes per division of the factory roll FIFO's
// half-scale codes, which a frame's traces say they hold by it.
const RollCodesPerDiv = codesPerDiv / 2.0

// HalfScale reports whether frame f holds the roll FIFO's half-scale codes
// (RollCodesPerDiv), which an export must rescale.
// TRLC-LINKS: REQ-SDS-024
func HalfScale(f *sample.Frame) bool {
	return len(f.Traces) > 0 && f.Traces[0].CodesPerDiv == RollCodesPerDiv
}

// precisionFrac is the fraction bits of the Q8.8 precision words.
const precisionFrac = 8

// resetFrame clears every field of arena frame f for a new record, keeping
// its two traces' code buffers at their full capacity and its envelope and
// detail storage emptied. The precision words are dropped: a producer keeps
// their buffers itself (qBuffer).
// TRLC-LINKS: REQ-SDS-007, REQ-SDS-009
func resetFrame(f *sample.Frame) {
	traces, details := f.Traces[:2], f.Details[:0]
	*f = sample.Frame{Traces: traces, Details: details}
	for ch := range traces {
		t := &traces[ch]
		*t = sample.Trace{
			Ch: ch, Format: codeFormat, CodesPerDiv: codesPerDiv, PrecisionFrac: precisionFrac,
			Codes8: t.Codes8, Min8: t.Min8[:0], Max8: t.Max8[:0],
		}
	}
}

// setEnvCols sizes frame f's display envelope to cols columns, keeping the
// values of the columns it had.
// TRLC-LINKS: REQ-SDS-009
func setEnvCols(f *sample.Frame, cols int) {
	for ch := range f.Traces {
		t := &f.Traces[ch]
		if cap(t.Min8) < cols || cap(t.Max8) < cols {
			t.Min8 = append(make([]uint8, 0, cols), t.Min8...)
			t.Max8 = append(make([]uint8, 0, cols), t.Max8...)
		}
		t.Min8, t.Max8 = t.Min8[:cols], t.Max8[:cols]
	}
}

// arena is the triple buffer between the engine owner and consumers
// (spec 01 §2): write = producer's private drain target, ready = most recent
// published, read = consumer's private slot. Double-buffering tears against
// the immediate-re-arm invariant; three slots are required.
// TRLC-LINKS: REQ-SDS-007
type arena struct {
	mu    sync.Mutex
	write *sample.Frame
	ready *sample.Frame
	read  *sample.Frame
	dirty bool
}

// TRLC-LINKS: REQ-SDS-007
func newArena(capacity int) *arena {
	mk := func() *sample.Frame {
		f := &sample.Frame{Traces: make([]sample.Trace, 2)}
		for ch := range f.Traces {
			f.Traces[ch] = sample.Trace{
				Codes8: make([]uint8, capacity),
				Min8:   make([]uint8, 0, envDisplayCols), Max8: make([]uint8, 0, envDisplayCols),
			}
		}
		resetFrame(f)
		return f
	}
	return &arena{write: mk(), ready: mk(), read: mk()}
}

// Write returns the producer's private slot. Owner-only.
// TRLC-LINKS: REQ-SDS-007
func (a *arena) Write() *sample.Frame { return a.write }

// Publish swaps the drained write slot into ready. The mutex guards only the
// pointer swap — never held across bus access. If the consumer hasn't taken
// the previous frame it is overwritten (drop-newest backpressure).
// TRLC-LINKS: REQ-SDS-007
func (a *arena) Publish() {
	a.mu.Lock()
	a.write, a.ready = a.ready, a.write
	a.dirty = true
	a.mu.Unlock()
}

// Consume returns the newest published frame. fresh=false means nothing new
// was published since the last call — the caller re-presents the held frame
// (a quiet NORM display, not an error).
// TRLC-LINKS: REQ-SDS-007
func (a *arena) Consume() (f *sample.Frame, fresh bool) {
	a.mu.Lock()
	fresh = a.dirty
	if fresh {
		a.read, a.ready = a.ready, a.read
		a.dirty = false
	}
	f = a.read
	a.mu.Unlock()
	return f, fresh
}
