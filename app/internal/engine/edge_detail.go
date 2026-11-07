// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"time"

	core "github.com/labeth/open-dso-core/engine"
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

// recallEdgeWindow reads a window of the frozen record m at its own rate,
// centred on word w, and calibrates a raw record's converters (a decimated
// record is filtered across them already); nil when the read fails.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-016
func (e *Engine) recallEdgeWindow(ctx context.Context, m sramcapture.Metadata, w uint32, scale [2]core.ChScale) *sample.Frame {
	if m.Length < edgeWindowWords || m.SamplesPerWord == 0 {
		return nil
	}
	start := uint32(0)
	if w > edgeWindowWords/2 {
		start = w - edgeWindowWords/2
	}
	start = min(start, m.Length-edgeWindowWords)
	n := edgeWindowWords * int(m.SamplesPerWord)
	f := recordFrame(n)
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
		if cal.apply(f.Codes8(0), f.Codes8(1), q1, q2, [2]float64{scale[0].Vdiv, scale[1].Vdiv}, f.SampleS) {
			f.Traces[0].Precision, f.Traces[1].Precision = q1, q2
		}
	}
	return f
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

// edgeKey is what edge windows were read under: the capture's settings (not
// its split around the trigger, which a serial trigger moves) and the scale.
type edgeKey struct {
	cfg   sramcapture.Config
	scale [2]core.ChScale
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
	return d.at.IsZero() || now.Sub(d.at) >= core.EdgeDetailEvery
}

// store keeps the windows read at now.
// TRLC-LINKS: REQ-SDS-021
func (d *edgeDetail) store(key edgeKey, now time.Time, edge, opp *sample.Frame) {
	d.key, d.at, d.edge, d.opp = key, now, edge, opp
	d.read++
}
