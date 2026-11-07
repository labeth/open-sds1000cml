// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"time"

	"github.com/labeth/open-dso-core/measure"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// A live envelope frame's edge windows (details Edge and EdgeOpp) are
// full-rate stretches of the record around one edge: the edge's shape, which
// the envelope's (min, max) buckets cannot hold, for rise, fall, overshoot
// and preshoot. Their codes are calibrated when the calibration applied, with
// the calibrated Q8.8 as precision words (none when it did not apply); their
// content is RawFullRate on a raw record, RawDecimated on a decimated one.

// edgeWindowWords is a window's length in record words: 2048 samples of a raw
// record (4 us at 2 ns), the least the converter calibration finds its
// rotation in; 1024 of a decimated one.
const edgeWindowWords = 1024

// oppositeEdgeBucket is the first bucket after the trigger's (trig) where the
// trigger channel's envelope - unordered (min, max) pairs - comes back from
// the level the trigger edge went to: the next edge the other way. -1 when the
// window has none.
// TRLC-LINKS: REQ-SDS-021
func oppositeEdgeBucket(pairs []uint8, trig int, rising bool) int {
	n := len(pairs) / 2
	if trig < 0 || trig >= n {
		return -1
	}
	lo, hi := 255, 0
	for _, v := range pairs {
		lo, hi = min(lo, int(v)), max(hi, int(v))
	}
	if hi-lo < 16 {
		return -1
	}
	q1, q3 := lo+(hi-lo)/4, hi-(hi-lo)/4
	at := func(i int) (int, int) {
		a, b := int(pairs[2*i]), int(pairs[2*i+1])
		return min(a, b), max(a, b)
	}
	i := trig + 1
	for ; i < n; i++ { // past the trigger edge: a bucket wholly at its new level
		mn, mx := at(i)
		if rising && mn >= q3 || !rising && mx <= q1 {
			break
		}
	}
	for ; i < n; i++ {
		mn, mx := at(i)
		if rising && mn <= q1 || !rising && mx >= q3 {
			return i
		}
	}
	return -1
}

// nearestEdgeBucket is the edge of the envelope - unordered (min, max)
// pairs - nearest bucket trig, either way: a bucket spanning both levels, or
// the first one at its new level after a step between neighbours; rising says
// which way the line goes there. A serial, pulse or video trigger points at no
// edge of the trigger's own direction (a UART match ends in its stop bit), so
// the edge windows go to the nearest edges instead. -1 when there is none.
// TRLC-LINKS: REQ-SDS-021
func nearestEdgeBucket(pairs []uint8, trig int) (int, bool) {
	n := len(pairs) / 2
	if trig < 0 || trig >= n {
		return -1, false
	}
	lo, hi := 255, 0
	for _, v := range pairs {
		lo, hi = min(lo, int(v)), max(hi, int(v))
	}
	if hi-lo < 16 {
		return -1, false
	}
	q1, q3 := lo+(hi-lo)/4, hi-(hi-lo)/4
	level := func(i int) int { // +1 high, -1 low, 0 an edge inside
		a, b := int(pairs[2*i]), int(pairs[2*i+1])
		switch mn, mx := min(a, b), max(a, b); {
		case mn >= q3:
			return 1
		case mx <= q1:
			return -1
		case mn <= q1 && mx >= q3:
			return 0
		}
		return 2 // between the levels: no decision
	}
	edgeAt := func(i int) (bool, bool) {
		l := level(i)
		if l == 0 {
			before, after := 2, 2
			for j := i - 1; j >= 0 && before != 1 && before != -1; j-- {
				before = level(j)
			}
			for j := i + 1; j < n && after != 1 && after != -1; j++ {
				after = level(j)
			}
			return before != after && (before == 1 || before == -1) && (after == 1 || after == -1), after == 1
		}
		if i > 0 && (l == 1 || l == -1) && level(i-1) == -l {
			return true, l == 1
		}
		return false, false
	}
	for d := range n {
		for _, i := range []int{trig - d, trig + d} {
			if i >= 0 && i < n {
				if ok, rising := edgeAt(i); ok {
					return i, rising
				}
			}
		}
	}
	return -1, false
}

