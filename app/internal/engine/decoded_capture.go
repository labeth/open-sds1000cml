// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

const decodedBufferLimit = 131072 // 5 MiB; finite client-backpressure allowance

// decodedAbandonAfter ends a web page's decoded capture no client has touched
// for so long: a page closed, reloaded or gone mid-session would otherwise
// keep the acquisition waiting on it for good. An open page polls the status
// twice a second and reads the events as they come; a hidden tab's timers
// still run once a minute.
const decodedAbandonAfter = 2 * time.Minute

// decodedOrphanAfter is how long the scope's own decoded stream (the panel's,
// read without pause while it runs) may go unread before it is taken as
// left open by a reader that failed.
const decodedOrphanAfter = 10 * time.Second

// Fixed storage keeps service-gap diagnostics out of the allocator while the
// FIFO is live. Report only after disabling the producer.
type decodedServiceStats struct {
	started, last                     time.Time
	lastConsumer                      time.Time
	maxConsumerGap                    time.Duration
	maxBuffered                       int
	maxRequests, maxControl, maxPause time.Duration
	maxGap, maxRead                   time.Duration
	reads, events, lost, slow         uint64
	pauseTotal                        uint64
	gaps                              [16]struct{ At, Gap time.Duration }
}

// decodedCall stages SRAM work on the existing acquisition owner. Cancellation
// of a queued request prevents it from touching hardware when serviced later.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (d *DecodedStreams) decodedCall(ctx context.Context, run func() (any, error)) (any, error) {
	if d.h.memory() == nil {
		return nil, errors.New("decoded capture requires the general SRAM image")
	}
	return d.callOwner(ctx, d.decodedRequests, run)
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (d *DecodedStreams) serviceDecodedRequests() {
	for range 8 {
		select {
		case job := <-d.decodedRequests:
			if job.Serve() {
				d.h.beat()
			}
		default:
			return
		}
	}
}

// pumpDecodedEvents never waits for clients. FIFO loss markers pass through;
// host buffer exhaustion or sequence discontinuity terminates the transcript
// explicitly while the trigger and retained SRAM record remain independent.
// decodedBatchMax bounds one consumer copy: enough to drain a full owner
// iteration's events (decodedPumpReads x sramcapture.DecodedBatchMax).
const decodedBatchMax = 32768

// decodedPumpReads bounds the event reads one owner iteration makes.
const decodedPumpReads = 8

// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) pumpDecodedEvents() {
	if d.decodedSession == nil || d.decodedLines {
		return // in line mode the worker keeps the events (ADR-STREAM-LINES-IN-WORKER)
	}
	if d.decodedError != nil {
		// A failed transcript must not destroy an independently retained record
		// through a stale-health restart. Probe hardware without consuming events;
		// successful reads feed the existing bus heartbeat, failed reads do not.
		_, _ = d.h.memory().SupportsDecodedCapture()
		return
	}
	if len(d.decodedScratch) != sramcapture.DecodedBatchMax {
		d.decodedScratch = make([]sramcapture.DecodedEvent, sramcapture.DecodedBatchMax)
	}
	now := time.Now()
	s := &d.decodedService
	if !s.last.IsZero() {
		gap := now.Sub(s.last)
		s.maxGap = max(s.maxGap, gap)
		if gap >= 8*time.Millisecond {
			if s.slow < uint64(len(s.gaps)) {
				s.gaps[s.slow].At, s.gaps[s.slow].Gap = now.Sub(s.started), gap
			}
			s.slow++
		}
	}
	s.last = now
	// A full read means more is waiting: keep reading, a bounded number of
	// times, so one owner iteration can move several IPC payloads. One read
	// an iteration caps the transcript near 50 k events/s, and USB-LS and
	// FlexRay streams overflow the worker's queue.
	for range decodedPumpReads {
		batch, err := d.h.memory().ReadDecodedEventsInto(d.decodedSession.Identity.Epoch, d.decodedScratch)
		s.maxRead = max(s.maxRead, time.Since(now))
		s.reads++
		s.events += uint64(len(batch))
		if err != nil {
			d.decodedError = err
			return
		}
		if d.decodedError = d.bufferDecoded(batch); d.decodedError != nil {
			return
		}
		if len(batch) < len(d.decodedScratch) {
			return
		}
	}
}

