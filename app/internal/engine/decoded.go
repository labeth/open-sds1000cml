// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/decodedlines"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// DecodedStreams are the scope's decoded streams on its fabric: the FPGA's
// decoded captures (the scope's BUS stream and a web page's capture), the
// software-decoded streams of the symbol protocols (gap-free on the stream
// and digital stream images, or as captures, with the history ring) and the
// client decode tap a web page decodes the digital stream through. They run
// on the acquisition owner they are given (decodedHost): today the engine's
// owner loop, which steps them where it always did; the same streams serve
// the core's HAL cycle through hal.Session (session.go) once it drives the
// board. A request from a controller waits on the owner as before.
type DecodedStreams struct {
	h    decodedHost
	done <-chan struct{}

	mu    sync.Mutex
	stats Stats          // DecodedStreamMode and DecodedWeb, as the snapshot shows them
	adopt *adoptedRecord // a decoded stream's record for the stopped loop to show

	decodedCfg      sramcapture.Config // the running decoded capture's configuration (acquisition owner)
	decodedLines    bool               // the worker builds the stream transcript; no event pumping (owner)
	decodedStarting time.Time          // a decoded stream holds its image until its capture begins, at most until then (owner)

	decodedRequests           chan core.OwnerJob
	decodedSession            *sramcapture.DecodedCapture // acquisition-owner fields
	decodedBuffer             []sramcapture.DecodedEvent
	decodedHead, decodedCount int
	decodedScratch            []sramcapture.DecodedEvent
	decodedSequence           uint32
	decodedError              error
	decodedService            decodedServiceStats
	decodedDeadline           *bus.DecodedDeadline
	decodedWeb                bool        // the session is a web page's (BeginDecodedCapture), not the scope's stream
	decodedContact            time.Time   // a client's last call on the session (status, events, record)
	soft                      *softStream // a software-decoded stream (soft_stream.go)
	// ctap is the client decode's ring and the page's frames (soft_client.go),
	// shared by the owner and the web's requests under its own lock.
	ctap      clientTap
	softEpoch uint32
}

// decodedHost is the acquisition owner the decoded streams run on: its
// run state, its other streams, its image switch, its health beat and the
// board, memory, bus and clock it drives.
type decodedHost interface {
	setRunning(on bool)
	soundYield()
	hasImages() bool
	hasImage(name string) bool
	loadImage(name string) bool
	protocolImage() string
	freshStreamImage(image string) error
	noteStreamLoss(expected uint64)
	busErr(err error)
	sleepBeating(d time.Duration)
	beat()
	stopping() bool
	planSerial(c *sramcapture.Config, serial SerialParams, fallbackNormal bool)
	planHysteresis(c *sramcapture.Config)
	symbolCount(cfg *sramcapture.Config) int
	arincAux() uint32
	ownStreamPump() (streamPumper, error)
	memory() acq.Memory
	board() *acq.Board
	bus() bus.Bus
	calibration() *InterleaveCalibration
	clock() Clock
	logf(format string, a ...any)
}

// newDecodedStreams are the decoded streams on owner h, which closes done
// when it stops.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func newDecodedStreams(h decodedHost, done <-chan struct{}) *DecodedStreams {
	return &DecodedStreams{h: h, done: done, decodedRequests: make(chan core.OwnerJob, 8)}
}

// fillStats puts the decoded streams' status into s: how the stream runs
// and whether a web page's capture holds the acquisition.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) fillStats(s *Stats) {
	d.mu.Lock()
	s.DecodedStreamMode, s.DecodedWeb = d.stats.DecodedStreamMode, d.stats.DecodedWeb
	d.mu.Unlock()
}

// callOwner queues run for the owner the streams run on and waits for its
// reply (ownerCall).
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (d *DecodedStreams) callOwner(ctx context.Context, queue chan<- core.OwnerJob, run func() (any, error)) (any, error) {
	return ownerCall(ctx, queue, d.done, run)
}

