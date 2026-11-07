// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The SDS1000CML+ streams by draining the stream image's SRAM drum on the
// owner: there is no transfer goroutine, so Lease drains the next bank on
// the goroutine that asks (the owner) into the board's block buffer.

// streamTimeout bounds one bank's drain.
const streamTimeout = 2 * time.Second

type streamerFacet Board

// streamState is a running hal.Streamer stream.
type streamState struct {
	mu       sync.Mutex
	running  bool
	spec     hal.StreamSpec
	expected uint64
	buf      bytes.Buffer
	runs     []byte
	faults   hal.StreamFaults
}

// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) streamOf() *streamState { return &s.stream }

// Kinds is what the loaded image streams: 8-bit codes of both channels on
// the stream images.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Kinds() []hal.Payload {
	b := (*Board)(s)
	if b.imageLoaded != ImageStream && b.imageLoaded != ImageBits {
		return nil
	}
	return []hal.Payload{hal.PayloadCodes}
}

// Start starts a continuous stream of the staged plan's codes. OWNER.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Start(spec hal.StreamSpec) error {
	b := (*Board)(s)
	if spec.Payload != hal.PayloadCodes {
		return hal.Refuse("stream payload", "the drum streams sample codes; decoded and digital streams run through the engine's stream view")
	}
	if len(spec.Channels) != 2 {
		return hal.Refuse("stream channels", "the drum streams C1 and C2 together")
	}
	if len(s.Kinds()) == 0 {
		return hal.Refuse("stream", "the stream image is not loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), armTimeout)
	defer cancel()
	if err := b.mem.StartStream(ctx, b.Staged(), true, false, sramcapture.StreamPace{}); err != nil {
		return err
	}
	st := s.streamOf()
	st.mu.Lock()
	st.running, st.spec, st.expected, st.faults = true, spec, 0, hal.StreamFaults{}
	st.mu.Unlock()
	b.state = hal.Armed
	return nil
}

// Lease drains the next bank and hands it out as channel-major runs of
// 8-bit codes. OWNER on this board (the drain is a bus access).
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Lease(ctx context.Context) (hal.StreamBlock, error) {
	b := (*Board)(s)
	st := s.streamOf()
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.running {
		return hal.StreamBlock{}, hal.Refuse("stream", "no stream is running")
	}
	st.buf.Reset()
	dctx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	blk, err := b.mem.DrainStream(dctx, st.expected, &st.buf)
	if err != nil {
		st.faults.Stalls++
		return hal.StreamBlock{}, err
	}
	if blk.FirstWord != st.expected && st.expected != 0 {
		st.faults.SeqJumps++
	}
	st.expected = blk.FirstWord + uint64(blk.Words)
	raw := st.buf.Bytes()
	n := len(raw) / 2 // samples a channel: words of (C1, C2, C1, C2)
	st.runs = append(st.runs[:0], make([]byte, 2*n)...)
	for i := 0; i+1 < len(raw); i += 2 {
		st.runs[i/2], st.runs[n+i/2] = raw[i], raw[i+1]
	}
	f := sample.CodeFormat{Bits: 8, Storage: sample.U8, Max: 255, Mid: 128}
	return hal.StreamBlock{Payload: hal.PayloadCodes, Formats: []hal.CodeFormat{f, f}, Bytes: st.runs, First: blk.FirstWord * 2}, nil
}

// Release returns a block; the board's one buffer is reused by the next
// Lease.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Release(hal.StreamBlock) {}

// Stop stops the stream. OWNER.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Stop() error {
	b := (*Board)(s)
	st := s.streamOf()
	st.mu.Lock()
	st.running = false
	st.mu.Unlock()
	b.state = hal.Idle
	ctx, cancel := context.WithTimeout(context.Background(), haltTimeout)
	defer cancel()
	return b.mem.StopStream(ctx)
}

// Faults is the stream's losses since Start.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Faults() hal.StreamFaults {
	st := s.streamOf()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.faults
}

// Freeze stops the history ring the stream image keeps beside the stream:
// the ring becomes the record Readout reads. OWNER.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Freeze(uint64) (hal.RecordID, error) {
	b := (*Board)(s)
	if !b.mem.StreamRingSupported() {
		return hal.RecordID{}, hal.Refuse("history ring", "the loaded image keeps no ring")
	}
	ctx, cancel := context.WithTimeout(context.Background(), armTimeout)
	defer cancel()
	m, _, err := b.mem.FreezeRing(ctx)
	if err != nil {
		return hal.RecordID{}, err
	}
	b.SetRecord(m)
	return recordID(m), nil
}

var _ hal.RingRecorder = (*streamerFacet)(nil)

type stackerFacet Board

// Stack is refused: the stacking image accumulates against a template the
// request carries (a level, a window, a qualifier), which the engine's FPGA
// stack job builds; hal.Stacker's record count alone cannot ask for it.
// TRLC-LINKS: REQ-SDS-035
func (s *stackerFacet) Stack(int) error {
	return hal.Refuse("hardware stacking", "the stacking image needs its template request: the FPGA stack job runs it")
}
