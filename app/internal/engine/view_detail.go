// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// A view window (detail RoleView(s) of surface s) is a stretch of a live
// envelope frame's screen read again at full detail for a view that shows it
// magnified (the scope's zoom, the web page's): the screen's readout spreads
// its buckets over the whole screen, so a 50x zoom showed 80 points for 40
// cycles of a 1 MHz square at 200us/div, aliased. It holds the same capture,
// as an envelope of its own buckets (time-ordered peak pairs) or raw samples
// when short; the detail's From, To are where it lies among the frame's
// values (fractional indices): whole buckets widen the request a little.

// LeaseViewWindow asks, for surface s, for the stretch [a, b] of the live
// frames at full detail (ViewDetail) for d from now; d <= 0 ends the request.
// a and b are screen widths from the trigger (the record's middle on an
// untriggered frame): a zoom by z about the trigger is -0.5/z .. 0.5/z. cols
// is how many display columns show it. A surface's newest request wins.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) LeaseViewWindow(s ViewSurface, a, b float64, cols int, d time.Duration) {
	if s < 0 || s >= core.ViewSurfaces {
		return
	}
	v := &e.views[s]
	if d <= 0 || !(b > a) || math.IsInf(b-a, 0) || cols < 2 {
		v.Lease.Store(0)
		return
	}
	v.A.Store(math.Float64bits(a))
	v.B.Store(math.Float64bits(b))
	v.Cols.Store(int64(cols))
	e.lease(&v.Lease, d)
}

// viewRequest is surface s's current request, if any.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) viewRequest(s ViewSurface) (a, b float64, cols int, ok bool) {
	v := &e.views[s]
	if !e.leased(&v.Lease) {
		return 0, 0, 0, false
	}
	a, b = math.Float64frombits(v.A.Load()), math.Float64frombits(v.B.Load())
	return a, b, int(v.Cols.Load()), b > a
}

// planView plans the read of the record's words [from, to) in buckets
// buckets: whole buckets about its centre, raw when that is short. false when
// the stretch is empty.
// TRLC-LINKS: REQ-SDS-021
func planView(m sramcapture.Metadata, from, to float64, buckets uint32) (core.ViewPlan, bool) {
	from, to = math.Max(0, math.Floor(from)), math.Min(float64(m.Length), math.Ceil(to))
	if m.SamplesPerWord == 0 || !(to-from >= 2) {
		return core.ViewPlan{}, false
	}
	p := core.ViewPlan{Buckets: buckets}
	p.Bucket, p.Start, p.Length = envelopeWindow(uint32(from), uint32(to-from), buckets, m.Length, 0)
	p.N = int(p.Length) * int(m.SamplesPerWord)
	if p.Bucket != 0 {
		p.N = 2 * int(buckets)
	}
	return p, p.N > 0
}

// viewSpan is the record words [from, to) of a request for a, b screen widths
// from the screen's anchor: the trigger's word, or the middle of the frame's
// words [offset, offset+words) on an untriggered frame (EdgeX < 0), as the
// displays anchor it.
// TRLC-LINKS: REQ-SDS-021
func viewSpan(m sramcapture.Metadata, screen int, offset, words uint32, edgeX, a, b float64) (from, to float64) {
	anchor := float64(m.TriggerIndex)
	if edgeX < 0 {
		anchor = float64(offset) + float64(words)/2
	}
	screenWords := float64(screen) / float64(m.SamplesPerWord)
	return anchor + a*screenWords, anchor + b*screenWords
}

// viewRead is a planned view window's read: the plan and the frame its
// values land in.
type viewRead struct {
	p   core.ViewPlan
	f   *sample.Frame
	w   sramFrameWriter
	win *sample.Detail
}

// newViewRead prepares the read of planned view p of the record m.
// TRLC-LINKS: REQ-SDS-021
func newViewRead(m sramcapture.Metadata, p core.ViewPlan) *viewRead {
	n := p.N
	f := recordFrame(n)
	if m.FractionBits == 8 {
		f.Traces[0].Precision, f.Traces[1].Precision = make([]uint16, n), make([]uint16, n)
	}
	return &viewRead{p: p, f: f, w: sramFrameWriter{f: f, m: m}}
}

// record is the read as the capture makes it.
// TRLC-LINKS: REQ-SDS-021
func (r *viewRead) record() sramcapture.RecordRead {
	if r.p.Bucket != 0 {
		return sramcapture.RecordRead{Offset: r.p.Start, Bucket: r.p.Bucket, Count: r.p.Buckets, Dst: &r.w}
	}
	return sramcapture.RecordRead{Offset: r.p.Start, Count: r.p.Length, Dst: &r.w}
}

