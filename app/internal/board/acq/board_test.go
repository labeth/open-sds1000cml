// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// fakeMem answers the calls the board's HAL facets make; the others are
// not expected.
type fakeMem struct {
	Memory
	armed    []sramcapture.Config
	polls    int
	freezeAt int
	meta     sramcapture.Metadata
	reads    [][]sramcapture.RecordRead
	recalls  []string
	hook     func(done, total int)
	caps     map[string]bool
	halted   int
	forced   int
}

func (m *fakeMem) Arm(_ context.Context, c sramcapture.Config) error {
	m.armed, m.polls = append(m.armed, c), 0
	return nil
}

func (m *fakeMem) Poll() (sramcapture.Metadata, error) {
	m.polls++
	if m.polls >= m.freezeAt {
		return sramcapture.Metadata{Frozen: true, Ready: true, Triggered: true}, nil
	}
	return sramcapture.Metadata{Running: true, Triggered: m.polls > 1}, nil
}

func (m *fakeMem) Status() (sramcapture.Metadata, error) { return m.meta, nil }
func (m *fakeMem) Force(context.Context) error           { m.forced++; return nil }
func (m *fakeMem) Halt(context.Context) error            { m.halted++; return nil }
func (m *fakeMem) SetSpanHook(fn func(int, int))         { m.hook = fn }

func (m *fakeMem) recall(name string, count uint32, dst io.Writer) (int64, error) {
	m.recalls = append(m.recalls, name)
	half := make([]byte, 2*count)
	n, err := dst.Write(half)
	if err != nil {
		return int64(n), err
	}
	if m.hook != nil {
		m.hook(1, 2)
	}
	n2, err := dst.Write(half)
	return int64(n + n2), err
}

func (m *fakeMem) Recall(_ context.Context, _, count uint32, dst io.Writer) (int64, error) {
	return m.recall("Recall", count, dst)
}

func (m *fakeMem) RecallForward(_ context.Context, _, count uint32, dst io.Writer) (int64, error) {
	return m.recall("RecallForward", count, dst)
}

func (m *fakeMem) RecallReadsOf(_ context.Context, _ sramcapture.Metadata, reads ...sramcapture.RecordRead) error {
	m.reads = append(m.reads, reads)
	for _, r := range reads {
		n := r.Count * 4
		if r.Bucket != 0 && m.meta.FractionBits == 8 {
			n = r.Count * 8
		}
		if _, err := r.Dst.Write(make([]byte, n)); err != nil {
			return err
		}
	}
	return nil
}

func (m *fakeMem) has(k string) (bool, error)        { return m.caps[k], nil }
func (m *fakeMem) SupportsUART() (bool, error)       { return m.has("uart") }
func (m *fakeMem) SupportsI2C() (bool, error)        { return m.has("i2c") }
func (m *fakeMem) SupportsSPI() (bool, error)        { return m.has("spi") }
func (m *fakeMem) SupportsSampleSPI() (bool, error)  { return false, nil }
func (m *fakeMem) SupportsSampleUART() (bool, error) { return false, nil }
func (m *fakeMem) SupportsSampleI2C() (bool, error)  { return false, nil }
func (m *fakeMem) SupportsSENT() (bool, error)       { return false, nil }
func (m *fakeMem) SupportsUSBLS() (bool, error)      { return false, nil }
func (m *fakeMem) SupportsMIL1553() (bool, error)    { return false, nil }
func (m *fakeMem) SupportsManchester() (bool, error) { return false, nil }
func (m *fakeMem) SupportsSequence() (bool, error)   { return false, nil }
func (m *fakeMem) SequenceOnly() (bool, error)       { return false, nil }
func (m *fakeMem) SupportsEnvelope() (bool, error)   { return true, nil }
func (m *fakeMem) SupportsQualifier() (bool, error)  { return m.has("qualifier") }
func (m *fakeMem) SupportsSymbol() (bool, error)     { return false, nil }
func (m *fakeMem) PacketProtocols() (uint16, error)  { return 0, nil }
func (m *fakeMem) RawOnly() (bool, error)            { return false, nil }
func (m *fakeMem) BoxDecimation() (bool, error)      { return false, nil }
func (m *fakeMem) PickDecimation() (bool, error)     { return false, nil }
func (m *fakeMem) PickOnAsking() (bool, error)       { return false, nil }

