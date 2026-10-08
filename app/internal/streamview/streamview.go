// Package streamview is the scope-side decoded stream (ADR-PANEL-DECODED-STREAM):
// it runs one hardware decoded capture, keeps a hexdump transcript of decoded
// units for the display, and on the trigger (or a manual stop) ends the
// capture so the engine shows the retained full-rate record.
// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package streamview

import (
	"context"
	"fmt"
	"sync"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/sramcapture"
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

// State is where a stream stands.
type State int

const (
	Idle State = iota
	Streaming
	Triggered // the trigger fired; the record is on the display
	Stopped   // stopped by the operator
	Failed
	Starting // the capture is being set up (an image load can take seconds)
)

// String names the state for the display.
// TRLC-LINKS: REQ-SDS-018
func (s State) String() string {
	return [...]string{"IDLE", "STREAM", "TRIG'D", "STOP", "FAIL", "START"}[s]
}

// View is a snapshot of the stream's state and counters.
type View struct {
	State           State
	Label           string // protocol and settings
	Lines           int    // transcript lines held
	Events, Units   uint64
	Lost            uint64
	Errors          uint64
	UnitsPerSecond  float64
	Elapsed         time.Duration
	Err             string
	RecordAvailable bool
	TriggerLine     int // transcript line holding the trigger; -1 unknown
}

// Page is a window of formatted transcript lines.
type Page struct {
	Lines       []string
	Top, Total  int // first line shown, lines held
	TriggerLine int // -1 when unknown
	Header      string
}

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
	c.Stop()
	c.mu.Lock()
	if c.view.State == Starting {
		c.mu.Unlock()
		return fmt.Errorf("a stream is already starting")
	}
	c.view = View{Label: label, TriggerLine: -1, State: Starting}
	c.t = decodedlines.New(p.Proto, historyLines)
	c.stopReq = false
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
		c.view.State, c.view.Err = Failed, err.Error()
		return err
	}
	c.view.State = Streaming
	c.cancel, c.done = cancel, make(chan struct{})
	go c.run(ctx, id, c.done) // a STOP pressed while starting is in stopReq
	return nil
}