// window is the view window the read gave, with the live readout's
// conditioning; at maps a record word to the frame's value index. nil when
// the read did not fill it.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) window(m sramcapture.Metadata, r *viewRead, scale [2]core.ChScale, at func(w uint32) float64) *sample.Detail {
	p, f, n := r.p, r.f, r.p.N
	if r.w.n != n {
		return nil
	}
	sampleS := 1 / m.SampleRateHz
	v := &sample.Detail{From: at(p.Start), To: at(p.Start + p.Length), Frame: f}
	f.SampleS, f.Content = sampleS, sample.RawDecimated
	f.Traces[0].Precision, f.Traces[1].Precision = nil, nil // the record's, not the calibrated codes'
	if p.Bucket != 0 {
		f.SampleS = sampleS * float64(p.Bucket) * float64(m.SamplesPerWord) / 2
		f.Content = sample.PeakPairs
		f.Flags = sample.Ordered
		if cal := e.interleaveCalibration; cal != nil && m.Decimation <= 1 {
			q1, q2 := e.viewQ8(n)
			cal.debiasEnvelope(0, f.Codes8(0), q1, false)
			cal.debiasEnvelope(1, f.Codes8(1), q2, false)
		}
		orderEnvelopeHead(f.Codes8(0), nil)
		orderEnvelopeHead(f.Codes8(1), nil)
		orderEnvelope(f.Codes8(0), nil)
		orderEnvelope(f.Codes8(1), nil)
	} else if cal := e.interleaveCalibration; cal != nil && m.FractionBits == 0 && n >= 2000 {
		q1, q2 := e.viewQ8(n)
		cal.apply(f.Codes8(0), f.Codes8(1), q1, q2, [2]float64{scale[0].Vdiv, scale[1].Vdiv}, sampleS)
	}
	return v
}

// viewQ8 is scratch for a view window's Q8.8 values (two channels of n),
// which its calibration writes and the window does not keep: one buffer
// for the engine goroutine's reads, not two allocations a window a frame.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) viewQ8(n int) (q1, q2 []uint16) {
	if cap(e.viewQ) < 2*n {
		e.viewQ = make([]uint16, 2*n)
	}
	q := e.viewQ[:2*n]
	return q[:n:n], q[n:]
}

// viewReads is the view windows the surfaces ask for of a frame, planned:
// one read serves both surfaces when they ask for the same.
type viewReads [core.ViewSurfaces]*viewRead

// planViews plans the view windows the surfaces ask for of frame f, read from
// words [offset, offset+words) of the record in buckets of bucket words. They
// depend on the record's metadata alone, so they are read in the same
// request as the screen's envelope (RecallReadsOf): with the bus worker, one
// round trip for both, and a display render holding the CPU meanwhile is
// waited out once a frame, not twice.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) planViews(f *sample.Frame, m sramcapture.Metadata, screen int, offset, words, bucket, screenBuckets uint32) (v viewReads) {
	if m.SamplesPerWord == 0 || screen <= 0 || bucket == 0 {
		return v
	}
	for s := range core.ViewSurfaces {
		a, b, cols, ok := e.viewRequest(s)
		if !ok {
			continue
		}
		n := core.ViewBuckets(a, b, cols, screenBuckets)
		if n == 0 {
			continue
		}
		from, to := viewSpan(m, screen, offset, words, f.TriggerAt, a, b)
		p, ok := planView(m, from, to, n)
		if !ok {
			continue
		}
		for _, prev := range v[:s] {
			if prev != nil && prev.p == p {
				v[s] = prev
			}
		}
		if v[s] == nil {
			v[s] = newViewRead(m, p)
		}
	}
	return v
}

// reads appends the views' distinct reads to dst.
// TRLC-LINKS: REQ-SDS-021
func (v *viewReads) reads(dst []sramcapture.RecordRead) []sramcapture.RecordRead {
	for s, r := range v {
		if r != nil && (s == 0 || v[0] != r) {
			dst = append(dst, r.record())
		}
	}
	return dst
}

// windows gives frame f the view windows the reads filled (the frame read
// from word offset in buckets of bucket words).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) windows(f *sample.Frame, v *viewReads, m sramcapture.Metadata, offset, bucket uint32, scale [2]core.ChScale) {
	for s, r := range v {
		if r == nil {
			continue
		}
		if r.win == nil {
			r.win = e.window(m, r, scale, core.EnvelopeValue(offset, bucket))
		}
		if r.win != nil {
			d := *r.win
			d.Role = sample.RoleView(s)
			f.Details = append(f.Details, d)
		}
	}
}
