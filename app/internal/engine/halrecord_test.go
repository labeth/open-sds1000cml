// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labeth/open-sds1000cml/app/internal/board/acq"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/superres"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The acquisition owner's calls into the record memory and the panel bus,
// replayed for scripted scenarios and compared with recorded traces: a
// change of structure (the engine's pure code moving to the core, the
// record memory moving behind the board's HAL facets) must leave every call
// the hardware sees, its order and arguments, and every published frame
// unchanged. The scenarios run on a fake clock, so the poll counts are part
// of the trace. Regenerate with -update-halrecord only for a change meant to
// alter what the hardware sees.

var updateHALRecord = flag.Bool("update-halrecord", false, "rewrite the acquisition call traces")

// callLog is the ordered trace of one scenario.
type callLog struct {
	off   bool // a benchmark's: nothing is kept
	mu    sync.Mutex
	lines []string
	last  string
	rep   int
}

func (l *callLog) add(format string, args ...any) {
	if l.off {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := fmt.Sprintf(format, args...)
	if s == l.last {
		l.rep++
		return
	}
	l.flushLocked()
	l.last, l.rep = s, 1
}

func (l *callLog) flushLocked() {
	switch {
	case l.rep == 1:
		l.lines = append(l.lines, l.last)
	case l.rep > 1:
		l.lines = append(l.lines, fmt.Sprintf("%s x%d", l.last, l.rep))
	}
	l.last, l.rep = "", 0
}

func (l *callLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
	return strings.Join(l.lines, "\n") + "\n"
}

// recordedCS3 is the panel and offset/level DAC bus, logging every write.
type recordedCS3 struct {
	*fakeBus
	log *callLog
}

func (b *recordedCS3) Write(plane uint8, sel, val uint16) error {
	b.log.add("bus write plane %d sel %#x = %#x", plane, sel, val)
	return b.fakeBus.Write(plane, sel, val)
}

func (b *recordedCS3) RawWrite(sel, val uint16) error {
	b.log.add("bus raw write sel %#x = %#x", sel, val)
	return b.fakeBus.RawWrite(sel, val)
}

// memRecorder stands in for the SRAM fabric: it models arming, the trigger,
// freezing and recalls well enough for the owner loop, synthesizes the
// signal from the record's trigger point, and logs every call.
type memRecorder struct {
	log  *callLog
	caps map[string]bool
	e    *Engine

	arm      int // arms so far
	armed    bool
	cfg      sramcapture.Config
	polls    int
	forced   bool
	record   uint32
	spanHook func(done, total int)
	onSpan   func(e *Engine)   // once, before the first span hook of a recall
	cache    map[string][]byte // payloads already made, when set (a benchmark's)

	// scenario
	hooks    map[int]func(e *Engine) // at the k-th arm, before it returns
	stopAt   int                     // the arm whose hook stops the loop
	triggers func(arm int) bool      // does capture k see its trigger
	fill     int                     // polls until a capture's record is full
}

var _ acq.Memory = (*memRecorder)(nil)

// cfgText is the configuration's fields that differ from the zero value.
func cfgText(c sramcapture.Config) string {
	b, err := json.Marshal(c)
	if err != nil {
		return err.Error()
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err.Error()
	}
	var out strings.Builder
	nonZero(&out, "", v)
	return strings.TrimSpace(out.String())
}

func nonZero(out *strings.Builder, path string, v any) {
	switch v := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			nonZero(out, path+"."+k, v[k])
		}
	case []any:
		for i, x := range v {
			nonZero(out, fmt.Sprintf("%s[%d]", path, i), x)
		}
	case bool:
		if v {
			fmt.Fprintf(out, " %s", path[1:])
		}
	case float64:
		if v != 0 {
			fmt.Fprintf(out, " %s=%v", path[1:], v)
		}
	case string:
		if v != "" {
			fmt.Fprintf(out, " %s=%q", path[1:], v)
		}
	}
}

func (m *memRecorder) Arm(_ context.Context, cfg sramcapture.Config) error {
	m.arm++
	m.armed, m.cfg, m.polls, m.forced = true, cfg, 0, false
	m.log.add("Arm #%d at %v: %s", m.arm, m.e.clk.Now().Sub(time.Unix(1000, 0)), cfgText(cfg))
	if h := m.hooks[m.arm]; h != nil {
		h(m.e)
	}
	if m.stopAt > 0 && m.arm >= m.stopAt {
		m.e.stopReq.Store(true)
	}
	return nil
}

