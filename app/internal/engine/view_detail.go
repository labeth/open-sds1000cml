// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"open-sds/app/internal/sramcapture"
)

// ViewWindow is a stretch of a live envelope frame's screen read again at
// full detail for a view that shows it magnified (the scope's zoom, the web
// page's): the screen's readout spreads its buckets over the whole screen, so
// a 50x zoom showed 80 points for 40 cycles of a 1 MHz square at 200us/div,
// aliased. It holds the same capture, as an envelope of its own buckets
// (time-ordered pairs) or raw samples when short.
type ViewWindow struct {
	C1, C2  []uint8
	Valid   int
	SampleS float64 // per value: half a bucket on an envelope
	// From, To are where it lies among the frame's values (fractional
	// indices of Frame.C1): whole buckets widen the request a little.
	From, To   float64
	PeakDetect bool
}

// ViewSurface is who asks for a view window.
type ViewSurface int

const (
	ViewScope ViewSurface = iota // the scope's zoom
	ViewWeb                      // the web page's zoom
	viewSurfaces
)

// viewSlot is one surface's request: until when, its ends (screen widths
// from the trigger, float64 bits) and the display columns it is drawn over.
type viewSlot struct {
	lease atomic.Int64
	a, b  atomic.Uint64
	cols  atomic.Int64
}

// viewMaxBuckets caps a view's buckets: a bucket a display column up to a
// browser canvas's width. A bucket costs the frame two bus reads (~7 us).
const viewMaxBuckets = 1024

// LeaseViewWindow asks, for surface s, for the stretch [a, b] of the live
// frames at full detail (ViewWindow) for d from now; d <= 0 ends the request.
// a and b are screen widths from the trigger (the record's middle on an
// untriggered frame): a zoom by z about the trigger is -0.5/z .. 0.5/z. cols
// is how many display columns show it. A surface's newest request wins.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) LeaseViewWindow(s ViewSurface, a, b float64, cols int, d time.Duration) {
	if s < 0 || s >= viewSurfaces {
		return
	}
	v := &e.views[s]
	if d <= 0 || !(b > a) || math.IsInf(b-a, 0) || cols < 2 {
		v.lease.Store(0)
		return
	}
	v.a.Store(math.Float64bits(a))
	v.b.Store(math.Float64bits(b))
	v.cols.Store(int64(cols))
	e.lease(&v.lease, d)
}

// viewRequest is surface s's current request, if any.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) viewRequest(s ViewSurface) (a, b float64, cols int, ok bool) {
	v := &e.views[s]
	if !e.leased(&v.lease) {
		return 0, 0, 0, false
	}
	a, b = math.Float64frombits(v.a.Load()), math.Float64frombits(v.b.Load())
	return a, b, int(v.cols.Load()), b > a
}

// viewBuckets is how many buckets a view of b-a screen widths shown over cols
// columns is read in; 0 when the screen's envelope (screen buckets across
// it) has a bucket at least every second column there already. Magnified
// further, a bucket spans many columns and its (min, max) pair draws as a
// slope: the 1 MHz square at 200us/div zoomed 50x read as a slow wave.
// TRLC-LINKS: REQ-SDS-021
func viewBuckets(a, b float64, cols int, screen uint32) uint32 {
	if !(b > a) || cols < 2 || 2*float64(screen)*(b-a) >= float64(cols) {
		return 0
	}
	n := uint32(min(cols, viewMaxBuckets))
	return n + n&1 // the reducer takes buckets in pairs
}

// viewPlan is how a view's stretch is read: buckets of bucket words (0: raw
// samples) over the record words [start, start+length), n values a channel.
type viewPlan struct {
	bucket, start, length, buckets uint32
	n                              int
}

