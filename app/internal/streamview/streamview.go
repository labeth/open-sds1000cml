// Package streamview is the scope-side decoded stream (ADR-PANEL-DECODED-STREAM):
// it runs one hardware decoded capture, keeps a hexdump transcript of decoded
// units for the display, and on the trigger (or a manual stop) ends the
// capture so the engine shows the retained full-rate record.
// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package streamview

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/streamwire"
	"github.com/labeth/open-sds1000cml/app/internal/decodedlines"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Source is the engine's decoded-capture surface.
type Source interface {
	BeginDecodedStream(ctx context.Context, p engine.SerialParams, trig engine.StreamTrigger, value int) (sramcapture.RecordIdentity, error)
	ReadDecodedBatchInto(ctx context.Context, id sramcapture.RecordIdentity, dst []sramcapture.DecodedEvent) (int, error)
	DecodedCaptureStatus(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, error)
	FreezeDecodedStream(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, bool, error)
	EndDecodedCapture(ctx context.Context, id sramcapture.RecordIdentity) error
	// ReadDecodedLines pulls worker-built lines; ok false means event mode.
	ReadDecodedLines(ctx context.Context, id sramcapture.RecordIdentity, from int, flush bool) (decodedlines.Batch, bool, error)
}

// State is streamwire.State.
type State = streamwire.State

// The states of a stream (streamwire's).
const (
	Idle      = streamwire.Idle
	Streaming = streamwire.Streaming
	Triggered = streamwire.Triggered
	Stopped   = streamwire.Stopped
	Failed    = streamwire.Failed
	Starting  = streamwire.Starting
)

// View is streamwire.View.
type View = streamwire.View

// Page is streamwire.Page.
type Page = streamwire.Page

// historyLines bounds the transcript (about 260 k bytes of 8-bit units).
const historyLines = 16384

// Controller runs at most one stream.
type Controller struct {
	src  Source
	logf func(string, ...any)

	mu      sync.Mutex
	view    View
	t       *decodedlines.Transcript
	start   time.Time
	cancel  context.CancelFunc
	done    chan struct{}
	stopReq bool
	discard bool // the stop wants live captures next: the record is not kept
}

// New makes a controller over src.
// TRLC-LINKS: REQ-SDS-018
func New(src Source, logf func(string, ...any)) *Controller {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Controller{src: src, logf: logf, view: View{TriggerLine: -1}, t: decodedlines.New(0, historyLines)}
}

// Snapshot returns the current state and counters.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Snapshot() View {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshLocked()
	return c.view
}

// Active reports a stream starting or running.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.State == Streaming || c.view.State == Starting
}

// Start begins a stream with p's decoder and the trigger; label describes
// the settings for the display. A running stream is stopped first.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Start(p engine.SerialParams, trig engine.StreamTrigger, value int, label string) error {
	return c.StartNoted(p, trig, value, label, "")
}

// StartNoted is Start with a note shown while the stream starts: why it
// takes seconds ("loading the general FPGA image: the qualifier image has no
// UART receiver").
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) StartNoted(p engine.SerialParams, trig engine.StreamTrigger, value int, label, note string) error {
	c.End()
	c.mu.Lock()
	if c.view.State == Starting {
		c.mu.Unlock()
		return errors.New("a stream is already starting")
	}
	c.view = View{Label: label, TriggerLine: -1, State: Starting, Note: note}
	c.t = decodedlines.New(p.Proto, historyLines)
	c.stopReq, c.discard = false, false
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	begin, cancelBegin := context.WithTimeout(ctx, 20*time.Second)
	id, err := c.src.BeginDecodedStream(begin, p, trig, value)
	cancelBegin()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.start = time.Now()
	if err != nil {
		cancel()
		c.view.State, c.view.Err, c.view.Note = Failed, err.Error(), ""
		return err
	}
	c.view.State, c.view.Note = Streaming, ""
	c.cancel, c.done = cancel, make(chan struct{})
	go c.run(ctx, id, c.done) // a STOP pressed while starting is in stopReq
	return nil
}

// Stop ends a running stream by hand; the retained record, when there is
// one, goes to the display.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Stop() { c.stop(false) }

// End ends a running stream for live captures (BUS's Stream view left, or a
// stream started again): its record is not handed to the display, whose
// recall would hold the next capture for seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) End() { c.stop(true) }

// stop is Stop and End.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) stop(discard bool) {
	c.mu.Lock()
	done := c.done
	if done != nil || c.view.State == Starting {
		c.stopReq = true
		c.discard = c.discard || discard
	}
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