func (m *memRecorder) triggered() bool {
	return m.cfg.Normal && m.triggers != nil && m.triggers(m.arm)
}

func (m *memRecorder) frozen() bool {
	return m.armed && m.polls >= m.fill && (m.triggered() || m.forced || !m.cfg.Normal)
}

func (m *memRecorder) meta() sramcapture.Metadata {
	md := sramcapture.Metadata{Revision: 10, MapID: sramcapture.QualifiedMapID, Locked: true, Interleaved: true}
	if !m.armed {
		return md
	}
	md.Running = !m.frozen()
	md.Triggered = m.triggered() && m.polls >= 1
	if !m.frozen() {
		return md
	}
	words := m.cfg.PreWords + m.cfg.PostWords
	md.Ready, md.Frozen = true, true
	md.Length, md.TriggerIndex = words, m.cfg.PreWords
	md.HasRecordIdentity, md.RecordID, md.EventEpoch = true, m.record, 1
	md.SampleRateHz, md.Decimation, md.SampleBits, md.SamplesPerWord = 500e6, 1, 8, 2
	if log := m.cfg.DecimationLog2; log != 0 {
		md.Decimation = 1 << log
		md.SampleRateHz /= float64(md.Decimation)
		md.SampleBits, md.FractionBits, md.SamplesPerWord = 16, 8, 1
	}
	return md
}

func (m *memRecorder) Poll() (sramcapture.Metadata, error) {
	m.polls++
	m.log.add("Poll")
	return m.meta(), nil
}

func (m *memRecorder) Status() (sramcapture.Metadata, error) {
	md := m.meta()
	if md.Frozen {
		m.record++
		md.RecordID = m.record
	}
	m.log.add("Status frozen=%v words=%d trig=%d", md.Frozen, md.Length, md.TriggerIndex)
	return md, nil
}

func (m *memRecorder) Force(context.Context) error {
	m.forced = true
	m.log.add("Force")
	return nil
}

func (m *memRecorder) Halt(context.Context) error {
	m.armed = false
	m.log.add("Halt at %v", m.e.clk.Now().Sub(time.Unix(1000, 0)))
	return nil
}

func (m *memRecorder) Snapshot(context.Context) ([10]uint8, error) {
	m.log.add("Snapshot")
	return [10]uint8{100, 101, 99, 100, 102, 100, 98, 101, 100, 99}, nil
}

func (m *memRecorder) RestartEncode() error { m.log.add("RestartEncode"); return nil }
func (m *memRecorder) Beats() uint64        { return 0 }
func (m *memRecorder) Locked(fn func())     { fn() }
func (m *memRecorder) SetSpanHook(fn func(done, total int)) {
	m.log.add("SetSpanHook %v", fn != nil)
	m.spanHook = fn
}

// recSquare is C1: a square of 4000 samples (8 us at 2 ns) rising at the
// trigger; recTriangle is C2.
func recSquare(s int64) float64 {
	if ((s%4000)+4000)%4000 < 2000 {
		return 196
	}
	return 60
}

func recTriangle(s int64) float64 {
	p := ((s % 6000) + 6000) % 6000
	if p < 3000 {
		return 80 + 96*float64(p)/3000
	}
	return 176 - 96*float64(p-3000)/3000
}

// rawAt is the signal at word w's j-th sample of the frozen record.
func (m *memRecorder) rawAt(md sramcapture.Metadata, w uint32, j int) (a, b float64) {
	var s int64
	if md.SamplesPerWord == 2 {
		s = 2*(int64(w)-int64(md.TriggerIndex)) + int64(j)
	} else {
		s = (int64(w) - int64(md.TriggerIndex)) * int64(md.Decimation)
	}
	return recSquare(s), recTriangle(s)
}

// appendWords appends words [off, off+n) of the record as the fabric lays
// them out.
func (m *memRecorder) appendWords(dst []byte, md sramcapture.Metadata, off, n uint32) []byte {
	for w := off; w < off+n; w++ {
		if md.SamplesPerWord == 2 {
			a0, b0 := m.rawAt(md, w, 0)
			a1, b1 := m.rawAt(md, w, 1)
			dst = append(dst, uint8(a0), uint8(b0), uint8(a1), uint8(b1))
			continue
		}
		a, b := m.rawAt(md, w, 0)
		dst = binary.LittleEndian.AppendUint16(dst, uint16(a*256))
		dst = binary.LittleEndian.AppendUint16(dst, uint16(b*256))
	}
	return dst
}