// bufferDecoded appends a read's events to the host buffer, in sequence:
// an error ends the transcript (a sequence gap, the buffer full). The
// buffer is checked once a read and its slot found by wrapping, not a
// modulo an event.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) bufferDecoded(batch []sramcapture.DecodedEvent) error {
	if len(batch) == 0 {
		return nil
	}
	if len(d.decodedBuffer) != decodedBufferLimit {
		d.decodedBuffer = make([]sramcapture.DecodedEvent, decodedBufferLimit)
	}
	s := &d.decodedService
	// The events that go in: those in sequence, while the buffer has room;
	// the losses counted as the loop met them (it stopped at the first that
	// could not go in, after counting its loss).
	var err error
	k := 0
	for ; k < len(batch); k++ {
		v := &batch[k]
		if v.Kind == sramcapture.EventLoss {
			s.lost += uint64(v.Count)
		}
		if v.Sequence != d.decodedSequence+uint32(k) {
			err = errors.New("decoded event sequence discontinuity")
			break
		}
		if d.decodedCount+k >= decodedBufferLimit {
			err = errors.New("decoded host buffer overflow; transcript incomplete")
			break
		}
	}
	// copied in at most two stretches: to the ring's end, then from its start
	at := (d.decodedHead + d.decodedCount) % decodedBufferLimit
	n := copy(d.decodedBuffer[at:], batch[:k])
	copy(d.decodedBuffer, batch[n:k])
	d.decodedSequence += uint32(k)
	d.decodedCount += k
	if k > 0 { // the mark moves with an event taken in
		s.maxBuffered = max(s.maxBuffered, d.decodedCount)
	}
	return err
}

// BeginDecodedCapture pauses normal frame rearming and keeps all register work
// on the acquisition owner. It never changes the analog front-end settings.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) BeginDecodedCapture(ctx context.Context, cfg sramcapture.Config) (sramcapture.RecordIdentity, error) {
	return d.beginDecodedCapture(ctx, cfg, 0, true)
}

// beginDecodedCapture starts a session; web marks a web page's (the scope's
// keys end it, and it ends itself when abandoned), not the scope's stream.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) beginDecodedCapture(ctx context.Context, cfg sramcapture.Config, lineProto int, web bool) (sramcapture.RecordIdentity, error) {
	value, err := d.decodedCall(ctx, func() (any, error) {
		d.decodedStarting = time.Time{} // the session (or its failure) takes over the image
		if d.soft != nil {
			return nil, errors.New("a decoded stream is already running")
		}
		if err := d.decodedTaken(web); err != nil {
			return nil, err
		}
		if cfg.Packet.Enabled && cfg.Packet.Protocol == sramcapture.PacketARINC && cfg.Packet.Aux == 0 {
			if cfg.Packet.Aux = d.h.arincAux(); cfg.Packet.Aux == 0 {
				return nil, errors.New("ARINC 429 levels are not known yet: show the signal with ARINC decoding and a manual threshold first")
			}
		}
		// a configuration the fabric refuses is refused before the image
		// load and the halt: what streams now keeps streaming
		check := cfg
		check.DecodedEvents = true
		if err := check.Check(); err != nil {
			return nil, err
		}
		if cfg.Symbol.Enabled {
			return nil, errors.New("the image that triggers this protocol cannot stream its decoded events")
		}
		d.h.soundYield()
		// Only UART, I2C and SPI stream from the general image; a request for
		// a receiver on the samples streams from the fast image: SPI framed
		// tighter than its level receiver takes (a clock past about 20 MHz)
		// or with a data delay, a UART bit time in sixteenths of a
		// nanosecond, I2C asking for it.
		want := ""
		switch {
		case cfg.Packet.Enabled:
			want = acq.ImagePacket
		case cfg.MIL1553.Enabled || cfg.SENT.Enabled || cfg.USBLS.Enabled:
			want = acq.ImageFrame
		case cfg.Manchester.Enabled:
			want = acq.ImageLine
		case (cfg.SPI.Enabled && (cfg.SPI.GapTicks < 9 || cfg.SPI.Delay != 0)) || (cfg.UART.Enabled && cfg.UART.Period16 != 0) || (cfg.I2C.Enabled && cfg.I2C.Samples):
			want = acq.ImageFast
		}
		if d.h.hasImages() && d.h.hasImage(want) && want != d.h.protocolImage() {
			if err := d.h.memory().Halt(ctx); err != nil {
				return nil, err
			}
			d.h.loadImage(want)
		}
		supported, err := d.h.memory().SupportsDecodedCapture()
		if err != nil {
			return nil, err
		}
		if !supported {
			return nil, errors.New("loaded image lacks decoded capture support")
		}
		d.h.setRunning(false) // keeps the status in step: streaming is not RUN
		if err := d.h.memory().Halt(ctx); err != nil {
			return nil, err
		}
		if _, err := d.h.memory().EnableDecodedEvents(false); err != nil {
			return nil, err
		}
		d.decodedSession = nil
		d.decodedBuffer = make([]sramcapture.DecodedEvent, decodedBufferLimit)
		d.decodedHead, d.decodedCount = 0, 0
		if len(d.decodedScratch) != sramcapture.DecodedBatchMax {
			d.decodedScratch = make([]sramcapture.DecodedEvent, sramcapture.DecodedBatchMax)
		}
		d.decodedError = nil
		d.decodedSequence = 0
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		d.decodedService = decodedServiceStats{started: time.Now(), pauseTotal: mem.PauseTotalNs}
		if dev, ok := d.h.bus().(*bus.Dev); ok && d.decodedDeadline == nil {
			d.decodedDeadline, err = dev.StartDecodedDeadline()
			if err != nil {
				return nil, err
			}
		}
		session, err := d.h.memory().StartDecodedCapture(ctx, cfg)
		// Select the transcript consumer before the owner can pump events. A
		// second owner call would strand the first batch in decodedBuffer.
		lines := false
		if err == nil && lineProto != 0 {
			lines, err = d.h.memory().SetDecodedLineMode(lineProto)
		}
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			haltErr := d.h.memory().Halt(cleanup)
			_, streamErr := d.h.memory().EnableDecodedEvents(false)
			return nil, errors.Join(err, haltErr, streamErr, d.closeDecodedDeadline())
		}
		d.decodedSession = session
		d.decodedCfg = cfg
		d.decodedLines = lines
		d.decodedWeb, d.decodedContact = web, time.Now()
		d.mu.Lock()
		d.stats.DecodedWeb = web
		d.mu.Unlock()
		return session.Identity, nil
	})
	return core.OwnerResult[sramcapture.RecordIdentity](value, err)
}