// recallEdgeWindow reads a window of the frozen record m at its own rate,
// centred on word w, and calibrates a raw record's converters (a decimated
// record is filtered across them already); nil when the read fails.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-016
func (e *Engine) recallEdgeWindow(ctx context.Context, m sramcapture.Metadata, w uint32, scale [2]chScale) *sample.Frame {
	if m.Length < edgeWindowWords || m.SamplesPerWord == 0 {
		return nil
	}
	start := uint32(0)
	if w > edgeWindowWords/2 {
		start = w - edgeWindowWords/2
	}
	start = min(start, m.Length-edgeWindowWords)
	n := edgeWindowWords * int(m.SamplesPerWord)
	f := &sample.Frame{Traces: []sample.Trace{{Codes8: make([]uint8, n), Valid: n}, {Ch: 1, Codes8: make([]uint8, n), Valid: n}}}
	if m.FractionBits == 8 {
		f.Traces[0].Precision, f.Traces[1].Precision = make([]uint16, n), make([]uint16, n)
	}
	writer := sramFrameWriter{f: f, m: m}
	if err := e.sram.RecallReadsOf(ctx, m, sramcapture.RecordRead{Offset: start, Count: edgeWindowWords, Dst: &writer}); err != nil || writer.n != n {
		return nil
	}
	f.SampleS, f.Content = 1/m.SampleRateHz, sample.RawDecimated
	if m.SamplesPerWord == 2 && m.FractionBits == 0 {
		f.Content = sample.RawFullRate
	}
	if cal := e.interleaveCalibration; cal != nil && m.SamplesPerWord == 2 && m.FractionBits == 0 {
		q1, q2 := make([]uint16, n), make([]uint16, n)
		if cal.apply(f.Codes8(0), f.Codes8(1), q1, q2, [2]float64{scale[0].vdiv, scale[1].vdiv}, f.SampleS) {
			f.Traces[0].Precision, f.Traces[1].Precision = q1, q2
		}
	}
	return f
}

// EdgeWindows is channel ch's samples of frame f's edge windows, for
// measure.Result.WithEdgeDetail; none on a frame without them. The codes are
// the calibrated ones rounded: the Q8.8 values would cost each measurement a
// 65536-bin histogram (512 KB), on every LCD repaint - half the frame rate.
// TRLC-LINKS: REQ-SDS-021
func EdgeWindows(f *sample.Frame, ch int) []measure.EdgeWindow {
	var out []measure.EdgeWindow // nil without windows
	for _, r := range [2]sample.Role{sample.Edge, sample.EdgeOpp} {
		d := f.Detail(r)
		if d == nil {
			continue
		}
		if out == nil {
			out = make([]measure.EdgeWindow, 0, 2) // both, one allocation
		}
		w := d.Frame
		out = append(out, measure.EdgeWindow{Sig: w.Codes8(ch), SampleS: w.SampleS, Shape: w.Content == sample.RawFullRate})
	}
	return out
}

// EdgeRead numbers the read frame f's edge windows come from (0: none):
// they are carried to the frames between reads.
// TRLC-LINKS: REQ-SDS-021
func EdgeRead(f *sample.Frame) uint64 {
	for _, r := range [2]sample.Role{sample.Edge, sample.EdgeOpp} {
		if d := f.Detail(r); d != nil {
			return d.Read
		}
	}
	return 0
}

// addEdgeWindows gives frame f the edge windows d holds.
// TRLC-LINKS: REQ-SDS-021
func addEdgeWindows(f *sample.Frame, d *edgeDetail) {
	if d.edge != nil {
		f.Details = append(f.Details, sample.Detail{Role: sample.Edge, Read: d.read, Frame: d.edge})
	}
	if d.opp != nil {
		f.Details = append(f.Details, sample.Detail{Role: sample.EdgeOpp, Read: d.read, Frame: d.opp})
	}
}

// edgeDetailEvery is how often the edge windows are read: the two reads cost a
// live frame about 25 ms, which on every frame took the rate from 28 to 16 a
// second; the measurements they serve repaint four times a second.
const edgeDetailEvery = 500 * time.Millisecond

// edgeKey is what edge windows were read under: the capture's settings (not
// its split around the trigger, which a serial trigger moves) and the scale.
type edgeKey struct {
	cfg   sramcapture.Config
	scale [2]chScale
}

// edgeDetail is the last edge windows read, carried to the frames until the
// next read; a capture under another key drops them. read numbers the reads
// (EdgeRead), so statistics count a reading once, not on every frame
// it is carried to.
type edgeDetail struct {
	key       edgeKey
	at        time.Time
	edge, opp *sample.Frame
	read      uint64
}

// due reports whether the windows are to be read now: none read yet under
// key, or edgeDetailEvery since the last read. A new key drops the old ones.
// TRLC-LINKS: REQ-SDS-021
func (d *edgeDetail) due(key edgeKey, now time.Time) bool {
	if d.key != key {
		*d = edgeDetail{key: key, read: d.read}
	}
	return d.at.IsZero() || now.Sub(d.at) >= edgeDetailEvery
}

// store keeps the windows read at now.
// TRLC-LINKS: REQ-SDS-021
func (d *edgeDetail) store(key edgeKey, now time.Time, edge, opp *sample.Frame) {
	d.key, d.at, d.edge, d.opp = key, now, edge, opp
	d.read++
}
