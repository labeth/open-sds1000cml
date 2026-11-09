// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The SDS1000CML+ streams from the stream image's SRAM drum: the record
// memory's words published bank by bank as the record writes them. A
// decimated stream's word is both channels' averages over the bucket as
// Q8.8 precision words (C1 then C2, little-endian). With a bus worker (the
// instrument) the worker drains the banks into a ring at its FIFO service,
// so the app's scheduling cannot lose words; without one the owner drains
// them, scheduled as the decoded stream is. There is no transfer goroutine
// of the board's own: Lease takes the next words on the goroutine that asks
// (the owner).

// streamTimeout bounds one bank's drain.
const streamTimeout = 2 * time.Second

// streamIdle is the pause between looks while no words are there yet.
const streamIdle = 5 * time.Millisecond

// soundLog2 is the audio stream's decimation: 2^13 samples a word, about
// 61 kSa/s on each channel.
const soundLog2 = 13

// FeatureSound is the hal.Feature an audio stream's request names: the
// planner answers it with the audio stream's rate.
const FeatureSound = "sound"

// q88Format is a decimated stream word's format: a Q8.8 precision word on
// the 8-bit converter's scale.
var q88Format = sample.CodeFormat{Bits: 8, Storage: sample.U16, Frac: 8, Max: 0xFFFF, Mid: 0x8000}

type streamerFacet Board

// streamPumper is the bus worker's stream-bank pump (bus.Dev with a worker).
type streamPumper interface {
	SetIsolatedStreamPump(bool) (bool, error)
	ReadIsolatedStream([]byte) (uint64, int, bool, error)
}

// streamState is a running hal.Streamer stream.
type streamState struct {
	mu       sync.Mutex
	running  bool
	spec     hal.StreamSpec
	sampleS  float64 // between a channel's delivered samples
	expected uint64  // stream words taken: the next block's first item
	buf      bytes.Buffer
	raw      []byte
	words    []uint16
	faults   hal.StreamFaults
	pump     streamPumper         // the worker's pump while it drains the banks
	deadline *bus.DecodedDeadline // the owner's drain scheduling without one
	// reload is set by a loss: after a fault the stream image can fault every
	// new stream at its first word until it is loaded again (seen on the
	// instrument), so the next Start loads it first.
	reload bool

	markMu   sync.Mutex
	markWant hal.MarkSpec // what SetMarks asked
	marksOn  bool         // the image marks crossings on this stream
	applied  hal.MarkSpec // the marks the image makes now
	markSeen [2]uint16    // the image's rise and fall counts last read
	marks    []hal.Mark
}

// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) streamOf() *streamState { return &s.stream }

// Kinds is what the board streams: averaged codes of both channels, on the
// stream image (Start loads it).
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Kinds() []hal.Payload {
	if !(*Board)(s).HasImage(ImageStream) {
		return nil
	}
	return []hal.Payload{hal.PayloadCodes}
}