// decodedTaken refuses a web page's decoded capture while the scope's own
// stream runs and is read: starting would take the fabric from under its
// reader ("identity no longer active") and leave the scope with a stream
// nobody follows. A web session, or the scope's stream gone unread (its
// reader failed), gives way; the scope's own stream always ends its old
// session first.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) decodedTaken(web bool) error {
	if !web || d.decodedSession == nil || d.decodedWeb || time.Since(d.decodedContact) >= decodedOrphanAfter {
		return nil
	}
	return errors.New("the scope is streaming this bus (BUS Stream): end it there first")
}

// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) decodedIdentity(id sramcapture.RecordIdentity) error {
	if d.decodedSession == nil || d.decodedSession.Identity != id {
		return errors.New("decoded capture identity is no longer active")
	}
	return nil
}

// ReadDecodedBatch returns independent event values, never a shared buffer.
// One consumer owns the transcript; sequence numbers detect competing readers.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) ReadDecodedBatch(ctx context.Context, id sramcapture.RecordIdentity) ([]sramcapture.DecodedEvent, error) {
	out := make([]sramcapture.DecodedEvent, 4096)
	n, err := d.ReadDecodedBatchInto(ctx, id, out)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

// ReadDecodedBatchInto copies a bounded batch into client-owned reusable memory.
// Allocation and serialization stay outside the acquisition owner's goroutine.
// Cancellation waits for an in-flight copy before the caller may reuse dst.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) ReadDecodedBatchInto(ctx context.Context, id sramcapture.RecordIdentity, dst []sramcapture.DecodedEvent) (int, error) {
	if len(dst) < 1 || len(dst) > decodedBatchMax {
		return 0, fmt.Errorf("decoded batch storage must hold 1..%d events", decodedBatchMax)
	}
	var guard sync.Mutex
	n := 0
	_, err := d.decodedCall(ctx, func() (any, error) {
		guard.Lock()
		defer guard.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d.softFor(id) {
			var err error
			n, err = d.softRead(id, dst)
			return nil, err
		}
		if err := d.decodedIdentity(id); err != nil {
			return nil, err
		}
		if d.decodedError != nil {
			return nil, d.decodedError
		}
		now := time.Now()
		d.decodedContact = now
		if !d.decodedService.lastConsumer.IsZero() {
			d.decodedService.maxConsumerGap = max(d.decodedService.maxConsumerGap, now.Sub(d.decodedService.lastConsumer))
		}
		d.decodedService.lastConsumer = now
		n = min(len(dst), d.decodedCount)
		first := min(n, decodedBufferLimit-d.decodedHead)
		copy(dst[:first], d.decodedBuffer[d.decodedHead:d.decodedHead+first])
		copy(dst[first:n], d.decodedBuffer[:n-first])
		d.decodedHead = (d.decodedHead + n) % decodedBufferLimit
		d.decodedCount -= n
		return nil, nil
	})
	guard.Lock()
	defer guard.Unlock()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// DecodedCaptureStatus reads the status of the retained record id names,
