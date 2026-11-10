// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"io"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// rampMem recalls a ramp: the record's sample s reads code s on C1 and
// s+100 on C2 (modulo 256), two samples a word.
type rampMem struct{ *fakeMem }

func (m rampMem) RecallForward(_ context.Context, offset, count uint32, dst io.Writer) (int64, error) {
	b := make([]byte, 4*count)
	for i := range count {
		s := 2 * (offset + i)
		b[4*i], b[4*i+1], b[4*i+2], b[4*i+3] = byte(s), byte(s+100), byte(s+1), byte(s+101)
	}
	n, err := dst.Write(b)
	return int64(n), err
}

// RecallReadsOf reads each window, a bucketed one as the fabric's
// envelope: a word a bucket of each channel's minimum and maximum.
func (m rampMem) RecallReadsOf(ctx context.Context, _ sramcapture.Metadata, reads ...sramcapture.RecordRead) error {
	for _, r := range reads {
		if r.Bucket == 0 {
			if _, err := m.RecallForward(ctx, r.Offset, r.Count, r.Dst); err != nil {
				return err
			}
			continue
		}
		m.envelopes++
		b := make([]byte, 4*r.Count)
		for i := range r.Count {
			first, last := 2*(r.Offset+i*r.Bucket), 2*(r.Offset+(i+1)*r.Bucket)-1 // a ramp: its extremes
			b[4*i], b[4*i+1], b[4*i+2], b[4*i+3] = byte(first), byte(first+100), byte(last), byte(last+100)
		}
		if _, err := r.Dst.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// A frame the caller leaves empty is the board's to size: any window of
// samples (whole words or not) reads exactly those samples, a Span of 0 is
// the plan's screen around the trigger, a window keeps only the channels
// it names, a compressed window delivers its points (peak pairs in time
// order), and a record or content the board does not hold is refused.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func TestBoardSizedWindows(t *testing.T) {
	m := &fakeMem{freezeAt: 1, caps: map[string]bool{}}
	b := New(Config{Memory: rampMem{m}, Panel: &cs3Log{}})
	b.SetFrontEnd(frontEnd{})
	b.ProbeImage(func(string, ...any) {})
	plan, err := b.Planner().Plan(hal.Request{Channels: []int{0, 1}, TimePerDivS: 1e-6, PretrigFrac: .5})
	if err != nil || plan.WinSamples != 5000 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquirer().Arm(); err != nil {
		t.Fatal(err)
	}
	m.meta.Frozen, m.meta.Ready, m.meta.Triggered = true, true, true
	m.meta.Length, m.meta.TriggerIndex, m.meta.SamplesPerWord, m.meta.Revision, m.meta.RecordID = 8192, 4096, 2, 10, 9
	st, err := b.Acquirer().Wait(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	read := func(w hal.Window) (*sample.Frame, error) {
		f := &sample.Frame{}
		return f, b.Readout().Read(context.Background(), hal.ReadRequest{Record: st.Record, Windows: []hal.Window{w}}, []*sample.Frame{f}, nil)
	}
	trig := 2 * 4096 // the trigger's sample
	f, err := read(hal.Window{Role: hal.Main, StartSample: -3, Span: 7})
	if err != nil || f.Len() != 7 || f.TriggerAt != 3 || f.Content != sample.RawFullRate || len(f.Traces) != 2 {
		t.Fatalf("7 samples about the trigger: %+v %v", f, err)
	}
	for k := range 7 {
		if c := f.Traces[0].Codes8[k]; c != byte(trig-3+k) || f.Traces[1].Codes8[k] != byte(trig-3+k+100) {
			t.Fatalf("sample %d: C1 %d C2 %d", k, c, f.Traces[1].Codes8[k])
		}
	}
	if f.Traces[0].Scale.PerCode == 0 || f.TrigKind != sample.HardwareNode || f.Status.Record != st.Record {
		t.Fatalf("frame facts %+v", f)
	}
	f, err = read(hal.Window{Role: hal.Main, Channels: []int{1}})
	if err != nil || f.Len() != 5000 || f.TriggerAt != 2500 || len(f.Traces) != 1 || f.Traces[0].Ch != 1 {
		t.Fatalf("the screen of C2: len %d trigger at %g, %d traces (%v)", f.Len(), f.TriggerAt, len(f.Traces), err)
	}
	if f.Traces[0].Codes8[0] != byte(trig-2500+100) {
		t.Fatalf("the screen starts at code %d", f.Traces[0].Codes8[0])
	}
	f, err = read(hal.Window{Role: hal.Zoom, StartSample: 0, Span: 8, Points: 2, Want: sample.PeakPairs})
	if err != nil || f.Content != sample.PeakPairs || f.Len() != 4 {
		t.Fatalf("two peak pairs: %+v %v", f, err)
	}
	if c := f.Traces[0].Codes8[:4]; c[0] != byte(trig&255) || c[1] != byte(trig+3) || c[2] != byte(trig+4) || c[3] != byte(trig+7) {
		t.Fatalf("peak pairs %v", c)
	}
	if m.envelopes == 0 {
		t.Fatal("a window of whole buckets was reduced on the host, not by the fabric")
	}
	f, err = read(hal.Window{Role: hal.Zoom, StartSample: 0, Span: 12, Points: 2, Want: sample.PeakPairs})
	if err != nil || f.Content != sample.PeakPairs || f.Len() != 4 {
		t.Fatalf("two peak pairs of odd buckets: %+v %v", f, err)
	}
	if c := f.Traces[0].Codes8[:4]; c[0] != byte(trig&255) || c[1] != byte(trig+5) || c[2] != byte(trig+6) || c[3] != byte(trig+11) {
		t.Fatalf("peak pairs of odd buckets %v", c)
	}
	if _, err := read(hal.Window{Role: hal.Main, Want: sample.RawDecimated}); err == nil {
		t.Fatal("decimated samples of a full-rate record read")
	} else if _, ok := hal.AsRefusal(err); !ok {
		t.Fatalf("decimated samples of a full-rate record: %v", err)
	}
	f = &sample.Frame{}
	err = b.Readout().Read(context.Background(), hal.ReadRequest{Record: sample.RecordID{Seq: 1}, Windows: []hal.Window{{Role: hal.Main}}}, []*sample.Frame{f}, nil)
	if _, ok := hal.AsRefusal(err); !ok {
		t.Fatalf("another record: %v", err)
	}
}