// cs3Log records the panel plane's writes.
type cs3Log struct {
	bus.Bus
	writes [][2]uint16
}

func (c *cs3Log) Write(_ uint8, sel, val uint16) error {
	c.writes = append(c.writes, [2]uint16{sel, val})
	return nil
}

// frontEnd is a fixed front end: 1 V/div, no offset, on both channels.
type frontEnd struct{ hal.FrontEnd }

func (frontEnd) Get(int) hal.Channel { return hal.Channel{On: true, VoltsDiv: 1, Probe: 1} }

func newTestBoard(caps ...string) (*Board, *fakeMem, *cs3Log) {
	m := &fakeMem{freezeAt: 3, caps: map[string]bool{}}
	for _, c := range caps {
		m.caps[c] = true
	}
	p := &cs3Log{}
	b := New(Config{Memory: m, Panel: p})
	b.SetFrontEnd(frontEnd{})
	b.ProbeImage(func(string, ...any) {})
	return b, m, p
}

// The plan and the trigger staged apart reach the fabric in one arm write.
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011
func TestStageAndCommit(t *testing.T) {
	b, m, _ := newTestBoard()
	p, err := b.Planner().Plan(hal.Request{Channels: []int{0, 1}, TimePerDivS: 1e-3, PretrigFrac: .25})
	if err != nil {
		t.Fatal(err)
	}
	if p.AcqDecim < 16 || p.Content.Has(sample.RawFullRate) {
		t.Fatalf("a 1 ms/div plan decimates: %+v", p)
	}
	if err := b.Acquirer().Configure(p); err != nil {
		t.Fatal(err)
	}
	spec := hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: hal.Source{Kind: hal.Analog, Index: 1}, Levels: hal.Levels{High: 1, Slope: hal.Falling}}}}
	if err := b.Trigger().Program(spec); err != nil {
		t.Fatal(err)
	}
	if len(m.armed) != 0 {
		t.Fatal("staging wrote the fabric")
	}
	if err := b.Acquirer().Arm(); err != nil {
		t.Fatal(err)
	}
	c := m.armed[0]
	if c.TriggerChannel != 1 || !c.Falling || !c.Normal || c.TriggerLevel != 153 || c.DecimationLog2 == 0 {
		t.Fatalf("committed %+v", c)
	}
	if c.PreWords+c.PostWords != uint32(p.Depth) || c.PreWords != uint32(p.Depth)/4 {
		t.Fatalf("words %d + %d for %d samples", c.PreWords, c.PostWords, p.Depth)
	}
	if b.Acquirer().State() != hal.Armed {
		t.Fatal(b.Acquirer().State())
	}
}

// A capture is waited for poll by poll; the ready record is halted and read
// for its geometry.
// TRLC-LINKS: REQ-SDS-008
func TestWaitPollsOnce(t *testing.T) {
	b, m, _ := newTestBoard()
	m.meta = sramcapture.Metadata{Frozen: true, Ready: true, Triggered: true, Length: 100, TriggerIndex: 50, SamplesPerWord: 2, RecordID: 7, EventEpoch: 3}
	_ = b.Acquirer().Arm()
	st, err := b.Acquirer().Wait(context.Background(), 0)
	if !errors.Is(err, hal.ErrTimeout) || st.Done {
		t.Fatalf("first poll: %+v %v", st, err)
	}
	if st, err = b.Acquirer().Wait(context.Background(), 0); !errors.Is(err, hal.ErrTimeout) || !st.Triggered {
		t.Fatalf("second poll reports the trigger: %+v %v", st, err)
	}
	st, err = b.Acquirer().Wait(context.Background(), 0)
	if err != nil || !st.Done || st.Record != (sample.RecordID{Epoch: 3, Seq: 7}) || b.Record().Length != 100 {
		t.Fatalf("ready: %+v %v", st, err)
	}
	if b.Acquirer().State() != hal.Ready {
		t.Fatal(b.Acquirer().State())
	}
}

