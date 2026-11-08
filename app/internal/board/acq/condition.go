// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"fmt"

	"github.com/labeth/open-dso-core/dsp"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The record's conditioning: what Readout does to the words it read before
// a frame leaves the board. A decimated record's precision words are
// filtered (or, of box averages and picked samples, only rounded); the
// converters' interleave calibration corrects a full-rate record; an
// envelope's extremes lose the converters' offsets and are put in time
// order. Each window is conditioned as its role is used: the screen's
// main window whole, an edge window calibrated into precision words of its
// own, a view window's codes corrected and its precision words dropped.

// condState is the conditioning's calibration and working state (owner
// only).
type condState struct {
	cal   *InterleaveCalibration
	scale [2]float64 // each channel's converter range the record was taken at, V/div at the BNC
	// scratch is the precision filter's working buffer, kept between frames
	// and lent to the owner's own filters (ConditionScratch): one
	// megabyte-scale buffer, not one per step.
	scratch []uint16
	viewQ   []uint16 // a view window's Q8.8 scratch
	// the precision filter's noise gain and bandwidth, for the decimation
	// respDecim
	respDecim uint32
	respGain  float64
	respBW    float64
	observe   func(c1, c2 []uint8)
	armedPick bool // the armed capture's decimation picks
}

// SetInterleaveCalibration attaches the converters' calibration (nil:
// none): it corrects the records and its offsets size the trigger's re-arm
// band.
// TRLC-LINKS: REQ-SDS-016
func (b *Board) SetInterleaveCalibration(c *InterleaveCalibration) {
	b.cond.cal = c
	var off *[2][5]float64
	if c != nil {
		off = &c.Offset
	}
	b.SetConverterOffsets(off)
}

// InterleaveCalibration is the converters' calibration attached; nil:
// none.
// TRLC-LINKS: REQ-SDS-016
func (b *Board) InterleaveCalibration() *InterleaveCalibration { return b.cond.cal }

// SetRecordScale names each channel's converter range (V/div at the BNC)
// the record to read was taken at, which picks its calibration. OWNER.
// TRLC-LINKS: REQ-SDS-016, REQ-SDS-040
func (b *Board) SetRecordScale(vdiv [2]float64) { b.cond.scale = vdiv }

// SetRawObserver hears the raw codes of each full-rate main window read,
// before any conditioning: the converters' health watches them. OWNER.
// TRLC-LINKS: REQ-SDS-016
func (b *Board) SetRawObserver(fn func(c1, c2 []uint8)) { b.cond.observe = fn }

// ConditionScratch is the conditioning's working buffer, for the owner's
// own filters on a frame Readout delivered (its ERES): grown as they need,
// shared with the next read. OWNER.
// TRLC-LINKS: REQ-SDS-012
func (b *Board) ConditionScratch() *[]uint16 { return &b.cond.scratch }

// condition conditions frame f, read whole by window w of record m, as
// its role is used.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-016, REQ-SDS-040
func (b *Board) condition(m sramcapture.Metadata, w hal.Window, ww window, f *sample.Frame) {
	switch r := w.Role; {
	case r == sample.Main:
		b.conditionMain(m, ww.bucket, f)
	case r == sample.Edge || r == sample.EdgeOpp:
		b.conditionEdge(m, f)
	case r >= sample.RoleView(0):
		b.conditionView(m, ww.bucket, f)
	}
}

// conditionMain conditions the screen's window: a decimated record's
// precision filter, then an envelope's converter offsets and time order or
// a full-rate record's interleave calibration. Precision words a
// calibration writes take the frame's precision storage (a slice of its
// capacity, empty, stands for none); storage it did not fill is dropped.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-012, REQ-SDS-016, REQ-SDS-040
func (b *Board) conditionMain(m sramcapture.Metadata, bucket uint32, f *sample.Frame) {
	c := &b.cond
	n := f.Len()
	q1, q2 := f.Precision(0), f.Precision(1)
	full := len(q1) == n && len(q2) == n
	if !full {
		f.Traces[0].Precision, f.Traces[1].Precision = nil, nil
	}
	if b.cond.observe != nil && bucket == 0 && m.Decimation <= 1 && m.FractionBits == 0 && m.SamplesPerWord == 2 {
		c.observe(f.Codes8(0)[:n], f.Codes8(1)[:n])
	}
	caps := b.trig.caps
	if full && bucket == 0 && (caps.BoxDecim || c.armedPick) && m.Decimation > 1 {
		// Box averages: no CIC droop to compensate; a sinc's -3 dB at
		// 0.443 of the output rate. Picked samples: no filter.
		f.Cond.PassbandHz, f.Cond.BandwidthHz = .2/f.SampleS, .443/f.SampleS
		f.Cond.Filter = "box average"
		if caps.PickDecim || c.armedPick {
			f.Cond.PassbandHz, f.Cond.BandwidthHz = .5/f.SampleS, .5/f.SampleS
			f.Cond.Filter = "every 2^n-th sample"
		}
		dsp.RoundQ8(f.Codes8(0)[:n], f.Precision(0)[:n])
		dsp.RoundQ8(f.Codes8(1)[:n], f.Precision(1)[:n])
	} else if full && bucket == 0 {
		c.scratch = core.QBuffer(c.scratch, n)
		dsp.ConditionQ8(f.Precision(0), c.scratch)
		dsp.ConditionQ8(f.Precision(1), c.scratch)
		f.Cond.Guard = dsp.PrecisionGuard
		if c.respDecim != m.Decimation {
			c.respGain, c.respBW = dsp.PrecisionLimits(int(m.Decimation))
			c.respDecim = m.Decimation
		}
		f.Cond.NoiseGain = c.respGain
		f.Cond.PassbandHz = .075 / f.SampleS
		f.Cond.BandwidthHz = c.respBW / f.SampleS
		f.Cond.Filter = "CIC3 + compensated FIR"
		dsp.RoundQ8(f.Codes8(0)[:n], f.Precision(0)[:n])
		dsp.RoundQ8(f.Codes8(1)[:n], f.Precision(1)[:n])
	}
	switch {
	case bucket != 0:
		b.calibrateEnvelope(f, m, bucket, q1, q2)
		OrderEnvelopeHead(f.Codes8(0)[:n], f.Precision(0))
		OrderEnvelopeHead(f.Codes8(1)[:n], f.Precision(1))
		OrderEnvelope(f.Codes8(0)[:n], f.Precision(0))
		OrderEnvelope(f.Codes8(1)[:n], f.Precision(1))
		f.Flags.Set(sample.Ordered, true)
	case m.FractionBits == 0 && c.cal != nil:
		b.calibrateRecord(f, q1, q2)
	}
}