// run follows the stream until the trigger, a STOP or a failure ends the
// capture. With an isolated worker the worker builds the transcript and run
// pulls finished lines (ADR-STREAM-LINES-IN-WORKER); otherwise it builds the
// lines from events itself.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) run(ctx context.Context, id sramcapture.RecordIdentity, done chan struct{}) {
	defer close(done)
	defer func() {
		c.mu.Lock()
		c.cancel()
		c.done = nil
		c.mu.Unlock()
	}()
	_, lineMode, err := c.src.ReadDecodedLines(ctx, id, 0, false)
	if err != nil {
		c.finish(id, Failed, err, lineMode)
		return
	}
	var buf []sramcapture.DecodedEvent // events: only when run builds the lines
	if !lineMode {
		buf = make([]sramcapture.DecodedEvent, 32768)
	}
	nextStatus := time.Now()
	for {
		var got int
		var hint bool
		if lineMode {
			c.mu.Lock()
			from := c.t.Next()
			c.mu.Unlock()
			b, _, err := c.src.ReadDecodedLines(ctx, id, from, false)
			if err != nil {
				c.finish(id, Failed, err, lineMode)
				return
			}
			c.mu.Lock()
			c.appendLocked(b)
			c.mu.Unlock()
			got, hint = len(b.Lines), b.TriggerHint
		} else {
			n, err := c.src.ReadDecodedBatchInto(ctx, id, buf)
			if err != nil {
				c.finish(id, Failed, err, lineMode)
				return
			}
			c.mu.Lock()
			c.t.Add(buf[:n])
			hint = c.t.TakeTriggerHint()
			c.mu.Unlock()
			got = n
		}
		c.mu.Lock()
		stop := c.stopReq
		c.mu.Unlock()
		if stop {
			c.finish(id, Stopped, nil, lineMode)
			return
		}
		// The status read costs a score of register round trips on the
		// acquisition owner: poll it once a second, and at once when the
		// stream reports the trigger or the retained record.
		if hint || time.Now().After(nextStatus) {
			nextStatus = time.Now().Add(time.Second)
			m, err := c.src.DecodedCaptureStatus(ctx, id)
			if err != nil {
				c.finish(id, Failed, err, lineMode)
				return
			}
			if m.Triggered && m.Frozen && m.Ready {
				c.finish(id, Triggered, nil, lineMode)
				return
			}
		}
		if got == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// appendLocked adds a worker line batch, its totals and any missed lines.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) appendLocked(b decodedlines.Batch) {
	for i, l := range b.Lines {
		c.t.Append(b.Indices[i], l)
	}
	c.t.SetCounters(b.Counters)
}

// drainRecord moves into the transcript the units queued up to the moment
// the record froze (acquisition halted): the record's own span among them
// still belongs there. Decoders running on past the record end the drain, as
// does a quiet source or two seconds.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) drainRecord(ctx context.Context, id sramcapture.RecordIdentity, m sramcapture.Metadata, lineMode bool) {
	past := func(sample uint64) bool { return m.HasSampleTimeline && sample > m.SampleLast }
	var buf []sramcapture.DecodedEvent
	if !lineMode {
		buf = make([]sramcapture.DecodedEvent, 32768)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		var more bool
		if lineMode {
			more = c.drainLines(ctx, id, past)
		} else {
			more = c.drainEvents(ctx, id, buf, past)
		}
		if !more {
			return
		}
	}
}

// drainLines moves one batch of worker-built lines up to the record's end
// into the transcript; false when the drain is over.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) drainLines(ctx context.Context, id sramcapture.RecordIdentity, past func(uint64) bool) bool {
	c.mu.Lock()
	from := c.t.Next()
	c.mu.Unlock()
	b, _, err := c.src.ReadDecodedLines(ctx, id, from, true)
	if err != nil || len(b.Lines) == 0 {
		return false
	}
	more := true
	for k, l := range b.Lines {
		if past(l.Sample) {
			b.Lines, b.Indices, more = b.Lines[:k], b.Indices[:k], false
			break
		}
	}
	c.mu.Lock()
	c.appendLocked(b)
	c.mu.Unlock()
	return more
}

// drainEvents moves one batch of decoded events up to the record's end into
// the transcript; false when the drain is over.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) drainEvents(ctx context.Context, id sramcapture.RecordIdentity, buf []sramcapture.DecodedEvent, past func(uint64) bool) bool {
	n, err := c.src.ReadDecodedBatchInto(ctx, id, buf)
	if err != nil || n == 0 {
		return false
	}
	more := true
	for k, e := range buf[:n] {
		if e.Kind != sramcapture.EventLoss && past(e.Sample) {
			n, more = k, false
			break
		}
	}
	c.mu.Lock()
	c.t.Add(buf[:n])
	c.mu.Unlock()
	return more
}