// Stop ends a running stream by hand; the retained record, when there is
// one, goes to the display.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Stop() {
	c.mu.Lock()
	done := c.done
	if done != nil || c.view.State == Starting {
		c.stopReq = true
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
	buf := make([]sramcapture.DecodedEvent, 32768)
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

// finish ends the capture, handing the record to the display and marking
// the trigger and the record's span in the transcript.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) finish(id sramcapture.RecordIdentity, state State, cause error, lineMode bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, adopted, err := c.src.FreezeDecodedStream(ctx, id)
	if err == nil {
		// The record is frozen and acquisition halted; the units queued up to
		// that moment - the record's own span among them - still belong in
		// the transcript. Decoders running on past the record end the drain.
		past := func(sample uint64) bool { return m.HasSampleTimeline && sample > m.SampleLast }
		buf := make([]sramcapture.DecodedEvent, 32768)
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			if lineMode {
				c.mu.Lock()
				from := c.t.Next()
				c.mu.Unlock()
				b, _, rerr := c.src.ReadDecodedLines(ctx, id, from, true)
				if rerr != nil || len(b.Lines) == 0 {
					break
				}
				stop := false
				for k, l := range b.Lines {
					if past(l.Sample) {
						b.Lines, b.Indices, stop = b.Lines[:k], b.Indices[:k], true
						break
					}
				}
				c.mu.Lock()
				c.appendLocked(b)
				c.mu.Unlock()
				if stop {
					break
				}
				continue
			}
			n, rerr := c.src.ReadDecodedBatchInto(ctx, id, buf)
			if rerr != nil || n == 0 {
				break
			}
			stop := false
			for k, e := range buf[:n] {
				if e.Kind != sramcapture.EventLoss && past(e.Sample) {
					n, stop = k, true
					break
				}
			}
			c.mu.Lock()
			c.t.Add(buf[:n])
			c.mu.Unlock()
			if stop {
				break
			}
		}
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
	if v.State == Streaming {
		v.Elapsed = time.Since(c.start)
	}
	if s := v.Elapsed.Seconds(); s > 0 {
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
	for i := top; i < min(total, top+rows); i++ {
		p.Lines = append(p.Lines, t.Format(i))
	}
	return p
}

// protoIDs maps Autodetect's protocol names to the serial-trigger registry.
var protoIDs = map[string]int{"uart": 1, "i2c": 2, "spi": 3, "manchester": 4, "sent": 5,
	"canfd": 6, "mil1553": 7, "arinc429": 8, "usbls": 9, "flexray": 10}

// ParamsFromAuto turns what Autodetect found on a frame of colTimeS samples
// into the hardware decoder's settings, so the stream starts with exactly
// what the display decoded.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-013
func ParamsFromAuto(r decode.Result, colTimeS float64) (engine.SerialParams, error) {
	id, ok := protoIDs[r.Proto]
	if !r.OK || !ok {
		return engine.SerialParams{}, fmt.Errorf("auto decode has found no protocol to stream")
	}
	if r.Proto == "arinc429" {
		return engine.SerialParams{}, fmt.Errorf("ARINC 429 streams from the web page, which sets its levels")
	}
	if !(colTimeS > 0) || !(r.Thr > 0 && r.Thr < 255) {
		return engine.SerialParams{}, fmt.Errorf("auto decode has no usable threshold or sample time")
	}
	p := engine.SerialParams{Proto: id, ChA: r.Roles.ChA, ChB: r.Roles.ChB, Baud: r.Baud,
		CPOL: r.Roles.CPOL, CPHA: r.Roles.CPHA, MSB: r.Roles.MSB, IEEE: true,
		Threshold: r.Thr, HaveThr: true, Addr: -1, RW: 2}
	rate := 0.0
	if r.SPB > 0 {
		rate = 1 / (r.SPB * colTimeS)
	}
	switch r.Proto {
	case "spi":
		p.SPIClockHz = int(rate + 0.5)
	case "sent":
		p.TickNs = r.SPB * colTimeS * 1e9
	case "canfd":
		if p.Baud == 0 {
			p.Baud = int(rate + 0.5)
		}
	}
	return p, nil
}

// ListRows is how many transcript lines the full-screen list shows; the
// panel centres the trigger with it.
const ListRows = 40

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

// LineData is one transcript line as data: its absolute index since the
// stream started, the first unit's sample ordinal, the units, and which of
// them are decode errors or (I2C) not acknowledged.
type LineData struct {
	Index  int      `json:"i"`
	Sample uint64   `json:"s"`
	Units  []uint32 `json:"v"`
	Errs   uint16   `json:"e,omitempty"`
	Naks   uint16   `json:"k,omitempty"`
	Note   string   `json:"n,omitempty"`
}

// LinesPage answers Lines: Base is the oldest line still held, Next the index
// to ask for next; a reader that falls behind Base has missed lines.
type LinesPage struct {
	State string     `json:"state"`
	Base  int        `json:"base"`
	Next  int        `json:"next"`
	Lines []LineData `json:"lines"`
}

// Lines returns up to limit transcript lines from absolute index from (raised
// to the oldest held), for a reader following the stream.
// TRLC-LINKS: REQ-SDS-018
func (c *Controller) Lines(from, limit int) LinesPage {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.t
	p := LinesPage{State: c.view.State.String(), Base: t.Base(), Next: t.Next()}
	from = min(max(from, t.Base()), p.Next)
	for i := from; i < p.Next && len(p.Lines) < limit; i++ {
		l := t.At(i - t.Base())
		p.Lines = append(p.Lines, LineData{Index: i, Sample: l.Sample, Units: append([]uint32(nil), l.Vals[:l.N]...),
			Errs: l.Errs, Naks: l.Naks, Note: l.Note})
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
