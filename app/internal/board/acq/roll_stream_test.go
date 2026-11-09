// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// scaleFE is a front end at a V/div the test sets, whose setters leave
// their writes due.
type scaleFE struct {
	writerFE
	vdiv float64
}

func (f *scaleFE) Get(int) hal.Channel { return hal.Channel{On: true, VoltsDiv: f.vdiv, Probe: 1} }

// q88Scale is the Q8.8 words' scale at vdiv.
func q88Scale(vdiv float64) sample.Scale {
	return sample.Scale{Zero: 0x8000, PerCode: vdiv / (CodesPerDiv * 256)}
}

// While the board rolls in chunks, each chunk is armed on the trigger
// staged at its arm and states the scale it was captured at; a front-end
// change is written between chunks, the chunks armed in the settle after it
// are dropped (empty blocks at the new scale), and the first chunk after
// the settle follows the dropped time held at the last value carried over
// to the new scale.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-015
func TestRollChunksTakeSettings(t *testing.T) {
	b, fm, _ := newTestBoard()
	m := &rollMem{fakeMem: fm, base: 0x8800}
	b.mem = m
	fe := &scaleFE{vdiv: 1}
	b.SetFrontEnd(fe)
	now := time.Unix(100, 0)
	b.SetLog(nil, func() time.Time { return now })
	b.SetSleep(func(time.Duration) {})
	plan, err := b.Planner().Plan(hal.Request{Channels: []int{0}, TimePerDivS: 10e-3, Mode: hal.RollMode})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Request.Channels) != 2 {
		t.Fatalf("the roll plan streams %v, want both channels", plan.Request.Channels)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	st, _ := b.Streamer()
	if err := st.Start(hal.StreamSpec{Payload: hal.PayloadChunks, Channels: plan.Request.Channels}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := PlanRoll(10e-3)
	chunkS := float64(p.Chunk) * p.SampleS
	lease := func() hal.StreamBlock {
		t.Helper()
		now = now.Add(time.Duration(chunkS * float64(time.Second)))
		blk, err := st.Lease(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return blk
	}
	blk := lease()
	if len(blk.Scales) != 2 || blk.Scales[0] != q88Scale(1) {
		t.Fatalf("scales %+v", blk.Scales)
	}
	src := hal.Source{Kind: hal.Analog, Index: 1}
	edge := hal.TrigStage{Node: hal.NodeEdge, Source: src, Levels: hal.Levels{Slope: hal.Rising}}
	if err := b.Trigger().Program(hal.TriggerSpec{Stages: []hal.TrigStage{edge}}); err != nil {
		t.Fatal(err)
	}
	lease()
	if c := m.armed[len(m.armed)-1]; c.TriggerChannel != b.StagedTrigger().TriggerChannel || c.TriggerChannel != 1 {
		t.Fatalf("chunk armed on channel %d, the staged trigger's %d", c.TriggerChannel, b.StagedTrigger().TriggerChannel)
	}
	last := m.base - 1 // the newest sample kept
	// a V/div step: written before the next chunk, which is dropped
	fe.vdiv, fe.due = 2, true
	blk = lease()
	if fe.flushes != 1 || len(blk.Held) != 0 || blk.Scales[0] != q88Scale(2) {
		t.Fatalf("after the step: %d flushes, %d samples, scale %+v", fe.flushes, len(blk.Held), blk.Scales)
	}
	for range 3 { // still in the settle
		if blk = lease(); len(blk.Held) != 0 {
			t.Fatalf("a chunk armed in the settle: %d samples", len(blk.Held))
		}
	}
	now = now.Add(rollSettle)
	blk = lease()
	_, w := blk.Run(0)
	held := 0
	for held < len(blk.Held) && blk.Held[held] {
		held++
	}
	if held == 0 || len(w) != held+p.Chunk {
		t.Fatalf("after the settle %d samples, %d held", len(w), held)
	}
	// the held value is the last sample kept, at the new V/div
	if want := 0x8000 + (int(last)-0x8000+1)/2; int(w[0]) != want {
		t.Fatalf("held value %04x, want %04x: the last value at 2 V/div", w[0], want)
	}
}

// peakMem is the stream image's drum of peak words.
type peakMem struct {
	drumMem
	peakCfg []sramcapture.Config
}

func (m *peakMem) StartStream(_ context.Context, cfg sramcapture.Config, continuous, peak bool, _ sramcapture.StreamPace) error {
	if continuous && peak {
		m.peakCfg = append(m.peakCfg, cfg)
	}
	m.next = 0
	return nil
}

// peakWords is n peak words ({min1, min2, max1, max2}).
func peakWords(n int, lo, hi byte) []byte {
	out := make([]byte, 0, 4*n)
	for range n {
		out = append(out, lo, lo, hi, hi)
	}
	return out
}

// With the roll-stream preference the board rolls on the stream image's
// peak words: the planner says so (peak pairs at the engine's geometry),
// Start loads the stream image and starts the peak stream, a word is two
// view samples ordered to continue from the sample before, and a V/div
// change drops the words in its settle and places what follows by the
// host clock, held at the last pair.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func TestRollStreamOnTheStreamImage(t *testing.T) {
	b, dm, sw := newDrumBoard(t)
	m := &peakMem{drumMem: *dm}
	b.mem = m
	fe := &scaleFE{vdiv: 1}
	b.SetFrontEnd(fe)
	now := time.Unix(100, 0)
	b.SetLog(nil, func() time.Time { return now })
	b.SetSleep(func(time.Duration) {})
	b.SetRollStream(true)
	if !b.RollStream() {
		t.Fatal("the preference did not stay")
	}
	plan, err := b.Planner().Plan(hal.Request{Channels: []int{0, 1}, TimePerDivS: 100e-3, Mode: hal.RollMode})
	if err != nil {
		t.Fatal(err)
	}
	pp := PlanRollPeak(100e-3)
	if !plan.Content.Has(sample.PeakPairs) || plan.SampleS != pp.SampleS || plan.WinSamples != pp.Screen || plan.AcqDecim != 1<<pp.Log {
		t.Fatalf("plan %+v for %+v", plan, pp)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	st, _ := b.Streamer()
	if err := st.Start(hal.StreamSpec{Payload: hal.PayloadChunks, Channels: []int{0, 1}}); err != nil {
		t.Fatal(err)
	}
	if sw.loaded != "stream" || len(m.peakCfg) != 1 || m.peakCfg[0].DecimationLog2 != pp.Log {
		t.Fatalf("image %q, peak streams %+v", sw.loaded, m.peakCfg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m.banks = [][]byte{append(peakWords(2, 10, 20), peakWords(1, 30, 40)...)}
	blk, err := st.Lease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, w := blk.Run(0)
	// codes 10/20 twice, then 30/40: ordered from the first sample on
	want := []uint16{10 << 8, 20 << 8, 20 << 8, 10 << 8, 30 << 8, 40 << 8}
	if len(w) != len(want) || blk.Payload != hal.PayloadChunks || len(blk.Scales) != 2 || blk.Scales[0] != q88Scale(1) {
		t.Fatalf("block %v scales %+v", w, blk.Scales)
	}
	for i := range want {
		if w[i] != want[i] {
			t.Fatalf("view samples %v, want %v", w, want)
		}
	}
	fe.vdiv, fe.due = 2, true
	m.banks = [][]byte{peakWords(4, 50, 60)}
	if blk, err = st.Lease(ctx); err != nil || len(blk.Held) != 0 || blk.Scales[0] != q88Scale(2) || fe.flushes != 1 {
		t.Fatalf("in the settle: %v, %d samples, scales %+v, %d flushes", err, len(blk.Held), blk.Scales, fe.flushes)
	}
	now = now.Add(rollSettle + 100*time.Millisecond)
	m.banks = [][]byte{peakWords(1, 70, 80)}
	if blk, err = st.Lease(ctx); err != nil {
		t.Fatal(err)
	}
	_, w = blk.Run(0)
	gap := len(w) - 2
	if gap < 1 || !blk.Held[0] || blk.Held[gap] {
		t.Fatalf("after the settle: %d samples, held %v", len(w), blk.Held)
	}
	// held at the last pair (30, 40) at 2 V/div: (codes - 128)/2 + 128
	if w[0] != uint16(79<<8) || w[1] != uint16(84<<8) {
		t.Fatalf("held %04x %04x, want the last pair at the new V/div", w[0], w[1])
	}
	if err := st.Stop(); err != nil {
		t.Fatal(err)
	}
	b.SetRollStream(false)
	if plan, _ = b.Planner().Plan(hal.Request{Channels: []int{0, 1}, TimePerDivS: 100e-3, Mode: hal.RollMode}); plan.Content.Has(sample.PeakPairs) {
		t.Fatal("the preference off still streams")
	}
}
