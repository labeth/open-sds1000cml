// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"open-sds/app/internal/bus"
	"open-sds/app/internal/sramcapture"
	"runtime"
	"sync"
	"time"
)

type decodedReply struct {
	value any
	err   error
}

const decodedBufferLimit = 131072 // 5 MiB; finite client-backpressure allowance

// Fixed storage keeps service-gap diagnostics out of the allocator while the
// FIFO is live. Report only after disabling the producer.
type decodedServiceStats struct {
	started, last                              time.Time
	lastConsumer                               time.Time
	maxConsumerGap                             time.Duration
	maxBuffered                                int
	maxExec, maxRequests, maxControl, maxPause time.Duration
	maxGap, maxRead                            time.Duration
	reads, events, lost, slow                  uint64
	pauseTotal                                 uint64
	gaps                                       [16]struct{ At, Gap time.Duration }
}

type decodedRequest struct {
	ctx  context.Context
	run  func() (any, error)
	done chan decodedReply
}

// decodedCall stages SRAM work on the existing acquisition owner. Cancellation
// of a queued request prevents it from touching hardware when serviced later.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (e *Engine) decodedCall(ctx context.Context, run func() (any, error)) (any, error) {
	if e.sram == nil {
		return nil, fmt.Errorf("decoded capture requires the general SRAM image")
	}
	req := decodedRequest{ctx: ctx, run: run, done: make(chan decodedReply, 1)}
	select {
	case e.decodedRequests <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrExecStopped
	}
	select {
	case r := <-req.done:
		return r.value, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrExecStopped
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (e *Engine) serviceDecodedRequests() {
	for i := 0; i < 8; i++ {
		select {
		case req := <-e.decodedRequests:
			if err := req.ctx.Err(); err != nil {
				req.done <- decodedReply{err: err}
				continue
			}
			value, err := req.run()
			req.done <- decodedReply{value, err}
			e.beatN.Add(1)
		default:
			return
		}
	}
}

// pumpDecodedEvents never waits for clients. FIFO loss markers pass through;
// host buffer exhaustion or sequence discontinuity terminates the transcript
// explicitly while the trigger and retained SRAM record remain independent.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) pumpDecodedEvents() {
	if e.decodedSession == nil {
		return
	}
	if e.decodedError != nil {
		// A failed transcript must not destroy an independently retained record
		// through a stale-health restart. Probe hardware without consuming events;
		// successful reads feed the existing bus heartbeat, failed reads do not.
		_, _ = e.sram.SupportsDecodedCapture()
		return
	}
	if len(e.decodedScratch) != 512 {
		e.decodedScratch = make([]sramcapture.DecodedEvent, 512)
	}
	now := time.Now()
	s := &e.decodedService
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
	batch, err := e.sram.ReadDecodedEventsInto(e.decodedSession.Identity.Epoch, e.decodedScratch)
	s.maxRead = max(s.maxRead, time.Since(now))
	s.reads++
	s.events += uint64(len(batch))
	if err != nil {
		e.decodedError = err
		return
	}
	for _, v := range batch {
		if v.Kind == sramcapture.EventLoss {
			s.lost += uint64(v.Count)
		}
		if v.Sequence != e.decodedSequence {
			e.decodedError = fmt.Errorf("decoded event sequence discontinuity")
			return
		}
		if e.decodedCount >= decodedBufferLimit {
			e.decodedError = fmt.Errorf("decoded host buffer overflow; transcript incomplete")
			return
		}
		e.decodedSequence++
		if len(e.decodedBuffer) != decodedBufferLimit {
			e.decodedBuffer = make([]sramcapture.DecodedEvent, decodedBufferLimit)
		}
		e.decodedBuffer[(e.decodedHead+e.decodedCount)%decodedBufferLimit] = v
		e.decodedCount++
		s.maxBuffered = max(s.maxBuffered, e.decodedCount)
	}
}

// BeginDecodedCapture pauses normal frame rearming and keeps all register work
// on the acquisition owner. It never changes the analog front-end settings.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) BeginDecodedCapture(ctx context.Context, cfg sramcapture.Config) (sramcapture.RecordIdentity, error) {
	value, err := e.decodedCall(ctx, func() (any, error) {
		supported, err := e.sram.SupportsDecodedCapture()
		if err != nil {
			return nil, err
		}
		if !supported {
			return nil, fmt.Errorf("loaded image lacks decoded capture support")
		}
		e.running.Store(false)
		if err := e.sram.Halt(ctx); err != nil {
			return nil, err
		}
		if _, err := e.sram.EnableDecodedEvents(false); err != nil {
			return nil, err
		}
		e.decodedSession = nil
		e.decodedBuffer = make([]sramcapture.DecodedEvent, decodedBufferLimit)
		e.decodedHead, e.decodedCount = 0, 0
		if len(e.decodedScratch) != 512 {
			e.decodedScratch = make([]sramcapture.DecodedEvent, 512)
		}
		e.decodedError = nil
		e.decodedSequence = 0
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		e.decodedService = decodedServiceStats{started: time.Now(), pauseTotal: mem.PauseTotalNs}
		if dev, ok := e.b.(*bus.Dev); ok && e.decodedDeadline == nil {
			e.decodedDeadline, err = dev.StartDecodedDeadline()
			if err != nil {
				return nil, err
			}
		}
		session, err := e.sram.StartDecodedCapture(ctx, cfg)
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			haltErr := e.sram.Halt(cleanup)
			_, streamErr := e.sram.EnableDecodedEvents(false)
			return nil, errors.Join(err, haltErr, streamErr, e.closeDecodedDeadline())
		}
		e.decodedSession = session
		return session.Identity, nil
	})
	if err != nil {
		return sramcapture.RecordIdentity{}, err
	}
	return value.(sramcapture.RecordIdentity), nil
}