// failing when that record is no longer the one the capture holds.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) DecodedCaptureStatus(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, error) {
	value, err := d.decodedCall(ctx, func() (any, error) {
		if d.softFor(id) {
			return d.softStatus(id)
		}
		if err := d.decodedIdentity(id); err != nil {
			return nil, err
		}
		d.decodedContact = time.Now()
		m, err := d.h.memory().Status()
		if err != nil {
			return nil, err
		}
		if !m.HasRecordIdentity || m.RecordID != id.Record || m.EventEpoch != id.Epoch {
			return nil, errors.New("retained record identity changed")
		}
		return m, nil
	})
	return core.OwnerResult[sramcapture.Metadata](value, err)
}

// RecallDecodedRecord stages raw data before returning to the network layer.
// A stalled client cannot own the hardware or cause SRAM to be rearmed.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-033
func (d *DecodedStreams) RecallDecodedRecord(ctx context.Context, id sramcapture.RecordIdentity, offset, count uint32) ([]byte, error) {
	if uint64(offset)+uint64(count) > uint64(sramcapture.Words) {
		return nil, errors.New("decoded recall exceeds SRAM capacity")
	}
	out := make([]byte, int(count)*4)
	// Queue each chunk separately: stream readers and panel requests can run
	// between transfers. Every chunk verifies identity under the capture lock;
	// replacement or cancellation discards the staged response in its entirety.
	for done := uint32(0); done < count || (count == 0 && done == 0); {
		// The general image reserves 16 of its 4096 read-buffer words for
		// SRAM warm-up. Fit one physical read per owner request.
		n := min(count-done, uint32(4080))
		position := offset + done
		// Each checked chunk writes directly into its own bounded window. The
		// response remains private until every chunk succeeds.
		chunk := bytes.NewBuffer(out[done*4 : done*4 : (done+n)*4])
		_, err := d.decodedCall(ctx, func() (any, error) {
			if err := d.decodedIdentity(id); err != nil {
				return nil, err
			}
			d.decodedContact = time.Now()
			d.pumpDecodedEvents()
			if _, err := d.decodedSession.Recall(ctx, position, n, chunk); err != nil {
				return nil, err
			}
			d.pumpDecodedEvents()
			if chunk.Len() != int(n)*4 {
				return nil, errors.New("decoded recall returned a short chunk")
			}
			return nil, nil
		})
		if err != nil {
			return nil, err
		}
		if count == 0 {
			break
		}
		done += n
	}
	return out, nil
}

// EndDecodedCapture leaves normal acquisition stopped until the operator runs it.
// Halting preserves the SRAM record; disabling streaming only flushes events.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) EndDecodedCapture(ctx context.Context, id sramcapture.RecordIdentity) error {
	_, err := d.decodedCall(ctx, func() (any, error) {
		if d.softFor(id) {
			return nil, d.softEnd(ctx, id)
		}
		if err := d.decodedIdentity(id); err != nil {
			return nil, err
		}
		return nil, d.endDecoded(ctx, false)
	})
	return err
}

// EndWebDecodedCapture ends a web page's decoded capture, if one is open, for
// the scope's own run-control keys: they always get the acquisition back. It
// reports whether there was one. The scope's own stream is left alone (its
// keys stop and restart it).
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) EndWebDecodedCapture(ctx context.Context) (bool, error) {
	v, err := d.decodedCall(ctx, func() (any, error) {
		if d.decodedSession == nil || !d.decodedWeb {
			return false, nil
		}
		d.h.logf("engine: the scope's keys end the web page's decoded capture")
		return true, d.endDecoded(ctx, false)
	})
	return core.OwnerResult[bool](v, err)
}