// planView plans the read of the record's words [from, to) in buckets
// buckets: whole buckets about its centre, raw when that is short. false when
// the stretch is empty.
// TRLC-LINKS: REQ-SDS-021
func planView(m sramcapture.Metadata, from, to float64, buckets uint32) (viewPlan, bool) {
	from, to = math.Max(0, math.Floor(from)), math.Min(float64(m.Length), math.Ceil(to))
	if m.SamplesPerWord == 0 || !(to-from >= 2) {
		return viewPlan{}, false
	}
	p := viewPlan{buckets: buckets}
	p.bucket, p.start, p.length = envelopeWindow(uint32(from), uint32(to-from), buckets, m.Length)
	p.n = int(p.length) * int(m.SamplesPerWord)
	if p.bucket != 0 {
		p.n = 2 * int(buckets)
	}
	return p, p.n > 0
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

// recallViewWindow reads planned view p with the live readout's
// conditioning; at maps a record word to the frame's value index. nil when
// the read fails.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) recallViewWindow(ctx context.Context, m sramcapture.Metadata, p viewPlan, scale [2]chScale, at func(w uint32) float64) *ViewWindow {
	n := p.n
	f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n}
	if m.FractionBits == 8 {
		f.Q1, f.Q2 = make([]uint16, n), make([]uint16, n)
	}
	writer := sramFrameWriter{f: f, m: m}
	var err error
	switch {
	case p.bucket != 0:
		_, err = e.sram.RecallEnvelope(ctx, p.start, p.bucket, p.buckets, &writer)
	case m.Revision == 10:
		_, err = e.sram.RecallForward(ctx, p.start, p.length, &writer)
	default:
		_, err = e.sram.Recall(ctx, p.start, p.length, &writer)
	}
	if err != nil || writer.n != n {
		return nil
	}
	sampleS := 1 / m.SampleRateHz
	v := &ViewWindow{C1: f.C1, C2: f.C2, Valid: n, SampleS: sampleS, From: at(p.start), To: at(p.start + p.length), PeakDetect: p.bucket != 0}
	if p.bucket != 0 {
		v.SampleS = sampleS * float64(p.bucket) * float64(m.SamplesPerWord) / 2
		if cal := e.interleaveCalibration; cal != nil && m.Decimation <= 1 {
			q1, q2 := make([]uint16, n), make([]uint16, n)
			cal.debiasEnvelope(0, f.C1, q1, false)
			cal.debiasEnvelope(1, f.C2, q2, false)
		}
		orderEnvelopeHead(f.C1, nil)
		orderEnvelopeHead(f.C2, nil)
		orderEnvelope(f.C1, nil)
		orderEnvelope(f.C2, nil)
	} else if cal := e.interleaveCalibration; cal != nil && m.FractionBits == 0 && n >= 2000 {
		q1, q2 := make([]uint16, n), make([]uint16, n)
		cal.apply(f.C1, f.C2, q1, q2, [2]float64{scale[0].vdiv, scale[1].vdiv}, sampleS)
	}
	return v
}

// envelopeValue maps a record word to the value index of an envelope frame
// read from word offset in buckets of bucket words: a value a half bucket.
// TRLC-LINKS: REQ-SDS-021
func envelopeValue(offset, bucket uint32) func(w uint32) float64 {
	return func(w uint32) float64 { return (float64(w) - float64(offset)) * 2 / float64(bucket) }
}

// readViews reads the frame's view windows the surfaces ask for (the frame
// read from words [offset, offset+words) of the record in buckets of bucket
// words, still frozen); one read serves both when they ask for the same.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) readViews(f *Frame, m sramcapture.Metadata, screen int, offset, words, bucket, screenBuckets uint32, scale [2]chScale) {
	if m.SamplesPerWord == 0 || screen <= 0 || bucket == 0 {
		return
	}
	var asked [viewSurfaces]viewPlan
	for s := range viewSurfaces {
		a, b, cols, ok := e.viewRequest(s)
		if !ok {
			continue
		}
		n := viewBuckets(a, b, cols, screenBuckets)
		if n == 0 {
			continue
		}
		from, to := viewSpan(m, screen, offset, words, f.EdgeX, a, b)
		p, ok := planView(m, from, to, n)
		if !ok {
			continue
		}
		asked[s] = p
		for q := range s {
			if asked[q] == p && f.Views[q] != nil {
				f.Views[s] = f.Views[q]
			}
		}
		if f.Views[s] != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		f.Views[s] = e.recallViewWindow(ctx, m, p, scale, envelopeValue(offset, bucket))
		cancel()
	}
}