// calibrateEnvelope readies an envelope frame f of bucket words a bucket:
// extremes of whole buckets, so no per-core calibration or FIR applies, only
// the converters' offsets on a full-rate record (a decimated one is
// filtered across the converters already).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func (b *Board) calibrateEnvelope(f *sample.Frame, m sramcapture.Metadata, bucket uint32, q1, q2 []uint16) {
	if len(f.Precision(0)) == f.Len() && len(f.Precision(1)) == f.Len() {
		dsp.RoundQ8(f.Codes8(0)[:f.Len()], f.Precision(0)[:f.Len()])
		dsp.RoundQ8(f.Codes8(1)[:f.Len()], f.Precision(1)[:f.Len()])
	}
	f.Cond.Filter = fmt.Sprintf("peak-detect display readout, %d words per bucket; full record in SRAM", bucket)
	f.Content = sample.PeakPairs
	if cal := b.cond.cal; cal != nil && m.Decimation <= 1 {
		f.Traces[0].Precision, f.Traces[1].Precision = core.QBuffer(q1, f.Len()), core.QBuffer(q2, f.Len())
		cal.DebiasEnvelope(0, f.Codes8(0)[:f.Len()], f.Precision(0), len(f.Precision(0)) == f.Len() && m.FractionBits == 8)
		cal.DebiasEnvelope(1, f.Codes8(1)[:f.Len()], f.Precision(1), len(f.Precision(1)) == f.Len() && m.FractionBits == 8)
		f.Cond.Filter += "; converter offsets removed from the extremes"
	}
}

// calibrateRecord applies the converters' interleave calibration, the
// record's range's, to full-rate record frame f into precision words in
// q1 and q2's storage.
// TRLC-LINKS: REQ-SDS-040
func (b *Board) calibrateRecord(f *sample.Frame, q1, q2 []uint16) {
	c := b.cond
	q1 = core.QBuffer(q1, f.Len())
	q2 = core.QBuffer(q2, f.Len())
	if !c.cal.Apply(f.Codes8(0)[:f.Len()], f.Codes8(1)[:f.Len()], q1, q2, c.scale, f.SampleS) {
		f.Cond.Filter = "interleave calibration bypassed (scale, clipping or phase mismatch)"
		return
	}
	f.Traces[0].Precision = q1
	f.Traces[1].Precision = q2
	f.Cond.Filter = "interleave calibration (phase checked)"
	if c.cal.HasTiming(c.scale, f.SampleS) {
		f.Cond.Filter += "; aperture compensation"
		f.Cond.Guard = 2
	}
}

// conditionEdge calibrates a full-rate edge window into precision words of
// its own.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-040
func (b *Board) conditionEdge(m sramcapture.Metadata, f *sample.Frame) {
	c := b.cond
	if c.cal == nil || m.SamplesPerWord != 2 || m.FractionBits != 0 {
		return
	}
	n := f.Len()
	q1, q2 := make([]uint16, n), make([]uint16, n)
	if c.cal.Apply(f.Codes8(0), f.Codes8(1), q1, q2, c.scale, 1/m.SampleRateHz) {
		f.Traces[0].Precision, f.Traces[1].Precision = q1, q2
	}
}

// conditionView corrects a view window's codes, its precision words
// dropped (the record's, not the calibrated codes'): an envelope's
// converter offsets and time order, a long enough full-rate window's
// interleave calibration.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (b *Board) conditionView(m sramcapture.Metadata, bucket uint32, f *sample.Frame) {
	c := b.cond
	n := f.Len()
	f.Traces[0].Precision, f.Traces[1].Precision = nil, nil
	if bucket != 0 {
		if c.cal != nil && m.Decimation <= 1 {
			q1, q2 := b.viewQ8(n)
			c.cal.DebiasEnvelope(0, f.Codes8(0), q1, false)
			c.cal.DebiasEnvelope(1, f.Codes8(1), q2, false)
		}
		OrderEnvelopeHead(f.Codes8(0), nil)
		OrderEnvelopeHead(f.Codes8(1), nil)
		OrderEnvelope(f.Codes8(0), nil)
		OrderEnvelope(f.Codes8(1), nil)
	} else if c.cal != nil && m.FractionBits == 0 && n >= 2000 {
		q1, q2 := b.viewQ8(n)
		c.cal.Apply(f.Codes8(0), f.Codes8(1), q1, q2, c.scale, 1/m.SampleRateHz)
	}
}

// viewQ8 is scratch for a view window's Q8.8 values (two channels of n),
// which its calibration writes and the window does not keep.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (b *Board) viewQ8(n int) (q1, q2 []uint16) {
	c := &b.cond
	if cap(c.viewQ) < 2*n {
		c.viewQ = make([]uint16, 2*n)
	}
	q := c.viewQ[:2*n]
	return q[:n:n], q[n:]
}