// expireDecoded ends a web page's session no client has touched for
// decodedAbandonAfter, on the acquisition owner (serviceCommands).
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) expireDecoded() {
	if d.decodedSession == nil {
		return
	}
	idle := time.Since(d.decodedContact)
	if d.decodedWeb {
		if idle < decodedAbandonAfter {
			return
		}
		d.h.logf("engine: no client on the web page's decoded capture for %s - ended", decodedAbandonAfter)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := d.endDecoded(ctx, true); err != nil {
			d.h.logf("engine: ending the abandoned decoded capture: %v", err)
		}
		return
	}
	// The scope's own stream: its reader reads all the time it runs, so one
	// silent past decodedOrphanAfter is gone (a reader failed and left the
	// session open): end it and capture again, the fabric no longer held.
	if idle < decodedOrphanAfter {
		return
	}
	d.h.logf("engine: the scope's decoded stream has had no reader for %s - ended, capturing again", decodedOrphanAfter)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.endDecoded(ctx, true); err != nil {
		d.h.logf("engine: ending the orphaned decoded capture: %v", err)
	}
	d.h.setRunning(true)
}

// endDecoded tears the open session down on the acquisition owner; force
// clears it even when the fabric does not answer (an abandoned session must
// not keep the acquisition). Normal acquisition stays stopped until RUN.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) endDecoded(ctx context.Context, force bool) error {
	haltErr := d.h.memory().Halt(ctx)
	var streamErr error
	if haltErr == nil {
		_, streamErr = d.h.memory().EnableDecodedEvents(false)
	}
	if err := errors.Join(haltErr, streamErr); err != nil && !force {
		return err
	}
	id := d.decodedSession.Identity
	d.decodedSession = nil
	d.decodedLines = false
	d.decodedBuffer = nil
	d.decodedHead, d.decodedCount = 0, 0
	d.decodedWeb = false
	d.mu.Lock()
	d.stats.DecodedWeb = false
	d.mu.Unlock()
	if err := d.closeDecodedDeadline(); err != nil {
		return err
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s := &d.decodedService
	d.h.logf("decoded service: epoch=%d reads=%d events=%d lost=%d max_gap=%s max_read=%s gc_pause=%s slow_gaps=%d first=%v buffered_peak=%d consumer_gap=%s requests=%s controls=%s pause=%s", id.Epoch, s.reads, s.events, s.lost, s.maxGap, s.maxRead, time.Duration(mem.PauseTotalNs-s.pauseTotal), s.slow, s.gaps[:min(s.slow, uint64(len(s.gaps)))], s.maxBuffered, s.maxConsumerGap, s.maxRequests, s.maxControl, s.maxPause) //nolint:gosec // G115: GC pause nanoseconds of one session, far below 2^63
	d.h.setRunning(false)                                                                                                                                                                                                                                                                                                                                                                                                            // keeps the status in step: streaming is not RUN
	return nil
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (d *DecodedStreams) closeDecodedDeadline() error {
	if d.decodedDeadline == nil {
		return nil
	}
	if err := d.decodedDeadline.Close(); err != nil {
		return err
	}
	d.decodedDeadline = nil
	return nil
}

// decodedIdlePause is the wait between the owner loop's passes while a
// decoded stream's worker builds its lines: a reader's request waits at most
// this long.
const decodedIdlePause = time.Millisecond

// decodedIdle is the owner loop's pass while a decoded capture runs: the
// events are pumped elsewhere, so it waits on the FIFO service, or a little
// between passes when the worker builds the lines (a pass with nothing to
// do spun a whole core).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (d *DecodedStreams) decodedIdle() {
	if d.decodedError != nil {
		d.h.clock().Sleep(20 * time.Millisecond)
	}
	if d.decodedDeadline != nil {
		stage := time.Now()
		if err := d.decodedDeadline.Pause(); err != nil {
			d.decodedError = err
		}
		d.decodedService.maxPause = max(d.decodedService.maxPause, time.Since(stage))
	} else if d.decodedLines {
		d.h.clock().Sleep(decodedIdlePause)
	}
}
