// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// drumMem is the stream image's drum: banks of (C1, C2) Q8.8 words handed
// out in order, and its level-crossing mark counters.
type drumMem struct {
	*fakeMem
	started  []sramcapture.Config
	modes    []sramcapture.StreamMarkMode
	setMarks []sramcapture.StreamMarkMode
	banks    [][]byte
	next     uint64
	fail     error
	marks    sramcapture.StreamMarkRead
	stopped  int
	snapshot func(context.Context) ([10]uint8, error)
}

func (m *drumMem) StartStreamMarked(_ context.Context, cfg sramcapture.Config, mode sramcapture.StreamMarkMode) error {
	m.started, m.modes, m.next = append(m.started, cfg), append(m.modes, mode), 0
	return nil
}

func (m *drumMem) DrainStream(_ context.Context, expected uint64, dst io.Writer) (sramcapture.StreamBlock, error) {
	if m.fail != nil {
		return sramcapture.StreamBlock{}, m.fail
	}
	if len(m.banks) == 0 {
		return sramcapture.StreamBlock{}, sramcapture.ErrNoStreamBlock
	}
	bank := m.banks[0]
	m.banks = m.banks[1:]
	if _, err := dst.Write(bank); err != nil {
		return sramcapture.StreamBlock{}, err
	}
	blk := sramcapture.StreamBlock{FirstWord: m.next, Words: uint32(len(bank) / 4)}
	m.next += uint64(blk.Words)
	_ = expected
	return blk, nil
}

func (m *drumMem) StopStream(context.Context) error { m.stopped++; return nil }
func (m *drumMem) StreamMarksSupported() bool       { return true }
func (m *drumMem) StreamRingSupported() bool        { return false }
func (m *drumMem) SupportsPanel() (bool, error)     { return false, nil }
func (m *drumMem) RestartEncode() error             { return nil }
func (m *drumMem) Snapshot(ctx context.Context) ([10]uint8, error) {
	return m.snapshot(ctx)
}

func (m *drumMem) SetStreamMarks(ch int, _, _ uint8, mode sramcapture.StreamMarkMode) error {
	m.setMarks = append(m.setMarks, mode)
	_ = ch
	return nil
}

func (m *drumMem) StreamMarks() (sramcapture.StreamMarkRead, error) { return m.marks, nil }

// bankOf is n words of (C1, C2) Q8.8 averages: C1 counts up from c1, C2
// down from c2.
func bankOf(n int, c1, c2 uint16) []byte {
	out := make([]byte, 0, 4*n)
	for i := range n {
		a, b := c1+uint16(i), c2-uint16(i)
		out = append(out, byte(a), byte(a>>8), byte(b), byte(b>>8))
	}
	return out
}

// TRLC-LINKS: REQ-SDS-265
func newDrumBoard(t *testing.T) (*Board, *drumMem, *switcher) {
	t.Helper()
	b, fm, _ := newTestBoard()
	m := &drumMem{fakeMem: fm}
	// the converters move (no stuck core) so the image's bring-up checks pass at once
	var k uint8
	m.snapshot = func(context.Context) ([10]uint8, error) {
		k++
		var s [10]uint8
		for i := range s {
			s[i] = k + uint8(i)
		}
		return s, nil
	}
	b.mem = m
	sw := &switcher{}
	b.SetImages(sw)
	return b, m, sw
}