// appendBuckets appends n envelope buckets of bucket words from off.
func (m *memRecorder) appendBuckets(dst []byte, md sramcapture.Metadata, off, bucket, n uint32) []byte {
	for k := range n {
		lo := [2]float64{255, 255}
		hi := [2]float64{0, 0}
		for w := off + k*bucket; w < off+(k+1)*bucket; w++ {
			for j := range int(md.SamplesPerWord) {
				a, b := m.rawAt(md, w, j)
				lo[0], hi[0] = math.Min(lo[0], a), math.Max(hi[0], a)
				lo[1], hi[1] = math.Min(lo[1], b), math.Max(hi[1], b)
			}
		}
		if md.FractionBits == 8 {
			for _, v := range []float64{lo[0], lo[1], hi[0], hi[1]} {
				dst = binary.LittleEndian.AppendUint16(dst, uint16(v*256))
			}
			continue
		}
		dst = append(dst, uint8(lo[0]), uint8(lo[1]), uint8(hi[0]), uint8(hi[1]))
	}
	return dst
}

func (m *memRecorder) recall(name string, offset, count uint32, dst io.Writer) (int64, error) {
	md := m.meta()
	m.log.add("%s offset %d words %d", name, offset, count)
	const span = 65536
	var total int64
	var buf []byte
	for done := uint32(0); done < count; done += span {
		n := min(span, count-done)
		key := ""
		if m.cache != nil {
			key = fmt.Sprintf("raw %d %d %d %d %d", md.TriggerIndex, md.SamplesPerWord, md.Decimation, offset+done, n)
		}
		if c, ok := m.cache[key]; ok {
			buf = c
		} else {
			buf = m.appendWords(nil, md, offset+done, n)
			if m.cache != nil {
				m.cache[key] = buf
			}
		}
		w, err := dst.Write(buf)
		total += int64(w)
		if err != nil {
			return total, err
		}
		if m.spanHook != nil && done+n < count {
			if a := m.onSpan; a != nil {
				m.onSpan = nil
				a(m.e)
			}
			m.spanHook(int(done+n), int(count))
		}
	}
	return total, nil
}

func (m *memRecorder) Recall(_ context.Context, offset, count uint32, dst io.Writer) (int64, error) {
	return m.recall("Recall", offset, count, dst)
}

func (m *memRecorder) RecallForward(_ context.Context, offset, count uint32, dst io.Writer) (int64, error) {
	return m.recall("RecallForward", offset, count, dst)
}

