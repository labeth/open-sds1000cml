// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package streams

import (
	"context"
	"sync"
	"time"

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
// on the acquisition owner they are given (decodedHost): the core's HAL
// cycle over the board (NewBoardStreams), which serves them as a hal.Session
// (decoded_session.go). A request from a controller waits on the owner.
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
