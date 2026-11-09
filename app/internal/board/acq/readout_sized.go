// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// recordContent is what record m holds: two 2 ns samples a word are the
// full rate, any other word a decimated sample; peak pairs of either are
// the fabric's bucketed reads.
// TRLC-LINKS: REQ-SDS-010
func recordContent(m sramcapture.Metadata) sample.ContentSet {
	if m.SamplesPerWord == 2 && m.FractionBits == 0 {
		return sample.ContentSetOf(sample.RawFullRate, sample.PeakPairs)
	}
	return sample.ContentSetOf(sample.RawDecimated, sample.PeakPairs)
}

// checkRequest refuses what no read of the halted record serves: another
// record, segments, or a content kind the record does not hold.
// TRLC-LINKS: REQ-SDS-010
func (b *Board) checkRequest(req *hal.ReadRequest) error {
	if req.Record != (sample.RecordID{}) && req.Record != recordID(b.rec) {
		return hal.Refuse("record", "the SRAM keeps only its newest record")
	}
	if req.Segments != [2]int{} && req.Segments != [2]int{0, 1} {
		return hal.Refuse("segments", "the SRAM record memory keeps one record")
	}
	have := recordContent(b.rec)
	for i := range req.Windows {
		if w := req.Windows[i].Want; w != 0 && !have.Has(w) {
			return hal.Refuse(w.String(), fmt.Sprintf("the record at this time/div holds %v", have))
		}
	}
	return nil
}

// sized is a window whose frame the board sizes: the record words it
// reads, and how the samples read become the window asked for.
type sized struct {
	on     bool
	w      hal.Window // the window asked for, resolved (Span > 0)
	skip   int        // samples read before the window's first
	bucket int        // samples a delivered point (1: every sample)
	fabric bool       // peak pairs the fabric reduced (fabricEnvelope): read as they are
}

// sizeWindows resolves the windows of dst's empty frames (the board sizes
// them): a Span of 0 is the plan's screen, centred DelayS after the
// trigger, within the record; each is read as whole words into a frame of
// its own. It returns the request to read and what to make of each frame.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func (b *Board) sizeWindows(req hal.ReadRequest, dst []*sample.Frame) (hal.ReadRequest, []sized, error) {
	m := b.rec
	per := int64(m.SamplesPerWord)
	if per <= 0 {
		return req, nil, fmt.Errorf("record of %d samples a word", per)
	}
	first, last := -int64(m.TriggerIndex)*per, (int64(m.Length)-int64(m.TriggerIndex))*per
	out := req
	out.Windows = slices.Clone(req.Windows)
	sz := make([]sized, len(dst))
	for i, f := range dst {
		if len(f.Traces) != 0 {
			continue
		}
		w := req.Windows[i]
		if w.Span == 0 {
			w.StartSample, w.Span = b.screenWindow(first, last)
		}
		if w.Span < 0 || w.StartSample < first || w.StartSample+w.Span > last {
			return req, nil, hal.Refuse("readout window", fmt.Sprintf(
				"samples %d…%d from the trigger lie outside the record (%d…%d)", w.StartSample, w.StartSample+w.Span, first, last))
		}
		if fabricEnvelope(m, w) {
			// the fabric's own envelope: a (min, max) pair a bucket, read as
			// it is, not the window's samples reduced here
			sz[i] = sized{on: true, w: w, bucket: int(w.Span) / w.Points, fabric: true}
			out.Windows[i] = w
			*f = *RecordFrame(2 * w.Points)
			if m.FractionBits == 8 {
				for ch := range f.Traces {
					f.Traces[ch].Precision = make([]uint16, 2*w.Points)
				}
			}
			continue
		}
		a0 := floorTo(w.StartSample, per)
		a1 := -floorTo(-(w.StartSample + w.Span), per)
		s := sized{on: true, w: w, skip: int(w.StartSample - a0), bucket: 1}
		if w.Points > 0 && int64(w.Points) < w.Span {
			s.bucket = int((w.Span + int64(w.Points) - 1) / int64(w.Points))
		}
		sz[i] = s
		raw := sample.RawDecimated
		if recordContent(m).Has(sample.RawFullRate) {
			raw = sample.RawFullRate
		}
		out.Windows[i] = hal.Window{Role: w.Role, StartSample: a0, Span: a1 - a0, Want: raw}
		*f = *RecordFrame(int(a1 - a0))
		f.SampleS = b.armedPlan.SampleS
		if m.SampleRateHz > 0 {
			f.SampleS = 1 / m.SampleRateHz
		}
		if m.FractionBits == 8 {
			for ch := range f.Traces {
				f.Traces[ch].Precision = make([]uint16, a1-a0)
			}
		}
	}
	return out, sz, nil
}

