// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// rollMem freezes every capture at once with the armed words, C1 counting
// up from the capture's base and C2 at a fixed level.
type rollMem struct {
	*fakeMem
	base uint16
}

func (m *rollMem) Status() (sramcapture.Metadata, error) {
	c := m.armed[len(m.armed)-1]
	return sramcapture.Metadata{Frozen: true, Ready: true, Length: c.PreWords + c.PostWords, Revision: 11}, nil
}

func (m *rollMem) Recall(_ context.Context, _, count uint32, dst io.Writer) (int64, error) {
	out := make([]byte, 4*count)
	for i := range count {
		binary.LittleEndian.PutUint16(out[4*i:], m.base+uint16(i))
		binary.LittleEndian.PutUint16(out[4*i+2:], 0x9000)
	}
	m.base += uint16(count)
	n, err := dst.Write(out)
	return int64(n), err
}

// Roll on the board is back-to-back captures of the roll plan's chunk at
// its decimation, each after the samples that held since the one before
// (the host clock's gap), C1 and C2 as Q8.8 view samples; beyond the
// largest decimation a group of captured samples averages into one.
// TRLC-LINKS: REQ-SDS-010
func TestRollCaptures(t *testing.T) {
	b, fm, _ := newTestBoard()
	m := &rollMem{fakeMem: fm, base: 0x8000}
	b.mem = m
	now := time.Unix(100, 0)
	b.SetLog(nil, func() time.Time { return now })
	b.SetSleep(func(time.Duration) {})
	r := hal.Request{Channels: []int{0, 1}, TimePerDivS: 10e-3, Mode: hal.RollMode}
	plan, err := b.Planner().Plan(r)
	if err != nil {
		t.Fatal(err)
	}
	p := PlanRoll(10e-3)
	if plan.WinSamples != p.Screen || plan.SampleS != p.SampleS || plan.AcqDecim != 1<<p.Log || p.Group != 1 {
		t.Fatalf("plan %+v, roll plan %+v", plan, p)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	st, _ := b.Streamer()
	if err := st.Start(hal.StreamSpec{Payload: hal.PayloadChunks, Channels: []int{0, 1}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	blk, err := st.Lease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := m.armed[len(m.armed)-1]
	if c.DecimationLog2 != p.Log || int(c.PreWords+c.PostWords) != p.Chunk || c.PostWords != 1 {
		t.Fatalf("armed %+v for %+v", c, p)
	}
	_, w1 := blk.Run(0)
	_, w2 := blk.Run(1)
	if blk.Payload != hal.PayloadChunks || blk.First != 0 || len(w1) != p.Chunk || len(blk.Held) != p.Chunk || blk.Held[0] || w1[0] != 0x8000 || w2[0] != 0x9000 {
		t.Fatalf("first chunk: %d samples, held %v.., %04x %04x", len(w1), blk.Held[:1], w1[:1], w2[:1])
	}
	// 10 ms later than the chunk itself lasts: the samples between hold the last value
	gapS := 10e-3
	now = now.Add(time.Duration((float64(p.Chunk)*p.SampleS + gapS) * float64(time.Second)))
	blk, err = st.Lease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gap := int(gapS/p.SampleS + .5)
	_, w1 = blk.Run(0)
	if blk.First != uint64(p.Chunk) || len(w1) != gap+p.Chunk || !blk.Held[0] || !blk.Held[gap-1] || blk.Held[gap] ||
		w1[0] != 0x8000+uint16(p.Chunk-1) || w1[gap] != 0x8000+uint16(p.Chunk) {
		t.Fatalf("second chunk: %d samples (gap %d), held %v %v %v, %04x %04x", len(w1), gap, blk.Held[0], blk.Held[gap-1], blk.Held[gap], w1[0], w1[gap])
	}
	if err := st.Stop(); err != nil {
		t.Fatal(err)
	}
	// 50 s/div: two captured samples a view sample at the largest decimation
	if g := PlanRoll(50); g.Group < 2 || g.Log != 20 {
		t.Fatalf("50 s/div %+v", g)
	}
	if _, err := b.Planner().Plan(hal.Request{Channels: []int{0}, TimePerDivS: 50, Mode: hal.RollMode}); err != nil {
		t.Fatal(err)
	}
}

// A software group averages its captured samples, held ones counted at the
// last value; a partial group waits for the next capture.
// TRLC-LINKS: REQ-SDS-010
func TestRollGroup(t *testing.T) {
	r := rollState{plan: RollPlan{Group: 3, Screen: 100}, chans: []int{1}}
	raw := make([]byte, 0, 28)
	for _, v := range []uint16{30, 60, 90, 120, 150, 180, 210} {
		raw = binary.LittleEndian.AppendUint16(raw, 0)
		raw = binary.LittleEndian.AppendUint16(raw, v)
	}
	blk := r.block(0, raw)
	if _, w := blk.Run(0); len(w) != 2 || w[0] != 60 || w[1] != 150 || blk.Held[0] {
		t.Fatalf("groups %v held %v", w, blk.Held)
	}
	blk = r.block(2, raw[:4]) // the carried 210, two held at 210: one group, held
	if _, w := blk.Run(0); len(w) != 1 || w[0] != 210 || !blk.Held[0] || blk.First != 2 {
		t.Fatalf("held group %v %v first %d", w, blk.Held, blk.First)
	}
}