// The audio stream is the stream image's averages of 2^13 samples, whatever
// the timebase: the plan says its rate, Start loads the stream image and
// starts the drum with the marks asked, and each block is the channels'
// Q8.8 runs, channel-major, numbered by stream word, at the delivered rate,
// with the level crossings the image counted since the last block.
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-272
func TestAudioStream(t *testing.T) {
	b, m, sw := newDrumBoard(t)
	r := hal.Request{Channels: []int{0}, TimePerDivS: 1e-3, Mode: hal.Stream, Need: []hal.Feature{{Name: FeatureSound}}}
	plan, err := b.Planner().Plan(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := float64(uint64(1)<<soundLog2) / 500e6; plan.SampleS != want || plan.AcqDecim != 1<<soundLog2 {
		t.Fatalf("plan %g s a sample, decimation %d", plan.SampleS, plan.AcqDecim)
	}
	if soundLog2 != core.SoundLog2 {
		t.Fatalf("the audio stream's decimation 2^%d, the core's sound ring 2^%d", soundLog2, core.SoundLog2)
	}
	if _, err := b.Planner().Plan(hal.Request{Channels: []int{0}, TimePerDivS: 1e-3, Mode: hal.Stream}); err == nil {
		t.Fatal("a stream plan for no consumer the board serves was taken")
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	st, ok := b.Streamer()
	if !ok || !slices.Contains(st.Kinds(), hal.PayloadCodes) {
		t.Fatal("no stream of codes")
	}
	mk, ok := st.(hal.StreamMarker)
	if !ok {
		t.Fatal("the stream marks nothing")
	}
	if err := mk.SetMarks(hal.MarkSpec{Channel: 1, Level: 140, Hyst: 3, Rise: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.Start(hal.StreamSpec{Payload: hal.PayloadCodes, Channels: []int{0, 1}, SampleS: plan.SampleS}); err != nil {
		t.Fatal(err)
	}
	if sw.loaded != "stream" || b.imageLoaded != ImageStream {
		t.Fatalf("image %q", sw.loaded)
	}
	c := m.started[0]
	if c.DecimationLog2 != soundLog2 || c.TriggerChannel != 1 || c.TriggerLevel != 140 || c.TriggerHysteresis != 3 || !c.Normal || m.modes[0] != (sramcapture.StreamMarkMode{Rise: true}) {
		t.Fatalf("drum started with %+v marks %+v", c, m.modes[0])
	}
	m.banks = [][]byte{bankOf(3, 0x8000, 0x9000), bankOf(2, 0x7000, 0x6000)}
	m.marks = sramcapture.StreamMarkRead{RiseCount: 2, RiseWord: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	blk, err := st.Lease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if blk.First != 0 || len(blk.Formats) != 2 || blk.SampleS != plan.SampleS {
		t.Fatalf("block %+v", blk)
	}
	c1, w1 := blk.Run(0)
	_, w2 := blk.Run(1)
	if c1 != nil || len(w1) != 3 || w1[0] != 0x8000 || w1[2] != 0x8002 || w2[0] != 0x9000 || w2[2] != 0x8ffe {
		t.Fatalf("runs %04x %04x", w1, w2)
	}
	if blk.Formats[0].Q88(uint32(w1[1])) != w1[1] {
		t.Fatal("a Q8.8 word does not map onto itself")
	}
	if len(blk.Marks) != 1 || blk.Marks[0] != (hal.Mark{At: 1, Kind: hal.MarkRise, Channel: 1, Count: 2}) {
		t.Fatalf("marks %+v", blk.Marks)
	}
	// a changed mark latches at the next block; counts already seen mark nothing
	if err := mk.SetMarks(hal.MarkSpec{Channel: 1, Level: 120, Fall: true}); err != nil {
		t.Fatal(err)
	}
	blk, err = st.Lease(ctx)
	if err != nil || blk.First != 3 || len(blk.Marks) != 0 || len(m.setMarks) != 1 || m.setMarks[0] != (sramcapture.StreamMarkMode{Fall: true}) {
		t.Fatalf("second block %+v err %v, marks set %+v", blk, err, m.setMarks)
	}
	// no words: Lease waits until its context ends
	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	if _, err := st.Lease(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an empty drum: %v", err)
	}
	// a loss ends the stream; the next start loads the image again
	m.fail = errors.New("bank fault")
	if _, err := st.Lease(ctx); err == nil {
		t.Fatal("a failed drain gave a block")
	}
	if err := st.Stop(); err != nil || m.stopped != 1 {
		t.Fatal(err, m.stopped)
	}
	m.fail, sw.loaded = nil, ""
	if err := st.Start(hal.StreamSpec{Payload: hal.PayloadCodes, Channels: []int{1}}); err != nil || sw.loaded != "stream" {
		t.Fatalf("restart after a loss: %v, image %q", err, sw.loaded)
	}
	m.banks = [][]byte{bankOf(2, 0x8100, 0x9100)}
	blk, err = st.Lease(ctx)
	if _, w := blk.Run(0); err != nil || len(blk.Formats) != 1 || len(w) != 2 || w[0] != 0x9100 {
		t.Fatalf("C2 alone: %+v %v", blk, err)
	}
	if _, ok := hal.AsRefusal(st.Start(hal.StreamSpec{Payload: hal.PayloadCodes, Channels: []int{0}})); !ok {
		t.Fatal("a second start while streaming was taken")
	}
	// the trigger's marks: the staged edge trigger's channel, comparator code and slope
	tr := b.StagedTrigger()
	tr.TriggerChannel, tr.TriggerLevel, tr.Falling = 1, 99, true
	if err := mk.SetMarks(hal.MarkSpec{FromTrigger: true}); err != nil {
		t.Fatal(err)
	}
	m.banks = [][]byte{bankOf(1, 0, 0)}
	if _, err := st.Lease(ctx); err != nil {
		t.Fatal(err)
	}
	if got, on := mk.Marking(); !on || got != (hal.MarkSpec{Channel: 1, Level: 99, Fall: true}) {
		t.Fatalf("the trigger's marks: %+v", got)
	}
}

// A stream's payload, channels and rate are the drum's: decoded, digital and
// full-rate streams are refused.
// TRLC-LINKS: REQ-SDS-035
func TestStreamRefusals(t *testing.T) {
	b, _, _ := newDrumBoard(t)
	st, _ := b.Streamer()
	for _, spec := range []hal.StreamSpec{
		{Payload: hal.PayloadEvents, Channels: []int{0}},
		{Payload: hal.PayloadCodes, Channels: []int{2}},
		{Payload: hal.PayloadCodes},
	} {
		if _, ok := hal.AsRefusal(st.Start(spec)); !ok {
			t.Errorf("%+v was taken", spec)
		}
	}
	// the staged plan is full rate: the drum streams decimated records only
	if _, ok := hal.AsRefusal(st.Start(hal.StreamSpec{Payload: hal.PayloadCodes, Channels: []int{0, 1}})); !ok {
		t.Error("a full-rate stream was taken")
	}
	if mk, ok := st.(hal.StreamMarker); !ok || mk.SetMarks(hal.MarkSpec{Channel: 2}) == nil {
		t.Error("marks on C3 were taken")
	}
}