// ownerCall queues run for the owner loop and waits for its reply. A
// caller whose context ends gets its error at once; a job already running
// still runs to completion. done closes when the owner stops.
// TRLC-LINKS: REQ-SDS-001
func ownerCall(ctx context.Context, queue chan<- core.OwnerJob, done <-chan struct{}, run func() (any, error)) (any, error) {
	job := core.OwnerJob{Cancelled: ctx.Err, Run: run, Done: make(chan core.OwnerReply, 1)}
	select {
	case queue <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return nil, ErrStopped
	}
	select {
	case r := <-job.Done:
		return r.Value, r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return nil, ErrStopped
	}
}

// callOwner queues run for the engine's owner loop and waits for its reply
// (ownerCall).
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) callOwner(ctx context.Context, queue chan<- core.OwnerJob, run func() (any, error)) (any, error) {
	return ownerCall(ctx, queue, e.done, run)
}

// engineHost is the engine's owner loop as the decoded streams' owner.
type engineHost struct{ e *Engine }

var _ decodedHost = engineHost{}

// TRLC-LINKS: REQ-SDS-013
func (h engineHost) setRunning(on bool) { h.e.SetRunning(on) }

// TRLC-LINKS: REQ-SDS-265
func (h engineHost) soundYield() { h.e.soundYield() }

// TRLC-LINKS: REQ-SDS-005
func (h engineHost) hasImages() bool { return h.e.images != nil }

// TRLC-LINKS: REQ-SDS-005
func (h engineHost) hasImage(name string) bool { return h.e.hasImage(name) }

// TRLC-LINKS: REQ-SDS-005
func (h engineHost) loadImage(name string) bool { return h.e.loadImage(name) }

// TRLC-LINKS: REQ-SDS-005
func (h engineHost) protocolImage() string { return h.e.protocolImage }

// TRLC-LINKS: REQ-SDS-035
func (h engineHost) freshStreamImage(image string) error { return h.e.freshStreamImage(image) }

// TRLC-LINKS: REQ-SDS-035
func (h engineHost) noteStreamLoss(expected uint64) { h.e.noteStreamLoss(expected) }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) busErr(err error) { h.e.busErr(err) }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) sleepBeating(d time.Duration) { h.e.sleepBeating(d) }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) beat() { h.e.beatN.Add(1) }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) stopping() bool { return h.e.stopReq.Load() }

// TRLC-LINKS: REQ-SDS-013
func (h engineHost) planSerial(c *sramcapture.Config, serial SerialParams, fallbackNormal bool) {
	h.e.planSerial(c, serial, fallbackNormal)
}

// TRLC-LINKS: REQ-SDS-011
func (h engineHost) planHysteresis(c *sramcapture.Config) { h.e.planHysteresis(c) }

// TRLC-LINKS: REQ-SDS-013
func (h engineHost) symbolCount(cfg *sramcapture.Config) int { return h.e.symbolCount(cfg) }

// TRLC-LINKS: REQ-SDS-013
func (h engineHost) arincAux() uint32 { return h.e.arincAux.Load() }

// TRLC-LINKS: REQ-SDS-018
func (h engineHost) ownStreamPump() (streamPumper, error) { return h.e.ownStreamPump() }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) memory() acq.Memory { return h.e.sram }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) board() *acq.Board { return h.e.hw() }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) bus() bus.Bus { return h.e.b }

// TRLC-LINKS: REQ-SDS-010
func (h engineHost) calibration() *InterleaveCalibration { return h.e.interleaveCalibration }

// TRLC-LINKS: REQ-SDS-001
func (h engineHost) clock() Clock { return h.e.clk }

// TRLC-LINKS: REQ-SDS-167
func (h engineHost) logf(format string, a ...any) { h.e.logf(format, a...) }

// The engine's decoded streams, as the controllers and the web reach them.

// DecodedStreams is the engine's decoded streams.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) DecodedStreams() *DecodedStreams { return e.streams() }