// fabricEnvelope reports a peak-pairs window w of record m that the
// fabric's envelope reads: whole buckets of an even number of words, a
// bucket at least two words, within one SRAM's worth of words (the windows
// ScreenWindow places, EnvelopeWindow).
// TRLC-LINKS: REQ-SDS-010
func fabricEnvelope(m sramcapture.Metadata, w hal.Window) bool {
	per := int64(m.SamplesPerWord)
	if w.Want != sample.PeakPairs || w.Points <= 0 || per <= 0 || w.Span <= 0 || w.Span%per != 0 || w.StartSample%per != 0 {
		return false
	}
	words := w.Span / per
	if words%int64(w.Points) != 0 || words > int64(sramcapture.Words-16) {
		return false
	}
	bucket := words / int64(w.Points)
	return bucket >= 2 && bucket%2 == 0
}

// floorTo is v rounded down to a multiple of n.
// TRLC-LINKS: REQ-SDS-010
func floorTo(v, n int64) int64 {
	q := v / n
	if v%n != 0 && v < 0 {
		q--
	}
	return q * n
}

// screenWindow is the armed plan's screen in the record's samples from the
// trigger: WinSamples centred DelayS after it, moved inside [first, last);
// the whole record when the screen is wider or the plan has none.
// TRLC-LINKS: REQ-SDS-010
func (b *Board) screenWindow(first, last int64) (start, span int64) {
	p := b.armedPlan
	span = int64(p.WinSamples)
	if span <= 0 || span >= last-first || !(p.SampleS > 0) {
		return first, last - first
	}
	start = int64(math.Round(p.Request.DelayS/p.SampleS)) - span/2
	start = min(max(start, first), last-span)
	return start, span
}

// finishSized makes each board-sized frame the window asked for: the
// samples read before it dropped, its points picked (or, for peak pairs,
// each bucket's extremes in time order), the channels asked kept, and the
// record's facts stated.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func (b *Board) finishSized(sz []sized, dst []*sample.Frame) {
	for i, s := range sz {
		if !s.on {
			continue
		}
		f := dst[i]
		if s.fabric {
			for ch := range f.Traces {
				f.Traces[ch].Precision = nil // a pair is codes
				f.Traces[ch].Valid = f.Len()
			}
			b.stateFrame(f, s)
			f.Traces = keepChannels(f.Traces, s.w.Channels)
			continue
		}
		n := int(s.w.Span)
		for ch := range f.Traces {
			t := &f.Traces[ch]
			copy(t.Codes8, t.Codes8[s.skip:s.skip+n])
			t.Codes8 = t.Codes8[:n]
			if t.Precision != nil {
				copy(t.Precision, t.Precision[s.skip:s.skip+n])
				t.Precision = t.Precision[:n]
			}
			t.Valid = n
		}
		b.stateFrame(f, s)
		f.Traces = keepChannels(f.Traces, s.w.Channels)
	}
}