// What the fabric cannot do is refused with its reason.
// TRLC-LINKS: REQ-SDS-011
func TestProgramRefuses(t *testing.T) {
	b, _, _ := newTestBoard()
	src := hal.Source{Kind: hal.Analog}
	for name, s := range map[string]hal.TriggerSpec{
		"two stages":  {Stages: make([]hal.TrigStage, 2)},
		"pulse":       {Stages: []hal.TrigStage{{Node: hal.NodePulse, Source: src}}},
		"either edge": {Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: src, Levels: hal.Levels{Slope: hal.Either}}}},
		"C3":          {Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: hal.Source{Kind: hal.Analog, Index: 2}}}},
		"AC":          {Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: src}}, Coupling: hal.TrigAC},
		"uart":        {Stages: []hal.TrigStage{{Node: hal.NodeProtocol, Source: src, Proto: prototrig.UART{Baud: 9600, Bits: 8}}}},
	} {
		if _, ok := hal.AsRefusal(b.Trigger().Program(s)); !ok {
			t.Errorf("%s: not refused", name)
		}
	}
}

// A pulse qualifier and a UART receiver plan as the engine's planners do.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013
func TestProgramQualifierAndUART(t *testing.T) {
	b, m, _ := newTestBoard("qualifier", "uart")
	src := hal.Source{Kind: hal.Analog}
	pulse := hal.TriggerSpec{Stages: []hal.TrigStage{{
		Node: hal.NodePulse, Source: src, Levels: hal.Levels{Slope: hal.Rising},
		Qual: &hal.Qualifier{Op: hal.Inside, MinS: 1e-6, MaxS: 5e-6},
	}}}
	if err := b.Trigger().Program(pulse); err != nil {
		t.Fatal(err)
	}
	_ = b.Acquirer().Arm()
	q := m.armed[0].Qualifier
	pre, lo, hi := QualTicks(1000, 5000)
	if !q.Enabled || q.Kind != sramcapture.QualPulse || q.Prescale != pre || q.MinTicks != lo || q.MaxTicks != hi {
		t.Fatalf("pulse %+v", q)
	}
	uart := hal.TriggerSpec{Stages: []hal.TrigStage{{
		Node: hal.NodeProtocol, Source: src,
		Proto: prototrig.UART{Baud: 115200, Bits: 8, IdleHigh: true, Condition: prototrig.UARTData, Value: 0x55},
	}}}
	if err := b.Trigger().Program(uart); err != nil {
		t.Fatal(err)
	}
	_ = b.Acquirer().Arm()
	u := m.armed[1].UART
	if !u.Enabled || u.Pattern != 0x55 || u.Length != 1 || m.armed[1].Qualifier.Enabled {
		t.Fatalf("uart %+v", m.armed[1])
	}
	if !b.Trigger().Caps().Has(hal.NodePulse) || b.Trigger().Caps().Has(hal.NodeSymbol) {
		t.Fatal(b.Trigger().Caps())
	}
}