func (m *memRecorder) RecallReadsOf(_ context.Context, md sramcapture.Metadata, reads ...sramcapture.RecordRead) error {
	var b strings.Builder
	for _, r := range reads {
		fmt.Fprintf(&b, " {%d %d %d}", r.Offset, r.Bucket, r.Count)
	}
	m.log.add("RecallReadsOf record %d%s", md.RecordID, b.String())
	for _, r := range reads {
		var buf []byte
		key := ""
		if m.cache != nil {
			key = fmt.Sprintf("%d %d %d %d %d %d", md.TriggerIndex, md.SamplesPerWord, md.Decimation, r.Offset, r.Bucket, r.Count)
			buf = m.cache[key]
		}
		switch {
		case buf != nil:
		case r.Bucket != 0:
			buf = m.appendBuckets(nil, md, r.Offset, r.Bucket, r.Count)
		default:
			buf = m.appendWords(nil, md, r.Offset, r.Count)
		}
		if m.cache != nil {
			m.cache[key] = buf
		}
		if _, err := r.Dst.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

func (m *memRecorder) EnvelopeDiagnostics() (int, string) { return 0, "" }

func (m *memRecorder) has(name string) (bool, error) {
	m.log.add("Supports %s", name)
	return m.caps[name], nil
}

func (m *memRecorder) SupportsUART() (bool, error)           { return m.has("uart") }
func (m *memRecorder) SupportsSampleUART() (bool, error)     { return m.has("sample-uart") }
func (m *memRecorder) SupportsI2C() (bool, error)            { return m.has("i2c") }
func (m *memRecorder) SupportsSampleI2C() (bool, error)      { return m.has("sample-i2c") }
func (m *memRecorder) SupportsSPI() (bool, error)            { return m.has("spi") }
func (m *memRecorder) SupportsSampleSPI() (bool, error)      { return m.has("sample-spi") }
func (m *memRecorder) SupportsSENT() (bool, error)           { return m.has("sent") }
func (m *memRecorder) SupportsUSBLS() (bool, error)          { return m.has("usbls") }
func (m *memRecorder) SupportsMIL1553() (bool, error)        { return m.has("mil1553") }
func (m *memRecorder) SupportsManchester() (bool, error)     { return m.has("manchester") }
func (m *memRecorder) SupportsSequence() (bool, error)       { return m.has("sequence") }
func (m *memRecorder) SequenceOnly() (bool, error)           { return m.has("sequence-only") }
func (m *memRecorder) SupportsEnvelope() (bool, error)       { return m.has("envelope") }
func (m *memRecorder) SupportsQualifier() (bool, error)      { return m.has("qualifier") }
func (m *memRecorder) SupportsSymbol() (bool, error)         { return m.has("symbol") }
func (m *memRecorder) SupportsPanel() (bool, error)          { return m.has("panel") }
func (m *memRecorder) SupportsDecodedCapture() (bool, error) { return m.has("decoded") }
func (m *memRecorder) RawOnly() (bool, error)                { return m.has("raw-only") }
func (m *memRecorder) BoxDecimation() (bool, error)          { return m.has("box") }
func (m *memRecorder) PickDecimation() (bool, error)         { return m.has("pick") }
func (m *memRecorder) PickOnAsking() (bool, error)           { return m.has("pick-on-asking") }

func (m *memRecorder) PacketProtocols() (uint16, error) {
	m.log.add("PacketProtocols")
	return 0, nil
}

func (m *memRecorder) SymbolMatches() (uint16, error) {
	m.log.add("SymbolMatches")
	return 0, nil
}

var errNotModelled = errors.New("not modelled by the recorder")

func (m *memRecorder) StartStream(_ context.Context, cfg sramcapture.Config, continuous, peak bool, pace sramcapture.StreamPace) error {
	m.log.add("StartStream %s %v %v %+v", cfgText(cfg), continuous, peak, pace)
	return errNotModelled
}

func (m *memRecorder) StartStreamMarked(_ context.Context, cfg sramcapture.Config, mode sramcapture.StreamMarkMode) error {
	m.log.add("StartStreamMarked %s %+v", cfgText(cfg), mode)
	return errNotModelled
}

func (m *memRecorder) StartStreamRing(_ context.Context, cfg sramcapture.Config, log uint8, pace sramcapture.StreamPace) error {
	m.log.add("StartStreamRing %s %d %+v", cfgText(cfg), log, pace)
	return errNotModelled
}

func (m *memRecorder) DrainStream(context.Context, uint64, io.Writer) (sramcapture.StreamBlock, error) {
	m.log.add("DrainStream")
	return sramcapture.StreamBlock{}, errNotModelled
}

func (m *memRecorder) StreamStatus() (sramcapture.StreamStatus, error) {
	m.log.add("StreamStatus")
	return sramcapture.StreamStatus{}, errNotModelled
}

func (m *memRecorder) StopStream(context.Context) error { m.log.add("StopStream"); return nil }
func (m *memRecorder) StreamMarksSupported() bool       { return false }
func (m *memRecorder) SetStreamMarks(int, uint8, uint8, sramcapture.StreamMarkMode) error {
	m.log.add("SetStreamMarks")
	return errNotModelled
}

func (m *memRecorder) StreamMarks() (sramcapture.StreamMarkRead, error) {
	return sramcapture.StreamMarkRead{}, errNotModelled
}
func (m *memRecorder) StreamRingSupported() bool { return false }
func (m *memRecorder) FreezeRing(context.Context) (sramcapture.Metadata, uint32, error) {
	m.log.add("FreezeRing")
	return sramcapture.Metadata{}, 0, errNotModelled
}

func (m *memRecorder) StartDecodedCapture(context.Context, sramcapture.Config) (*sramcapture.DecodedCapture, error) {
	m.log.add("StartDecodedCapture")
	return nil, errNotModelled
}

func (m *memRecorder) EnableDecodedEvents(on bool) (uint32, error) {
	m.log.add("EnableDecodedEvents %v", on)
	return 0, nil
}

func (m *memRecorder) ReadDecodedEventsInto(uint32, []sramcapture.DecodedEvent) ([]sramcapture.DecodedEvent, error) {
	return nil, errNotModelled
}

func (m *memRecorder) SetDecodedLineMode(int) (bool, error) { return false, nil }
func (m *memRecorder) ReadDecodedLines(uint32, int, bool, []byte) (int, bool, error) {
	return 0, false, errNotModelled
}

func (m *memRecorder) StackBegin(sramcapture.StackConfig) (int, error) {
	return 0, errNotModelled
}
func (m *memRecorder) StackResetTile(context.Context) error { return errNotModelled }
func (m *memRecorder) StackScan(context.Context, int, int, uint32) (uint32, uint32, uint32, error) {
	return 0, 0, 0, errNotModelled
}

func (m *memRecorder) StackDownload(context.Context, []superres.FPGAStackMoments) error {
	return errNotModelled
}

func (m *memRecorder) ConfigurePanel(p sramcapture.PanelConfig) error {
	m.log.add("ConfigurePanel %+v", p)
	return nil
}

func (m *memRecorder) ReadPanel() (sramcapture.PanelFrame, error) {
	m.log.add("ReadPanel")
	return sramcapture.PanelFrame{}, nil
}

// frameText summarizes a published frame for the trace.
func frameText(f *sample.Frame) string {
	h := fnv.New64a()
	for i := range f.Traces {
		t := &f.Traces[i]
		n := min(f.Len(), len(t.Codes8))
		_, _ = h.Write(t.Codes8[:n])
		for _, q := range t.Precision[:min(f.Len(), len(t.Precision))] {
			_, _ = h.Write([]byte{byte(q), byte(q >> 8)})
		}
		_, _ = h.Write(t.Min8)
		_, _ = h.Write(t.Max8)
	}
	var d strings.Builder
	for _, x := range f.Details {
		fmt.Fprintf(&d, " detail %v [%g %g] read %v len %d", x.Role, x.From, x.To, x.Read, x.Frame.Len())
	}
	return fmt.Sprintf("frame seq %d len %d content %v flags %v sample %.6g win %d trig %v kind %v node %q at %.4f decim %d depth %d filter %q hash %016x%s",
		f.Seq, f.Len(), f.Content, f.Flags, f.SampleS, f.WinPoints, f.Status.Triggered, f.TrigKind, f.TrigNode, f.TriggerAt,
		f.AcqDecim, f.Depth, f.Cond.Filter, h.Sum64(), d.String())
}

// halScenario is one scripted run of the owner loop.
type halScenario struct {
	name     string
	caps     []string
	hooks    map[int]func(e *Engine)
	stopAt   int
	triggers func(arm int) bool
	fill     int
	// at runs actions once the fake clock passes their time since the
	// start (inside the owner's sleep: a stopped loop arms nothing);
	// stopAfter, when set, stops the loop then instead of at stopAt.
	at        map[time.Duration]func(e *Engine)
	stopAfter time.Duration
	onSpan    func(e *Engine)
}

func always(int) bool { return true }

func runHALScenario(t *testing.T, sc halScenario) (string, string) {
	t.Helper()
	log := &callLog{}
	hl := &halLog{log: &callLog{}}
	start := time.Unix(1000, 0)
	now := start
	var e *Engine
	fired := map[time.Duration]bool{}
	clock := Clock{
		Now: func() time.Time { return now },
		Sleep: func(d time.Duration) {
			now = now.Add(d)
			for _, at := range slices.Sorted(maps.Keys(sc.at)) {
				if !fired[at] && now.Sub(start) >= at {
					fired[at] = true
					log.add("at %v", at)
					sc.at[at](e)
				}
			}
			if sc.stopAfter > 0 && now.Sub(start) >= sc.stopAfter {
				e.stopReq.Store(true)
			}
		},
	}
	cs3 := &recordedCS3{fakeBus: newFakeBus(), log: log}
	capture, err := sramcapture.New(&decodedTestBus{})
	if err != nil {
		t.Fatal(err)
	}
	e = New(Config{Bus: cs3, SRAM: capture, Clock: clock, Logf: func(string, ...any) {}})
	rec := &memRecorder{log: log, caps: map[string]bool{}, e: e, hooks: sc.hooks, stopAt: sc.stopAt, triggers: sc.triggers, fill: sc.fill}
	for _, c := range sc.caps {
		rec.caps[c] = true
	}
	rec.onSpan = sc.onSpan
	e.useMemory(rec)
	e.acqr = recAcquirer{e.acqr, hl}
	e.plat = recPlatform{e.plat, hl}
	e.readYield = e.plat.Service
	e.rdx = recReader{e.rdx, hl}
	// Each arm also reports the newest frame published before it.
	hooks := map[int]func(*Engine){}
	for k := 1; k <= max(sc.stopAt, 1000); k++ {
		user := sc.hooks[k]
		hooks[k] = func(e *Engine) {
			if f, ok := e.Consume(); ok {
				log.add("%s", frameText(f))
			}
			if user != nil {
				user(e)
			}
		}
	}
	rec.hooks = hooks
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Run()
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("%s: the owner loop did not stop", sc.name)
	}
	if f, ok := e.Consume(); ok {
		log.add("%s", frameText(f))
	}
	return log.text(), hl.log.text()
}

func halScenarios() []halScenario {
	general := []string{"envelope", "panel", "box"}
	qualify := []string{"envelope", "panel", "box", "qualifier"}
	uart := []string{"envelope", "panel", "box", "uart", "sample-uart", "sequence"}
	return []halScenario{
		{name: "auto-live", caps: general, stopAt: 8, triggers: always, fill: 3},
		{name: "auto-untriggered", caps: general, stopAt: 4, fill: 2},
		{
			name: "normal-waits", caps: general, stopAfter: 300 * time.Millisecond, fill: 2, triggers: func(k int) bool { return k%2 == 0 },
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetNorm(true) }},
		},
		{
			name: "fast-timebase", caps: general, stopAt: 5, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetTdiv(1e-6) }},
		},
		{
			name: "slow-decimated", caps: general, stopAt: 5, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetTdiv(5e-3) }},
		},
		{
			name: "stop-replay", caps: general, triggers: always, fill: 2, stopAfter: 400 * time.Millisecond,
			hooks: map[int]func(*Engine){2: func(e *Engine) { e.SetRunning(false) }},
			at: map[time.Duration]func(*Engine){
				200 * time.Millisecond: func(e *Engine) { e.SetChannelVdiv(0, 0.5, 0, 0) },
				300 * time.Millisecond: func(e *Engine) { e.SetRunning(true) },
			},
		},
		{
			name: "single", caps: general, triggers: always, fill: 2, stopAfter: 400 * time.Millisecond,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetSingle() }},
			at:    map[time.Duration]func(*Engine){300 * time.Millisecond: func(e *Engine) { e.SetRunning(true) }},
		},
		{
			name: "average", caps: general, stopAt: 6, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetAvgCount(4); e.SetAcqMode(AcqAverage) }},
		},
		{
			name: "eres", caps: general, stopAt: 4, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetEresLen(4); e.SetAcqMode(AcqEres) }},
		},
		{
			name: "precision", caps: general, stopAt: 4, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetTdiv(1e-3); e.SetAcqMode(AcqPrecision) }},
		},
		{
			name: "force-key", caps: general, stopAfter: 300 * time.Millisecond, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetNorm(true); e.ForceTrigger() }},
		},
		{
			name: "lamps-and-dacs", caps: general, triggers: always, fill: 2, stopAfter: 300 * time.Millisecond,
			hooks: map[int]func(*Engine){
				1: func(e *Engine) { e.SetLEDs(0x1234); e.SetOffsetDAC(0, 3000); e.SetTrigLevelCode(2100) },
				2: func(e *Engine) { e.SetRunning(false); e.SetLEDs(0x0042) },
			},
			at:     map[time.Duration]func(*Engine){200 * time.Millisecond: func(e *Engine) { e.SetRunning(true) }},
			onSpan: func(e *Engine) { e.SetLEDs(0x0777) },
		},
		{
			name: "view-window", caps: general, stopAt: 5, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) {
				e.SetTdiv(1e-3)
				e.LeaseViewWindow(ViewScope, -0.05, 0.05, 400, time.Hour)
			}},
		},
		{
			name: "raw-lease", caps: general, stopAt: 4, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.LeaseRawFor("test", time.Hour) }},
		},
		{
			name: "pulse-qualifier", caps: qualify, stopAt: 6, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) {
				e.SetPulseParams(0.5, 1000, 5000, CondInside)
				e.SetTrigType(int(TrigPulse))
			}},
		},
		{
			name: "zones", caps: qualify, stopAt: 6, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) {
				e.SetZones([]Zone{{Ch: 0, DtLoS: -1e-6, DtHiS: 1e-6, CodeLo: 100, CodeHi: 150}})
				e.SetZoneMode(ZoneTrigger)
			}},
		},
		{
			name: "mask", caps: general, stopAt: 6, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) {
				var m Mask
				for i := range m.Hi {
					m.Lo[i], m.Hi[i] = 20, 230
				}
				e.SetMask(&m)
				e.SetMaskMode(MaskTest)
			}},
		},
		{
			name: "uart-trigger", caps: uart, stopAt: 6, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) {
				e.SetSerialParams(SerialParams{Proto: core.SerUART, Baud: 115200, Bits: 8, Threshold: 128, HaveThr: true, Addr: -1, RW: 2, Bytes: []int{0x55}})
				e.SetSerialMode(SerialTrigger)
			}},
		},
		{
			name: "bode", caps: general, stopAt: 5, triggers: always, fill: 2,
			hooks: map[int]func(*Engine){1: func(e *Engine) { e.SetBodeMode(true, 0, 1) }},
		},
	}
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-008, REQ-SDS-009, REQ-SDS-040
func TestHALRecordReplay(t *testing.T) {
	dir := filepath.Join("testdata", "halrecord")
	for _, sc := range halScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			got, gotHAL := runHALScenario(t, sc)
			again, againHAL := runHALScenario(t, sc)
			if got != again || gotHAL != againHAL {
				t.Fatalf("the trace is not deterministic")
			}
			for _, tr := range []struct{ path, got string }{
				{filepath.Join(dir, sc.name+".txt"), got},
				{filepath.Join(dir, sc.name+".hal.txt"), gotHAL},
			} {
				compareTrace(t, tr.path, tr.got)
			}
		})
	}
}