// stateFrame compresses frame f as window s asks and states the record's
// facts in it: content, rates, trigger, status and each trace's scale.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func (b *Board) stateFrame(f *sample.Frame, s sized) {
	m, p := b.rec, b.armedPlan
	content := sample.RawDecimated
	if recordContent(m).Has(sample.RawFullRate) {
		content = sample.RawFullRate
	}
	peak := s.w.Want == sample.PeakPairs
	n, half := f.Len(), 1.0
	switch {
	case peak && s.fabric:
		content, half = sample.PeakPairs, 2
	case peak:
		n = peakPairs(f, s.bucket)
		content, half = sample.PeakPairs, 2
	case s.bucket > 1:
		n = pick(f, s.bucket)
		content = sample.RawDecimated
	}
	f.Content = content
	f.Flags.Set(sample.Ordered, true)
	f.Flags.Set(sample.Precision, f.Traces[0].Precision != nil)
	f.SampleS = p.SampleS * float64(s.bucket) / half
	f.CaptureS = p.SampleS / float64(max(p.AcqDecim, 1))
	f.AcqDecim = max(p.AcqDecim, 1)
	f.Depth = int(m.Length) * int(m.SamplesPerWord)
	f.WinPoints = int(math.Round(float64(p.WinSamples) / float64(s.bucket) * half))
	f.TriggerAt = float64(-s.w.StartSample) / float64(s.bucket) * half
	st := b.recStatus
	f.Status, f.ContentID = st, uint64(m.EventEpoch)<<32|uint64(m.RecordID)
	switch {
	case b.armedFree:
		f.TrigKind = sample.Untriggered
	case st.Triggered:
		f.TrigKind, f.TrigNode = sample.HardwareNode, b.armedNode.String()
	case st.Forced:
		f.TrigKind = sample.Forced
	default:
		f.TrigKind = sample.Untriggered
	}
	tdiv := p.Request.TimePerDivS
	f.Snapshot = sample.Settings{TdivS: tdiv, DisplayedS: tdiv, Norm: b.armedNorm}
	f.SetLen(n)
	for ch := range f.Traces {
		f.Traces[ch].Scale = b.levelScale(ch)
	}
	if s.w.Role == sample.Main {
		b.noteMeans(f)
	}
}

// pick keeps every bucket-th sample of f's traces and returns how many.
// TRLC-LINKS: REQ-SDS-010
func pick(f *sample.Frame, bucket int) int {
	n := (f.Len() + bucket - 1) / bucket
	for ch := range f.Traces {
		t := &f.Traces[ch]
		for i := range n {
			t.Codes8[i] = t.Codes8[i*bucket]
			if t.Precision != nil {
				t.Precision[i] = t.Precision[i*bucket]
			}
		}
		t.Codes8 = t.Codes8[:n]
		if t.Precision != nil {
			t.Precision = t.Precision[:n]
		}
	}
	return n
}

// peakPairs turns f's traces into each bucket's (min, max) in time order,
// two codes a bucket, and returns the codes a trace holds. The precision
// words go: a pair is codes.
// TRLC-LINKS: REQ-SDS-010
func peakPairs(f *sample.Frame, bucket int) int {
	n := f.Len()
	pairs := (n + bucket - 1) / bucket
	for ch := range f.Traces {
		t := &f.Traces[ch]
		c := t.Codes8
		if bucket < 2 { // a pair a sample: the pairs need room of their own
			c = append(make([]uint8, 0, 2*pairs), c[:n]...)[:2*pairs]
		}
		for i := range pairs {
			src := t.Codes8[i*bucket : min((i+1)*bucket, n)]
			lo, hi := src[0], src[0]
			for _, v := range src {
				lo, hi = min(lo, v), max(hi, v)
			}
			c[2*i], c[2*i+1] = lo, hi
		}
		t.Codes8, t.Precision = c[:2*pairs], nil
		OrderEnvelopeHead(t.Codes8, nil)
		OrderEnvelope(t.Codes8, nil)
	}
	return 2 * pairs
}

// keepChannels is traces without the channels a window did not ask for
// (none named: all of them).
// TRLC-LINKS: REQ-SDS-010
func keepChannels(traces []sample.Trace, chans []int) []sample.Trace {
	if len(chans) == 0 {
		return traces
	}
	out := traces[:0]
	for _, t := range traces {
		if slices.Contains(chans, t.Ch) {
			out = append(out, t)
		}
	}
	return out
}

// readSized reads req's windows when the board sizes some of their
// frames.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func (b *Board) readSized(ctx context.Context, req hal.ReadRequest, dst []*sample.Frame, yield func()) error {
	rr, sz, err := b.sizeWindows(req, dst)
	if err != nil {
		return err
	}
	if err := b.readRecord(ctx, rr, dst, yield); err != nil {
		return err
	}
	for i, f := range dst {
		if n := b.scratch.written[i]; n != f.Len() {
			if rr.Windows[i].Role == sample.Main {
				return ErrShortRead
			}
			f.SetLen(n)
			sz[i].w.Span = int64(max(0, n-sz[i].skip))
			continue
		}
		b.condition(b.rec, rr.Windows[i], b.scratch.windows[i], f)
	}
	b.finishSized(sz, dst)
	b.placeTrigger(rr.Windows, dst)
	return nil
}
