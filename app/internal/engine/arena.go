// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// The engine publishes sample.Frame: two traces of 8-bit codes, with
// optional Q8.8 precision words beside them. The three arena frames are
// reused in place: every producer path must set or clear ALL metadata every
// frame (spec 01 §2: a stale flag renders wrong output from correct data).

// CodeFormat is the converters' code format as the frames carry it: 8-bit
// codes, the screen's centre line at 128.
var CodeFormat = sample.CodeFormat{Bits: 8, Storage: sample.U8, Max: 255, Mid: 128}

// CodesPerDiv is a record's codes per vertical division.
const CodesPerDiv = 25

// RollCodesPerDiv is the codes per division of the factory roll FIFO's
// half-scale codes, which a frame's traces say they hold by it.
const RollCodesPerDiv = CodesPerDiv / 2.0

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
			Ch: ch, Format: CodeFormat, CodesPerDiv: CodesPerDiv, PrecisionFrac: precisionFrac,
			Codes8: t.Codes8, Min8: t.Min8[:0], Max8: t.Max8[:0],
		}
	}
}

// recordFrame is a frame of its own for n samples a channel of a record:
// two traces stating the record's format, their codes allocated.
// TRLC-LINKS: REQ-SDS-007, REQ-SDS-021
func recordFrame(n int) *sample.Frame {
	f := &sample.Frame{Traces: make([]sample.Trace, 2)}
	for ch := range f.Traces {
		f.Traces[ch] = sample.Trace{
			Ch: ch, Format: CodeFormat, CodesPerDiv: CodesPerDiv, PrecisionFrac: precisionFrac,
			Codes8: make([]uint8, n), Valid: n,
		}
	}
	return f
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

// newFrameArena is the engine's arena: frames of two traces of capacity
// codes each, reset to the converters' format.
// TRLC-LINKS: REQ-SDS-007
func newFrameArena(capacity int) *core.Arena {
	return core.NewArena(func() *sample.Frame {
		f := &sample.Frame{Traces: make([]sample.Trace, 2)}
		for ch := range f.Traces {
			f.Traces[ch] = sample.Trace{
				Codes8: make([]uint8, capacity),
				Min8:   make([]uint8, 0, envDisplayCols), Max8: make([]uint8, 0, envDisplayCols),
			}
		}
		resetFrame(f)
		return f
	})
}