// TRLC-LINKS: REQ-SDS-013
func (e *Engine) decodedIdentity(id sramcapture.RecordIdentity) error {
	if e.decodedSession == nil || e.decodedSession.Identity != id {
		return fmt.Errorf("decoded capture identity is no longer active")
	}
	return nil
}

// ReadDecodedBatch returns independent event values, never a shared buffer.
// One consumer owns the transcript; sequence numbers detect competing readers.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) ReadDecodedBatch(ctx context.Context, id sramcapture.RecordIdentity) ([]sramcapture.DecodedEvent, error) {
	out := make([]sramcapture.DecodedEvent, 4096)
	n, err := e.ReadDecodedBatchInto(ctx, id, out)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

// ReadDecodedBatchInto copies a bounded batch into client-owned reusable memory.
// Allocation and serialization stay outside the acquisition owner's goroutine.
// Cancellation waits for an in-flight copy before the caller may reuse dst.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) ReadDecodedBatchInto(ctx context.Context, id sramcapture.RecordIdentity, dst []sramcapture.DecodedEvent) (int, error) {
	if len(dst) < 1 || len(dst) > 4096 {
		return 0, fmt.Errorf("decoded batch storage must hold 1..4096 events")
	}
	var guard sync.Mutex
	n := 0
	_, err := e.decodedCall(ctx, func() (any, error) {
		guard.Lock()
		defer guard.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := e.decodedIdentity(id); err != nil {
			return nil, err
		}
		if e.decodedError != nil {
			return nil, e.decodedError
		}
		now := time.Now()
		if !e.decodedService.lastConsumer.IsZero() {
			e.decodedService.maxConsumerGap = max(e.decodedService.maxConsumerGap, now.Sub(e.decodedService.lastConsumer))
		}
		e.decodedService.lastConsumer = now
		n = min(len(dst), e.decodedCount)
		first := min(n, decodedBufferLimit-e.decodedHead)
		copy(dst[:first], e.decodedBuffer[e.decodedHead:e.decodedHead+first])
		copy(dst[first:n], e.decodedBuffer[:n-first])
		e.decodedHead = (e.decodedHead + n) % decodedBufferLimit
		e.decodedCount -= n
		return nil, nil
	})
	guard.Lock()
	defer guard.Unlock()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// TRLC-LINKS: REQ-SDS-013
func (e *Engine) DecodedCaptureStatus(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, error) {
	value, err := e.decodedCall(ctx, func() (any, error) {
		if err := e.decodedIdentity(id); err != nil {
			return nil, err
		}
		m, err := e.sram.Status()
		if err != nil {
			return nil, err
		}
		if !m.HasRecordIdentity || m.RecordID != id.Record || m.EventEpoch != id.Epoch {
			return nil, fmt.Errorf("retained record identity changed")
		}
		return m, nil
	})
	if err != nil {
		return sramcapture.Metadata{}, err
	}
	return value.(sramcapture.Metadata), nil
}

// RecallDecodedRecord stages raw data before returning to the network layer.
// A stalled client cannot own the hardware or cause SRAM to be rearmed.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-033
func (e *Engine) RecallDecodedRecord(ctx context.Context, id sramcapture.RecordIdentity, offset, count uint32) ([]byte, error) {
	if uint64(offset)+uint64(count) > uint64(sramcapture.Words) {
		return nil, fmt.Errorf("decoded recall exceeds SRAM capacity")
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
		_, err := e.decodedCall(ctx, func() (any, error) {
			if err := e.decodedIdentity(id); err != nil {
				return nil, err
			}
			e.pumpDecodedEvents()
			if _, err := e.decodedSession.Recall(ctx, position, n, chunk); err != nil {
				return nil, err
			}
			e.pumpDecodedEvents()
			if chunk.Len() != int(n)*4 {
				return nil, fmt.Errorf("decoded recall returned a short chunk")
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
func (e *Engine) EndDecodedCapture(ctx context.Context, id sramcapture.RecordIdentity) error {
	_, err := e.decodedCall(ctx, func() (any, error) {
		if err := e.decodedIdentity(id); err != nil {
			return nil, err
		}
		if err := e.sram.Halt(ctx); err != nil {
			return nil, err
		}
		if _, err := e.sram.EnableDecodedEvents(false); err != nil {
			return nil, err
		}
		e.decodedSession = nil
		e.decodedBuffer = nil
		e.decodedHead, e.decodedCount = 0, 0
		if err := e.closeDecodedDeadline(); err != nil {
			return nil, err
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		s := &e.decodedService
		e.logf("decoded service: epoch=%d reads=%d events=%d lost=%d max_gap=%s max_read=%s gc_pause=%s slow_gaps=%d first=%v buffered_peak=%d consumer_gap=%s exec=%s requests=%s controls=%s pause=%s", id.Epoch, s.reads, s.events, s.lost, s.maxGap, s.maxRead, time.Duration(mem.PauseTotalNs-s.pauseTotal), s.slow, s.gaps[:min(s.slow, uint64(len(s.gaps)))], s.maxBuffered, s.maxConsumerGap, s.maxExec, s.maxRequests, s.maxControl, s.maxPause)
		e.running.Store(false)
		return nil, nil
	})
	return err
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (e *Engine) closeDecodedDeadline() error {
	if e.decodedDeadline == nil {
		return nil
	}
	if err := e.decodedDeadline.Close(); err != nil {
		return err
	}
	e.decodedDeadline = nil
	return nil
}
