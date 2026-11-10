// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The decoded streams as the board's own stream sessions (hal.Session,
// ADR-HAL-BOARD-SESSIONS): on the core's HAL cycle the board's decoded
// streams run on the cycle's owner, which serves their requests at its
// commit points and gives them the pass while one holds the acquisition,
// as the engine's owner loop does (serviceCommands, sramCycle.Claim). Over
// the board alone (NewBoardStreams) their owner's other duties - the run
// state, the audio stream - are the cycle's, and the image switch, the
// stream image's reload and the bus errors the board's.

var _ hal.Session = (*DecodedStreams)(nil)

// Attach takes the cycle as the owner of streams made over the board
// (NewBoardStreams); the engine's own streams have their owner already.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) Attach(h hal.SessionHost) {
	if bh, ok := d.h.(*boardHost); ok {
		bh.mu.Lock()
		bh.owner = h
		bh.mu.Unlock()
	}
}

// Serve runs the controllers' requests, pumps a decoded capture's events
// and ends a session its reader left, as the engine's commit point does;
// a session that took the acquisition from a waiting capture leaves the
// board's capture released.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) Serve() {
	if d.h.memory() == nil {
		return
	}
	// The service figures of a decoded capture, as the engine's commit
	// point takes them: its requests here, the board's panel service
	// (which the cycle runs after this) by the board's own timing.
	b := d.h.board()
	if d.decodedSession != nil {
		stage := time.Now()
		d.serviceDecodedRequests()
		if d.decodedSession != nil {
			d.decodedService.maxRequests = max(d.decodedService.maxRequests, time.Since(stage))
			d.decodedService.maxControl = max(d.decodedService.maxControl, b.ServiceMax())
		}
	} else {
		d.serviceDecodedRequests()
	}
	b.TimeService(d.decodedSession != nil)
	d.pumpDecodedEvents()
	d.expireDecoded()
	if d.Active() {
		d.h.board().ReleaseCapture()
	}
}

// Step runs a software-decoded stream one step, or waits on a decoded
// capture's service, as sramCycle.Claim does; false when neither runs.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) Step(context.Context) bool {
	switch {
	case d.soft != nil:
		d.softStep()
		return true
	case d.decodedSession != nil:
		d.decodedIdle()
		return true
	}
	return false
}

// Active reports a decoded capture or a software-decoded stream holding
// the acquisition.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) Active() bool { return d.soft != nil || d.decodedSession != nil }

// TakeRecord hands the record a decoded stream ended on to the board as
// its stored record, once its capture has ended (the adoption and the end
// are two owner calls).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) TakeRecord() bool {
	if d.Active() {
		return false
	}
	a := d.takeAdopted()
	if a == nil {
		return false
	}
	d.h.board().AdoptRecord(a.m, a.cfg)
	d.h.logf("engine: showing the decoded stream's record (%d words, trigger word %d)", a.m.Length, a.m.TriggerIndex)
	return true
}

// HoldsImage reports the image a decoded stream runs on held for it: a
// stream starting, or started again soon (HoldDecodedImage), as the
// engine's owner keeps it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (d *DecodedStreams) HoldsImage() bool {
	return d.h.clock().Now().Before(d.decodedStarting)
}

// Close ends a decoded capture and a software stream still running.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) Close() {
	if d.decodedSession != nil {
		if err := d.h.board().Acquirer().Stop(); err != nil {
			d.h.logf("decoded shutdown halt: %v", err)
		}
		if _, err := d.h.memory().EnableDecodedEvents(false); err != nil {
			d.h.logf("decoded shutdown stream: %v", err)
		}
	}
	if err := d.closeDecodedDeadline(); err != nil {
		d.h.logf("decoded shutdown scheduling: %v", err)
	}
	if d.soft != nil {
		d.softStreamStop(d.soft)
	}
	if bh, ok := d.h.(*boardHost); ok {
		bh.close()
	}
}

// NewBoardStreams are the decoded streams over board b alone, for the
// core's HAL cycle to serve and step once attached (Board.SetSession):
// bus is the bus the board's memory drives (the worker's stream pump, the
// decoded stream's FIFO scheduling); logf the app's log.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func NewBoardStreams(b *acq.Board, bs bus.Bus, logf func(string, ...any)) *DecodedStreams {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	h := &boardHost{b: b, bs: bs, log: logf, clk: core.RealClock(), done: make(chan struct{})}
	return newDecodedStreams(h, h.done)
}

// boardHost is the board and the core's cycle as the decoded streams'
// owner.
type boardHost struct {
	b   *acq.Board
	bs  bus.Bus
	log func(string, ...any)
	clk Clock

	mu     sync.Mutex
	owner  hal.SessionHost // the cycle, once attached
	done   chan struct{}
	closed bool
	reload bool // a stream lost words before its first: its image loads again first
}

var _ decodedHost = (*boardHost)(nil)

// host is the attached cycle; nil before Attach.
// TRLC-LINKS: REQ-SDS-013
func (h *boardHost) host() hal.SessionHost {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.owner
}

// close closes done once: requests waiting for the owner end.
// TRLC-LINKS: REQ-SDS-013
func (h *boardHost) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}

// setRunning is the cycle's run state.
// TRLC-LINKS: REQ-SDS-013
func (h *boardHost) setRunning(on bool) {
	if o := h.host(); o != nil {
		o.SetRunning(on)
	}
}

// soundYield ends the cycle's audio stream.
// TRLC-LINKS: REQ-SDS-265
func (h *boardHost) soundYield() {
	if o := h.host(); o != nil {
		o.YieldStreams()
	}
}