// compareTrace compares a trace with its recording, or records it.
func compareTrace(t *testing.T, path, got string) {
	t.Helper()
	if *updateHALRecord {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (record with -update-halrecord)", err)
	}
	if got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := range min(len(gl), len(wl)) {
			if gl[i] != wl[i] {
				t.Fatalf("%s line %d differs:\n got  %s\n want %s", path, i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("%s: length differs: %d lines, want %d", path, len(gl), len(wl))
	}
}

var _ bus.Bus = (*recordedCS3)(nil)

// halLog is the owner loop's trace of HAL calls: what a core owner loop
// driving the same board must call, in order. Service calls are counted
// onto the next call, so a wait's poll-and-service rounds collapse.
type halLog struct {
	log     *callLog
	service int
}

func (h *halLog) add(format string, args ...any) {
	if h.service > 0 {
		format = fmt.Sprintf("Service x%d; ", h.service) + format
		h.service = 0
	}
	h.log.add(format, args...)
}

type recAcquirer struct {
	hal.Acquirer
	h *halLog
}

func (a recAcquirer) Configure(p hal.Plan) error {
	err := a.Acquirer.Configure(p)
	a.h.add("Acquirer.Configure decim %d depth %d -> %v", p.AcqDecim, p.Depth, err)
	return err
}

func (a recAcquirer) Arm() error {
	err := a.Acquirer.Arm()
	a.h.add("Acquirer.Arm -> %v", err)
	return err
}

func (a recAcquirer) Wait(ctx context.Context, d time.Duration) (hal.Status, error) {
	st, err := a.Acquirer.Wait(ctx, d)
	a.h.add("Acquirer.Wait(%v) -> triggered %v done %v record %v %v", d, st.Triggered, st.Done, st.Record, err)
	return st, err
}

func (a recAcquirer) Force() error {
	err := a.Acquirer.Force()
	a.h.add("Acquirer.Force -> %v", err)
	return err
}

func (a recAcquirer) Stop() error {
	err := a.Acquirer.Stop()
	a.h.add("Acquirer.Stop -> %v", err)
	return err
}

type recPlatform struct {
	hal.Platform
	h *halLog
}

func (p recPlatform) Service() {
	p.h.service++
	p.Platform.Service()
}

type recReader struct {
	hal.Readout
	h *halLog
}

func (r recReader) Read(ctx context.Context, req hal.ReadRequest, dst []*sample.Frame, yield func()) error {
	var b strings.Builder
	for _, w := range req.Windows {
		fmt.Fprintf(&b, " {%v %d+%d points %d %v}", w.Role, w.StartSample, w.Span, w.Points, w.Want)
	}
	err := r.Readout.Read(ctx, req, dst, yield)
	r.h.add("Readout.Read%s -> %v", b.String(), err)
	return err
}