// Start streams the staged plan's decimated words: the stream image is
// loaded first when another is in use or a loss left it faulted, the marks
// SetMarks asked are made where the image makes them, and a channel's
// samples arrive at the staged decimation. The drum streams C1 and C2
// together; spec.Channels picks which the blocks carry. OWNER, LONG when
// the image loads.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-265
func (s *streamerFacet) Start(spec hal.StreamSpec) error {
	b := (*Board)(s)
	st := s.streamOf()
	if spec.Payload != hal.PayloadCodes {
		return hal.Refuse("stream payload", "the drum streams sample codes; decoded and digital streams run through the engine's stream view")
	}
	if len(spec.Channels) == 0 || len(spec.Channels) > 2 || slices.ContainsFunc(spec.Channels, func(c int) bool { return c < 0 || c > 1 }) {
		return hal.Refuse("stream channels", "the drum streams C1 and C2")
	}
	if len(s.Kinds()) == 0 {
		return hal.Refuse("stream", "this build has no stream image")
	}
	if st.running {
		return hal.Refuse("stream", "a stream is running")
	}
	cfg := b.Staged()
	if cfg.DecimationLog2 < 8 {
		return hal.Refuse("stream rate", "the fabric streams decimated records only (2^8 samples a word or more)")
	}
	if b.imageLoaded != ImageStream || st.reload {
		st.reload = false
		if err := b.loadFresh(ImageStream); err != nil {
			return err
		}
	}
	st.markMu.Lock()
	mc := b.resolveMarks(st.markWant)
	st.markMu.Unlock()
	marks := b.mem.StreamMarksSupported()
	scfg := sramcapture.Config{
		Source: sramcapture.ADC, DecimationLog2: cfg.DecimationLog2, PreWords: sramcapture.Words - 17, PostWords: 17,
		TriggerChannel: uint8(mc.Channel & 1), TriggerLevel: uint8(min(mc.Level, 255)), TriggerHysteresis: uint8(min(mc.Hyst, 255)), Normal: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.mem.StartStreamMarked(ctx, scfg, sramcapture.StreamMarkMode{Rise: marks && mc.Rise, Fall: marks && mc.Fall}); err != nil {
		return err
	}
	st.mu.Lock()
	st.running, st.spec, st.expected, st.faults = true, spec, 0, hal.StreamFaults{}
	st.sampleS = float64(uint64(1)<<cfg.DecimationLog2) / 500e6
	st.mu.Unlock()
	st.markMu.Lock()
	st.marksOn, st.applied, st.markSeen = marks, mc, [2]uint16{}
	st.markMu.Unlock()
	b.state = hal.Armed
	if p, ok := b.panel.bus.(streamPumper); ok {
		owned, err := p.SetIsolatedStreamPump(true)
		if err != nil {
			_ = s.Stop()
			return err
		}
		if owned {
			st.pump = p
		}
	}
	if dev, ok := b.panel.bus.(*bus.Dev); ok && st.pump == nil {
		if d, err := dev.StartDecodedDeadline(); err == nil {
			st.deadline = d
		}
	}
	return nil
}

// Lease takes the stream's next words as a block of channel-major Q8.8
// runs of the channels Start was asked for, with the level crossings the
// image marked since the last block. It waits for words until ctx ends. A
// failed drain ends the stream (a loss: Start loads the image again).
// OWNER on this board (the drain is a bus access).
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-265, REQ-SDS-272
func (s *streamerFacet) Lease(ctx context.Context) (hal.StreamBlock, error) {
	st := s.streamOf()
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.running {
		return hal.StreamBlock{}, hal.Refuse("stream", "no stream is running")
	}
	s.applyMarks()
	for {
		first := st.expected
		raw, err := s.nextWords()
		if err != nil {
			st.faults.Stalls++
			st.reload = true
			return hal.StreamBlock{}, err
		}
		if len(raw) >= 4 {
			return s.block(first, raw), nil
		}
		if err := ctx.Err(); err != nil {
			return hal.StreamBlock{}, err
		}
		if st.deadline != nil {
			_ = st.deadline.Pause()
		} else {
			sleepCtx(ctx, streamIdle)
		}
	}
}

// block is raw's words from stream word first on as a hal block.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-265
func (s *streamerFacet) block(first uint64, raw []byte) hal.StreamBlock {
	st := s.streamOf()
	n := len(raw) / 4
	nch := len(st.spec.Channels)
	st.words = slices.Grow(st.words[:0], n*nch)[:n*nch]
	for k, ch := range st.spec.Channels {
		run := st.words[k*n : (k+1)*n]
		for i := range run {
			j := 4*i + 2*ch
			run[i] = uint16(raw[j]) | uint16(raw[j+1])<<8
		}
	}
	formats := make([]hal.CodeFormat, nch)
	for i := range formats {
		formats[i] = q88Format
	}
	return hal.StreamBlock{
		Payload: hal.PayloadCodes, Formats: formats, Words: st.words, First: first,
		Marks: s.readMarks(), SampleS: st.sampleS,
	}
}

// nextWords is the stream's next words: from the worker's ring, or drained
// from the banks; empty when none are there yet.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) nextWords() ([]byte, error) {
	b := (*Board)(s)
	st := s.streamOf()
	if st.pump != nil {
		st.raw = slices.Grow(st.raw[:0], 65536)[:65536]
		first, n, _, err := st.pump.ReadIsolatedStream(st.raw)
		if err == nil && n > 0 && first != st.expected {
			err = fmt.Errorf("stream ordinal %d, want %d", first, st.expected)
		}
		if err != nil || n == 0 {
			return nil, err
		}
		st.expected += uint64(n / 4) //nolint:gosec // G115: a read's byte count
		return st.raw[:n], nil
	}
	st.buf.Reset()
	ctx, cancel := context.WithTimeout(context.Background(), streamTimeout)
	defer cancel()
	blk, err := b.mem.DrainStream(ctx, st.expected, &st.buf)
	if errors.Is(err, sramcapture.ErrNoStreamBlock) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if blk.FirstWord != st.expected && st.expected != 0 {
		st.faults.SeqJumps++
	}
	st.expected = blk.FirstWord + uint64(blk.Words)
	return st.buf.Bytes(), nil
}

// SetMarks asks the image to mark crossings of a level on a channel; a
// running stream takes it at its next block (the level, channel and
// directions latch while the drum runs). ANY.
// TRLC-LINKS: REQ-SDS-272
func (s *streamerFacet) SetMarks(m hal.MarkSpec) error {
	if m.Channel < 0 || m.Channel > 1 {
		return hal.Refuse("stream marks", "the image marks crossings on C1 or C2")
	}
	st := s.streamOf()
	st.markMu.Lock()
	st.markWant = m
	st.markMu.Unlock()
	return nil
}