// A raw main window streams in bursts with the lamps flushed between them;
// windows of an envelope read go in one isolated recall.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func TestReadWindows(t *testing.T) {
	b, m, p := newTestBoard()
	b.SetReadProgress(nil)
	b.SetRecord(sramcapture.Metadata{Length: 4096, TriggerIndex: 2048, SamplesPerWord: 2, Revision: 10})
	f := RecordFrame(2048)
	b.StageLamps(0x0102)
	b.StageOffsetDAC(0, 7)
	if err := b.Readout().Read(context.Background(), hal.ReadRequest{Windows: []hal.Window{Window(b.Record(), sample.Main, 1024, 1024, 0)}}, []*sample.Frame{f}, b.Platform().Service); err != nil {
		t.Fatal(err)
	}
	if len(m.recalls) != 1 || m.recalls[0] != "RecallForward" {
		t.Fatal(m.recalls)
	}
	if len(p.writes) != 4 || p.writes[2] != [2]uint16{cs3LEDLo, 2} {
		t.Fatalf("between bursts only the lamps: %v", p.writes)
	}
	b.Platform().Service()
	if len(p.writes) != 6 || p.writes[4] != [2]uint16{cs3OffC1Lo, 7} {
		t.Fatalf("at the commit point the offsets: %v", p.writes)
	}
	env, edge := RecordFrame(2048), RecordFrame(512)
	ws := []hal.Window{Window(b.Record(), sample.Main, 0, 4096, 4), Window(b.Record(), sample.Edge, 1792, 256, 0)}
	if err := b.Readout().Read(context.Background(), hal.ReadRequest{Windows: ws}, []*sample.Frame{env, edge}, nil); err != nil {
		t.Fatal(err)
	}
	r := m.reads[0]
	if len(r) != 2 || r[0].Offset != 0 || r[0].Bucket != 4 || r[0].Count != 1024 || r[1].Offset != 1792 || r[1].Bucket != 0 || r[1].Count != 256 {
		t.Fatalf("reads %+v", r)
	}
	bad := hal.Window{Role: sample.Main, StartSample: 1, Span: 2}
	if err := b.Readout().Read(context.Background(), hal.ReadRequest{Windows: []hal.Window{bad}}, []*sample.Frame{f}, nil); err == nil {
		t.Fatal("a window of half a word read")
	}
}

// Images are loaded by feature, each load a fresh one: the fabric halted
// first, the next Arm a commit arm.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-231
func TestImagesByFeature(t *testing.T) {
	b, m, sw := newDrumBoard(t)
	im, ok := b.Images()
	if !ok {
		t.Fatal("no images")
	}
	if err := im.Load(context.Background(), []string{"qualify"}); err != nil || sw.loaded != "qualify" {
		t.Fatal(err, sw.loaded)
	}
	if im.Current().Features[0] != "qualify" || im.Current().Name != "qualify" || m.halted == 0 || !b.stopped {
		t.Fatal(im.Current(), m.halted, b.stopped)
	}
	if _, ok := hal.AsRefusal(im.Load(context.Background(), []string{"no-such-feature"})); !ok {
		t.Fatal("an unknown feature loaded")
	}
	if err := im.Load(context.Background(), nil); err != nil || sw.loaded != "general" {
		t.Fatal(err, sw.loaded)
	}
}

type switcher struct{ loaded string }

func (s *switcher) LoadStack() error   { s.loaded = "stack"; return nil }
func (s *switcher) LoadGeneral() error { s.loaded = "general"; return nil }
func (s *switcher) LoadPacket() error  { s.loaded = "packet"; return nil }
func (s *switcher) LoadLine() error    { s.loaded = "line"; return nil }
func (s *switcher) LoadFrame() error   { s.loaded = "frame"; return nil }
func (s *switcher) LoadStream() error  { s.loaded = "stream"; return nil }
func (s *switcher) LoadQualify() error { s.loaded = "qualify"; return nil }
func (s *switcher) LoadFast() error    { s.loaded = "fast"; return nil }
func (s *switcher) HasStack() bool     { return false }
func (s *switcher) HasPacket() bool    { return true }
func (s *switcher) HasLine() bool      { return true }
func (s *switcher) HasFrame() bool     { return true }
func (s *switcher) HasStream() bool    { return true }
func (s *switcher) HasQualify() bool   { return true }
func (s *switcher) HasFast() bool      { return true }