// TRLC-LINKS: REQ-SDS-005
func (h *boardHost) hasImages() bool { return h.b.ImageLoader() != nil }

// TRLC-LINKS: REQ-SDS-005
func (h *boardHost) hasImage(name string) bool { return h.b.HasImage(name) }

// loadImage loads image name through the board's images (the general one
// when it fails, as the engine's switch does); false never: the image in
// use says what loaded.
// TRLC-LINKS: REQ-SDS-005
func (h *boardHost) loadImage(name string) bool {
	im, ok := h.b.Images()
	if !ok {
		return true
	}
	h.log("engine: loading the %s image", imageShown(name))
	if err := im.Load(context.Background(), []string{acq.FeatureOf(name)}); err != nil {
		h.log("engine: %s image: %v", imageShown(name), err)
		if name != "" {
			if err := im.Load(context.Background(), []string{acq.FeatureOf("")}); err != nil {
				h.busErr(err)
			}
		}
	}
	return true
}

// imageShown is an image's name in the log.
// TRLC-LINKS: REQ-SDS-005
func imageShown(name string) string {
	if name == "" {
		return acq.ImageGeneralName
	}
	return name
}

// TRLC-LINKS: REQ-SDS-005
func (h *boardHost) protocolImage() string { return h.b.LoadedImage() }

// freshStreamImage loads a stream's image again when a stream lost words
// before its first (noteStreamLoss), as the engine does.
// TRLC-LINKS: REQ-SDS-035
func (h *boardHost) freshStreamImage(image string) error {
	if !h.reload || !h.hasImages() || !h.hasImage(image) {
		return nil
	}
	h.reload = false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := h.b.Memory().Halt(ctx)
	cancel()
	if err != nil {
		return err
	}
	h.loadImage(image)
	if h.b.LoadedImage() != image {
		return errors.New("the " + image + " image did not load")
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-035
func (h *boardHost) noteStreamLoss(expected uint64) {
	if expected == 0 {
		h.reload = true
	}
}

// busErr counts and logs a bus error on the board, the first few and every
// hundredth (the status's bus_errors).
// TRLC-LINKS: REQ-SDS-001
func (h *boardHost) busErr(err error) { h.b.NoteBusError(err) }

// sleepBeating sleeps d in slices of at most 500 ms, beating the cycle's
// heartbeat after each, as the engine's sleepBeating does; a closed
// session stops it early.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-025
func (h *boardHost) sleepBeating(d time.Duration) {
	for d > 0 && !h.stopping() {
		s := min(d, 500*time.Millisecond)
		h.clk.Sleep(s)
		h.beat()
		d -= s
	}
}

// beat advances the cycle's heartbeat after a step of a session's work
// (a decoded capture's successful read).
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-025
func (h *boardHost) beat() {
	if b, ok := h.host().(interface{ Beat() }); ok {
		b.Beat()
	}
}

// TRLC-LINKS: REQ-SDS-001
func (h *boardHost) stopping() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// TRLC-LINKS: REQ-SDS-013
func (h *boardHost) planSerial(c *sramcapture.Config, serial SerialParams, fallbackNormal bool) {
	h.b.PlanSerial(c, serial, fallbackNormal, 0)
}

// TRLC-LINKS: REQ-SDS-011
func (h *boardHost) planHysteresis(c *sramcapture.Config) { h.b.PlanHysteresis(c) }

// symbolCount is the symbol trigger's match count (modulo 2^16), -1
// without the block.
// TRLC-LINKS: REQ-SDS-013
func (h *boardHost) symbolCount(cfg *sramcapture.Config) int {
	if !cfg.Symbol.Enabled {
		return -1
	}
	st, ok := h.b.Trigger().Matches()
	if !ok {
		return -1
	}
	return int(st.Matches & 0xffff)
}

// arincAux is ARINC 429's slicer the cycle learned from the records, as
// the board's bus trigger programmed it last (0: none yet).
// TRLC-LINKS: REQ-SDS-013
func (h *boardHost) arincAux() uint32 { return h.b.ARINCAux() }

// ownStreamPump is the bus worker's stream-bank pump, when it took the
// stream.
// TRLC-LINKS: REQ-SDS-018
func (h *boardHost) ownStreamPump() (streamPumper, error) {
	p, ok := h.bs.(streamPumper)
	if !ok {
		return nil, nil
	}
	owned, err := p.SetIsolatedStreamPump(true)
	if err != nil || !owned {
		return nil, err
	}
	return p, nil
}

// TRLC-LINKS: REQ-SDS-001
func (h *boardHost) memory() acq.Memory { return h.b.Memory() }

// TRLC-LINKS: REQ-SDS-001
func (h *boardHost) board() *acq.Board { return h.b }

// TRLC-LINKS: REQ-SDS-001
func (h *boardHost) bus() bus.Bus { return h.bs }

// TRLC-LINKS: REQ-SDS-010
func (h *boardHost) calibration() *InterleaveCalibration { return h.b.InterleaveCalibration() }

// TRLC-LINKS: REQ-SDS-001
func (h *boardHost) clock() Clock { return h.clk }

// TRLC-LINKS: REQ-SDS-167
func (h *boardHost) logf(format string, a ...any) { h.log(format, a...) }

// DecodedStatus is how the decoded stream runs and whether a web page's
// decoded capture holds the acquisition, as the snapshot shows them.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) DecodedStatus() (mode string, web bool) {
	var s Stats
	d.fillStats(&s)
	return s.DecodedStreamMode, s.DecodedWeb
}