// Marking is what the running stream marks now: none when the image marks
// nothing or no stream runs. ANY.
// TRLC-LINKS: REQ-SDS-272
func (s *streamerFacet) Marking() (hal.MarkSpec, bool) {
	st := s.streamOf()
	st.markMu.Lock()
	defer st.markMu.Unlock()
	if !st.marksOn {
		return hal.MarkSpec{}, false
	}
	return st.applied, true
}

// resolveMarks is m with a trigger's marks resolved to the staged edge
// trigger's channel, comparator code and slope.
// TRLC-LINKS: REQ-SDS-272
func (b *Board) resolveMarks(m hal.MarkSpec) hal.MarkSpec {
	if !m.FromTrigger {
		return m
	}
	t := b.StagedTrigger()
	return hal.MarkSpec{Channel: int(t.TriggerChannel & 1), Level: uint32(t.TriggerLevel), Rise: !t.Falling, Fall: t.Falling}
}

// applyMarks writes changed marks to a running stream.
// TRLC-LINKS: REQ-SDS-272
func (s *streamerFacet) applyMarks() {
	b := (*Board)(s)
	st := s.streamOf()
	st.markMu.Lock()
	defer st.markMu.Unlock()
	mc := b.resolveMarks(st.markWant)
	if !st.marksOn || mc == st.applied {
		return
	}
	if err := b.mem.SetStreamMarks(mc.Channel&1, uint8(min(mc.Level, 255)), uint8(min(mc.Hyst, 255)), sramcapture.StreamMarkMode{Rise: mc.Rise, Fall: mc.Fall}); err != nil && b.panel.writeErr != nil {
		b.panel.writeErr(err)
	}
	st.applied = mc
}

// readMarks is the image's newest marks since the last read: a rise and a
// fall counter and the stream word each newest one fell in.
// TRLC-LINKS: REQ-SDS-272
func (s *streamerFacet) readMarks() []hal.Mark {
	b := (*Board)(s)
	st := s.streamOf()
	st.markMu.Lock()
	defer st.markMu.Unlock()
	if !st.marksOn {
		return nil
	}
	m, err := b.mem.StreamMarks()
	if err != nil {
		return nil
	}
	ch := st.applied.Channel & 1
	st.marks = st.marks[:0]
	if m.RiseCount != st.markSeen[0] {
		st.marks = append(st.marks, hal.Mark{At: core.UnwrapWord(m.RiseWord, st.expected), Kind: hal.MarkRise, Channel: ch, Count: int(m.RiseCount - st.markSeen[0])})
	}
	if m.FallCount != st.markSeen[1] {
		st.marks = append(st.marks, hal.Mark{At: core.UnwrapWord(m.FallWord, st.expected), Kind: hal.MarkFall, Channel: ch, Count: int(m.FallCount - st.markSeen[1])})
	}
	st.markSeen = [2]uint16{m.RiseCount, m.FallCount}
	if len(st.marks) == 2 && st.marks[1].At < st.marks[0].At {
		st.marks[0], st.marks[1] = st.marks[1], st.marks[0]
	}
	if len(st.marks) == 0 {
		return nil
	}
	return st.marks
}

// Release returns a block; the board's buffers are reused by the next
// Lease.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Release(hal.StreamBlock) {}

// Stop stops the stream: the worker's pump is let go first, the drain
// scheduling after. OWNER.
// TRLC-LINKS: REQ-SDS-035
func (s *streamerFacet) Stop() error {
	b := (*Board)(s)
	st := s.streamOf()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pump != nil {
		if _, err := st.pump.SetIsolatedStreamPump(false); err != nil && b.panel.writeErr != nil {
			b.panel.writeErr(err)
		}
		st.pump = nil
	}
	st.running = false
	st.markMu.Lock()
	st.marksOn = false
	st.markMu.Unlock()
	b.state = hal.Idle
	ctx, cancel := context.WithTimeout(context.Background(), haltTimeout)
	err := b.mem.StopStream(ctx)
	cancel()
	if st.deadline != nil {
		_ = st.deadline.Close()
		st.deadline = nil
	}
	return err
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

var (
	_ hal.RingRecorder = (*streamerFacet)(nil)
	_ hal.StreamMarker = (*streamerFacet)(nil)
)

// sleepCtx waits d, or until ctx ends.
// TRLC-LINKS: REQ-SDS-035
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

type stackerFacet Board

// Stack is refused: the stacking image accumulates against a template the
// request carries (a level, a window, a qualifier), which the engine's FPGA
// stack job builds; hal.Stacker's record count alone cannot ask for it.
// TRLC-LINKS: REQ-SDS-035
func (s *stackerFacet) Stack(int) error {
	return hal.Refuse("hardware stacking", "the stacking image needs its template request: the FPGA stack job runs it")
}