// finish ends the capture, handing the record to the display and marking
// the trigger and the record's span in the transcript.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) finish(id sramcapture.RecordIdentity, state State, cause error, lineMode bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, adopted, err := c.src.FreezeDecodedStream(ctx, id)
	c.mu.Lock()
	discard := c.discard && state == Stopped
	c.mu.Unlock()
	if d, ok := c.src.(interface{ DiscardDecodedRecord() }); ok && discard && adopted {
		d.DiscardDecodedRecord() // before the end: the loop takes the record only after it
		adopted = false
	}
	if err == nil {
		c.drainRecord(ctx, id, m, lineMode)
	}
	if endErr := c.src.EndDecodedCapture(ctx, id); err == nil {
		err = endErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t.Flush()
	if adopted && m.HasSampleTimeline {
		c.t.Mark(m.SampleFirst+2*uint64(m.TriggerIndex), m.SampleFirst, m.SampleLast)
	}
	c.refreshLocked()
	c.view.State, c.view.RecordAvailable = state, adopted
	if cause == nil {
		cause = err
	}
	if cause != nil {
		c.view.State, c.view.Err = Failed, cause.Error()
		c.logf("streamview: %v", cause)
	}
}

// refreshLocked copies the transcript's counters into the view.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) refreshLocked() {
	v, t := &c.view, c.t
	v.Lines, v.Events, v.Units, v.Lost, v.Errors = t.Count(), t.Events, t.Units, t.Lost, t.Errors
	v.TriggerLine = t.TriggerLine()
	if m, ok := c.src.(interface{ DecodedStreamMode() string }); ok && v.State == Streaming {
		v.Mode = m.DecodedStreamMode() // kept as it was at the end once stopped
	}
	if v.State == Streaming {
		v.Elapsed = time.Since(c.start)
	}
	if r, ok := t.Rate(); ok {
		v.UnitsPerSecond = r
	} else if s := v.Elapsed.Seconds(); s > 0 {
		v.UnitsPerSecond = float64(t.Units) / s
	}
}

// Window formats rows transcript lines starting at top; top < 0 (or past the
// end) shows the newest lines.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Window(rows, top int) Page {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.t
	total := t.Count()
	if top < 0 || top > total-rows {
		top = total - rows
	}
	top = max(top, 0)
	p := Page{Top: top, Total: total, TriggerLine: t.TriggerLine(), Header: t.Header()}
	p.Lines = make([]string, 0, max(0, min(total, top+rows)-top))
	for i := top; i < min(total, top+rows); i++ {
		p.Lines = append(p.Lines, t.Format(i))
	}
	return p
}

// ParamsFromDecode completes p with what a decode r of a frame of colTimeS
// samples measured: the core engine's.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-013, REQ-SDS-222
func ParamsFromDecode(p engine.SerialParams, r decode.Result, colTimeS float64) (engine.SerialParams, error) {
	return core.ParamsFromDecode(p, r, colTimeS)
}

// ListRows is streamwire.ListRows.
const ListRows = streamwire.ListRows

// Refuse shows why a stream could not start, without touching a running one.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Refuse(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.view.State == Streaming || c.view.State == Starting {
		return
	}
	c.view = View{State: Failed, Err: reason, TriggerLine: -1}
	c.t = decodedlines.New(0, historyLines)
}

// LineData is streamwire.LineData.
type LineData = streamwire.LineData

// LinesPage is streamwire.LinesPage.
type LinesPage = streamwire.LinesPage

// Lines returns up to limit transcript lines from absolute index from (raised
// to the oldest held), for a reader following the stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Lines(from, limit int) LinesPage {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.t
	p := LinesPage{State: c.view.State.String(), Base: t.Base(), Next: t.Next()}
	from = min(max(from, t.Base()), p.Next)
	n := max(0, min(p.Next-from, limit))
	// The lines' units share one slice: an allocation a page, not a line.
	units := 0
	for i := from; i < from+n; i++ {
		units += int(t.At(i - t.Base()).N)
	}
	vals := make([]uint32, 0, units)
	p.Lines = make([]LineData, 0, n)
	for i := from; i < from+n; i++ {
		l := t.At(i - t.Base())
		var u []uint32 // nil for a line without units, as before (JSON null)
		if l.N > 0 {
			k := len(vals)
			vals = append(vals, l.Vals[:l.N]...)
			u = vals[k:len(vals):len(vals)]
		}
		p.Lines = append(p.Lines, LineData{
			Index: i, Sample: l.Sample, Units: u,
			Errs: l.Errs, Naks: l.Naks, Note: l.Note,
		})
	}
	if len(p.Lines) > 0 {
		p.Next = p.Lines[len(p.Lines)-1].Index + 1
	}
	return p
}

// LinesBinary encodes transcript lines from absolute index from into dst in
// the decodedlines batch format and returns the bytes used: a cheap way for a
// client to follow a fast stream (/api/stream/lines.bin).
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) LinesBinary(from int, dst []byte) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t.EncodeLines(from, dst)
}