// streams is the engine's decoded streams, made on first use for an engine
// a test builds without New.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) streams() *DecodedStreams {
	if e.ds == nil {
		e.ds = newDecodedStreams(engineHost{e}, e.done)
	}
	return e.ds
}

// BeginDecodedStream starts the scope's decoded stream (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) BeginDecodedStream(ctx context.Context, p SerialParams, trig StreamTrigger, value int) (sramcapture.RecordIdentity, error) {
	return e.streams().BeginDecodedStream(ctx, p, trig, value)
}

// ReadDecodedLines reads the worker's transcript lines (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) ReadDecodedLines(ctx context.Context, id sramcapture.RecordIdentity, from int, flush bool) (decodedlines.Batch, bool, error) {
	return e.streams().ReadDecodedLines(ctx, id, from, flush)
}

// FreezeDecodedStream freezes a decoded stream's record (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) FreezeDecodedStream(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, bool, error) {
	return e.streams().FreezeDecodedStream(ctx, id)
}

// HoldDecodedImage keeps the loaded image between two streams
// (DecodedStreams).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (e *Engine) HoldDecodedImage(ctx context.Context) error {
	return e.streams().HoldDecodedImage(ctx)
}

// DiscardDecodedRecord drops a stream's frozen record (DecodedStreams).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (e *Engine) DiscardDecodedRecord() { e.streams().DiscardDecodedRecord() }

// LeaseClientDecode holds the client decode (DecodedStreams).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) LeaseClientDecode() { e.streams().LeaseClientDecode() }

// ReadClientWords is a page's read of the client-decode ring
// (DecodedStreams).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) ReadClientWords(seq uint64, limit int, wait time.Duration, dst []ClientChunk) (ClientWords, func()) {
	return e.streams().ReadClientWords(seq, limit, wait, dst)
}

// PostClientFrames queues a page's decoded frames (DecodedStreams).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) PostClientFrames(b []byte) error { return e.streams().PostClientFrames(b) }

// BeginDecodedCapture starts a web page's decoded capture (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) BeginDecodedCapture(ctx context.Context, cfg sramcapture.Config) (sramcapture.RecordIdentity, error) {
	return e.streams().BeginDecodedCapture(ctx, cfg)
}

// ReadDecodedBatch returns a batch of a capture's events (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) ReadDecodedBatch(ctx context.Context, id sramcapture.RecordIdentity) ([]sramcapture.DecodedEvent, error) {
	return e.streams().ReadDecodedBatch(ctx, id)
}

// ReadDecodedBatchInto copies a batch of a capture's events into dst
// (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) ReadDecodedBatchInto(ctx context.Context, id sramcapture.RecordIdentity, dst []sramcapture.DecodedEvent) (int, error) {
	return e.streams().ReadDecodedBatchInto(ctx, id, dst)
}

// DecodedCaptureStatus reads a capture's retained record (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) DecodedCaptureStatus(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, error) {
	return e.streams().DecodedCaptureStatus(ctx, id)
}

// RecallDecodedRecord reads a capture's retained record (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-033
func (e *Engine) RecallDecodedRecord(ctx context.Context, id sramcapture.RecordIdentity, offset, count uint32) ([]byte, error) {
	return e.streams().RecallDecodedRecord(ctx, id, offset, count)
}

// EndDecodedCapture ends a capture (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) EndDecodedCapture(ctx context.Context, id sramcapture.RecordIdentity) error {
	return e.streams().EndDecodedCapture(ctx, id)
}

// EndWebDecodedCapture ends a web page's capture for the scope's keys
// (DecodedStreams).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) EndWebDecodedCapture(ctx context.Context) (bool, error) {
	return e.streams().EndWebDecodedCapture(ctx)
}

// DecodedStreamMode says how the decoded stream runs (DecodedStreams).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) DecodedStreamMode() string { return e.streams().DecodedStreamMode() }
