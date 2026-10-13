// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"io"
	"time"

	"open-sds/app/internal/measure"
	"open-sds/app/internal/sramcapture"
)

// EdgeWindow is a full-rate stretch of the record around one edge of a live
// envelope frame: the edge's shape, which the envelope's (min, max) buckets
// cannot hold, for rise, fall, overshoot and preshoot.
type EdgeWindow struct {
	C1, C2  []uint8  // calibrated codes, when the calibration applied
	Q1, Q2  []uint16 // calibrated Q8.8; nil when the calibration did not apply
	SampleS float64
	Full    bool // samples at the full rate (a raw record), not a decimated record's
}

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

// recallEdgeWindow reads a window of the frozen record at its own rate,
// centred on word w, and calibrates a raw record's converters (a decimated
// record is filtered across them already); nil when the read fails.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-016
func (e *Engine) recallEdgeWindow(ctx context.Context, recall func(context.Context, uint32, uint32, io.Writer) (int64, error), m sramcapture.Metadata, w uint32, scale [2]chScale) *EdgeWindow {
	if m.Length < edgeWindowWords || m.SamplesPerWord == 0 {
		return nil
	}
	start := uint32(0)
	if w > edgeWindowWords/2 {
		start = w - edgeWindowWords/2
	}
	start = min(start, m.Length-edgeWindowWords)
	n := edgeWindowWords * int(m.SamplesPerWord)
	f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n}
	if m.FractionBits == 8 {
		f.Q1, f.Q2 = make([]uint16, n), make([]uint16, n)
	}
	writer := sramFrameWriter{f: f, m: m}
	if _, err := recall(ctx, start, edgeWindowWords, &writer); err != nil || writer.n != n {
		return nil
	}
	ew := &EdgeWindow{C1: f.C1, C2: f.C2, Q1: f.Q1, Q2: f.Q2, SampleS: 1 / m.SampleRateHz, Full: m.SamplesPerWord == 2 && m.FractionBits == 0}
	if cal := e.interleaveCalibration; cal != nil && m.SamplesPerWord == 2 && m.FractionBits == 0 {
		q1, q2 := make([]uint16, n), make([]uint16, n)
		if cal.apply(f.C1, f.C2, q1, q2, [2]float64{scale[0].vdiv, scale[1].vdiv}, ew.SampleS) {
			ew.Q1, ew.Q2 = q1, q2
		}
	}
	return ew
}

// EdgeWindows is channel ch's samples of the frame's edge windows, for
// measure.Result.WithEdgeDetail; none on a frame without them. The codes are
// the calibrated ones rounded: the Q8.8 values would cost each measurement a
// 65536-bin histogram (512 KB), on every LCD repaint - half the frame rate.
// TRLC-LINKS: REQ-SDS-021
func (f *Frame) EdgeWindows(ch int) []measure.EdgeWindow {
	var out []measure.EdgeWindow
	for _, w := range []*EdgeWindow{f.Edge, f.EdgeOpp} {
		if w == nil {
			continue
		}
		sig := w.C1
		if ch == 1 {
			sig = w.C2
		}
		out = append(out, measure.EdgeWindow{Sig: sig, SampleS: w.SampleS, Shape: w.Full})
	}
	return out
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
// (Frame.EdgeRead), so statistics count a reading once, not on every frame
// it is carried to.
type edgeDetail struct {
	key       edgeKey
	at        time.Time
	edge, opp *EdgeWindow
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
func (d *edgeDetail) store(key edgeKey, now time.Time, edge, opp *EdgeWindow) {
	d.key, d.at, d.edge, d.opp = key, now, edge, opp
	d.read++
}
