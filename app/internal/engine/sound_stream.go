// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The audio stream (ADR-SOUND): while a SOUND consumer holds the lease (the
// LCD's SOUND view, a web page or a phone listening), the acquisition owner
// runs the stream image's continuous stream of decimated words - the
// precision decimator's CIC at 2^13, 61.035 kSa/s a channel, one Q8.8 pair a
// word - and keeps the newest 2^20 samples a channel in a ring that the
// consumers copy from. It is gap-free: a loss (the stream overran) restarts
// the stream and the ring records a gap at that sample, so playback and
// analysis start afresh there instead of joining two stretches. Triggered
// captures stop while it runs: the stream owns the fabric.
//
// The FPGA's edge comparator on the decimated words marks the stream words
// a level crossing falls in (when the image has the marks): the cycle view
// and a gate-armed envelope take their trigger from those, the FPGA
// deciding every one.

// soundState is the owner's stream session and the consumers' view of it.
type soundState struct {
	lease atomic.Int64 // unix nanoseconds the lease runs to
	ring  *core.SoundRing

	mu      sync.Mutex
	info    SoundInfo
	markCfg SoundMarkConfig

	// Takes (sound_take.go).
	take     atomic.Pointer[SoundTake]
	preBits  atomic.Uint64
	postBits atomic.Uint64
	gateCfg  atomic.Pointer[SoundMarkConfig]

	// Owner-only.
	streaming bool
	pump      streamPumper // the bus worker's pump while it drains the banks; nil otherwise
	deadline  *bus.DecodedDeadline
	expected  uint64 // stream words drained (the stream's own ordinal)
	base      uint64 // the ring ordinal of this stream's word 0
	buf       [65536]byte
	retryAt   time.Time
	gapNext   bool
	marksOn   bool
	markSeen  [2]uint16       // the image's rise and fall counts last read
	applied   SoundMarkConfig // the marks the image makes now
	cfgFrom   uint64          // marks from this sample on are of applied
	reload    bool            // a loss ended the last stream: load the image again first

	// The take's trigger (owner-only, sound_take.go).
	takeSeq    uint64
	frozen     bool // STOPped: the take made, the stream ended
	trigMode   bool // NORMAL or SINGLE: the stream runs armed
	armed      bool
	pending    bool   // triggered, the post-trigger time streaming
	armFrom    uint64 // marks from here count
	armAt      uint64 // the first sample a trigger may fall on (the pre-trigger time streamed)
	nextArm    uint64 // NORMAL: the next arm after the take
	trigAt     uint64
	trigRising bool
}

// sound returns the sound state, made on first use.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) sound() *soundState {
	e.soundOnce.Do(func() {
		s := &soundState{ring: core.NewSoundRing(core.SoundRingLog2)}
		s.info.State = "off"
		s.info.RateHz = 500e6 / float64(uint64(1)<<SoundLog2)
		s.info.Mode = "live"
		pre, post := SoundTakeTimes(1, 4)
		s.preBits.Store(math.Float64bits(pre))
		s.postBits.Store(math.Float64bits(post))
		e.snd.Store(s)
	})
	return e.snd.Load()
}

// soundStreaming reports a running audio stream (the acquisition owner's
// view), without making the sound state.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundStreaming() bool {
	s := e.snd.Load()
	return s != nil && s.streaming
}

// LeaseSound keeps the audio stream running for d from now (0 releases).
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) LeaseSound(d time.Duration) {
	s := e.sound()
	if d <= 0 {
		s.lease.Store(0)
		return
	}
	was := e.soundWanted()
	s.lease.Store(e.clk.Now().Add(min(d, core.SoundLeaseMax)).UnixNano())
	if !was {
		// SOUND starts: a record read in flight is not wanted (its frame
		// would be dropped), so the stream need not wait for it. The owner
		// checks the rest (soundStarting; a read cut when another stream
		// holds the fabric costs one frame); while the stream runs no
		// record is read.
		e.rawMu.Lock()
		cut := e.recallCancel
		e.rawMu.Unlock()
		if cut != nil {
			cut()
		}
	}
}

// soundStarting reports a lease that will start the stream (none running,
// nothing holding the fabric): a record read now would only be dropped.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundStarting() bool {
	return e.soundWanted() && !e.soundStreaming() && e.soundBusy() == ""
}

// soundWanted reports a current lease.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundWanted() bool {
	s := e.snd.Load()
	if s == nil {
		return false
	}
	v := s.lease.Load()
	return v != 0 && e.clk.Now != nil && e.clk.Now().UnixNano() < v
}

// SoundInfo reports the audio stream's state.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) SoundInfo() SoundInfo {
	s := e.sound()
	s.mu.Lock()
	in := s.info
	if !s.info.Active {
		in.MarkCfg = s.markCfg // while streaming, the marks in force
	}
	s.mu.Unlock()
	in.PreS, in.PostS = s.takeTimes()
	in.TrigGate = s.gateCfg.Load() != nil
	if t := s.take.Load(); t != nil {
		in.Take = t.ID
	}
	s.ring.Mu.Lock()
	in.Next, in.Oldest, in.LastData = s.ring.Next, s.ring.OldestLocked(), s.ring.Last
	s.ring.Mu.Unlock()
	return in
}

// SoundRead copies up to limit samples of each channel from ordinal from (or
// the oldest held) into q1, q2.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) SoundRead(from uint64, limit int, q1, q2 []uint16) SoundBlock {
	s := e.sound()
	return s.ring.Read(from, limit, q1, q2, s.info.RateHz)
}

// SoundLatest copies the newest n samples of each channel.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) SoundLatest(n int, q1, q2 []uint16) SoundBlock {
	s := e.sound()
	return s.ring.Latest(n, q1, q2, s.info.RateHz)
}

// SoundMarksSince is the marks from ordinal from on.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268
func (e *Engine) SoundMarksSince(from uint64) []SoundMark {
	r := e.sound().ring
	r.Mu.Lock()
	defer r.Mu.Unlock()
	var out []SoundMark
	for _, m := range r.Marks {
		if m.At >= from {
			out = append(out, m)
		}
	}
	return out
}

// SoundWait parks until the ring holds samples past after (or timeout) and
// returns the next ordinal.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) SoundWait(after uint64, timeout time.Duration) uint64 {
	return e.sound().ring.Wait(after, timeout)
}

// SetSoundMarks asks the FPGA to mark crossings of a level on a channel
// (the cycle view's and a gate's trigger); it takes effect on the running
// stream at the next step.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268
func (e *Engine) SetSoundMarks(c SoundMarkConfig) {
	s := e.sound()
	c.Ch &= 1
	s.mu.Lock()
	s.markCfg = c
	s.mu.Unlock()
}

// setSoundState publishes the session's state.
// TRLC-LINKS: REQ-SDS-265
func (s *soundState) setState(state string, active bool, err error) {
	s.mu.Lock()
	s.info.State, s.info.Active = state, active
	s.info.Err = ""
	if err != nil {
		s.info.Err = err.Error()
	}
	s.mu.Unlock()
}

// soundBusy says why the audio stream cannot take the fabric now ("" it can).
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundBusy() string {
	switch {
	case e.decodedSession != nil || e.soft != nil || e.clk.Now != nil && e.clk.Now().Before(e.decodedStarting):
		return "a decoded stream runs (BUS): stop it to listen"
	case e.images == nil || !e.images.HasStream():
		return "this build has no stream image"
	}
	return ""
}

// soundStep runs the audio stream one step on the acquisition owner; it
// reports false when no lease holds (the stream stopped), so the loop
// carries on with captures.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundStep() bool {
	s := e.sound()
	if s.streaming && e.soundBusy() != "" {
		// Another stream took the fabric without ending this one: its
		// stop would halt that stream's acquisition.
		e.soundDrop(s)
	}
	if !e.soundWanted() {
		if s.streaming {
			e.soundStop(s)
		}
		if s.info.State != "off" {
			s.setState("off", false, nil)
			s.ring.Release()
		}
		// Nothing shows SOUND: its take goes with it.
		s.take.Store(nil)
		s.frozen, s.armed, s.pending = false, false, false
		return false
	}
	if why := e.soundBusy(); why != "" {
		s.setState(why, false, nil)
		return false
	}
	e.beatN.Add(1)
	if !e.running.Load() {
		e.soundFreeze(s)
		s.setMode("stopped")
		return true
	}
	trig := e.soundTrigMode()
	if s.frozen || trig != s.trigMode {
		// RUN again, or the trigger mode changed: AUTO is the live stream
		// (no take); a triggered mode keeps the take shown until the next.
		s.frozen, s.armed, s.pending, s.nextArm = false, false, false, 0
		if !trig {
			s.take.Store(nil)
		}
	}
	s.trigMode = trig
	if !trig {
		s.setMode("live")
	}
	if !s.streaming {
		if e.clk.Now().Before(s.retryAt) {
			e.sleepBeating(20 * time.Millisecond)
			return true
		}
		if err := e.soundStart(s); err != nil {
			s.setState("error", false, err)
			s.retryAt = e.clk.Now().Add(time.Second)
			return true
		}
	}
	e.soundDrain(s)
	if trig && s.streaming {
		e.soundTrigStep(s)
	}
	return true
}

// setMode publishes the take's mode.
// TRLC-LINKS: REQ-SDS-275
func (s *soundState) setMode(m string) {
	s.mu.Lock()
	s.info.Mode = m
	s.mu.Unlock()
}

// soundStart loads the stream image if needed and starts the stream.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundStart(s *soundState) error {
	s.setState("starting", false, nil)
	// After a fault the stream image can fault every new stream at its first
	// word until it is loaded again (seen on the instrument); a fresh load
	// costs about a second, once per loss.
	if e.protocolImage != acq.ImageStream || s.reload || e.streamReload {
		s.reload, e.streamReload = false, false
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := e.sram.Halt(ctx)
		cancel()
		if err != nil {
			return err
		}
		e.loadImage(acq.ImageStream)
		if e.protocolImage != acq.ImageStream {
			return errors.New("the stream image did not load")
		}
	}
	mc := e.soundMarkWant(s)
	cfg := sramcapture.Config{
		Source: sramcapture.ADC, DecimationLog2: SoundLog2, PreWords: sramcapture.Words - 17, PostWords: 17,
		TriggerChannel: uint8(mc.Ch), TriggerLevel: mc.Level, TriggerHysteresis: mc.Hyst, Normal: true, //nolint:gosec // G115: the mark channel is 0 or 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	marks := e.sram.StreamMarksSupported()
	if err := e.sram.StartStreamMarked(ctx, cfg, sramcapture.StreamMarkMode{Rise: marks && mc.Rising, Fall: marks && mc.Falling}); err != nil {
		return err
	}
	s.streaming, s.expected, s.applied, s.marksOn = true, 0, mc, marks
	s.markSeen = [2]uint16{}
	s.ring.Mu.Lock()
	s.base = s.ring.Next
	s.cfgFrom = s.base
	s.ring.Mu.Unlock()
	pump, err := e.ownStreamPump()
	if err != nil {
		e.soundStop(s)
		return err
	}
	s.pump = pump
	if dev, ok := e.b.(*bus.Dev); ok && s.pump == nil && s.deadline == nil {
		if d, err := dev.StartDecodedDeadline(); err == nil {
			s.deadline = d
		}
	}
	s.mu.Lock()
	s.info.Marks = marks
	s.info.MarkCfg = mc
	s.info.Started = e.clk.Now()
	s.mu.Unlock()
	s.setState("streaming", true, nil)
	e.logf("engine: audio stream at %.0f Sa/s (marks %v)", s.info.RateHz, marks)
	return nil
}

// soundStop ends the stream session.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundStop(s *soundState) {
	if !s.streaming {
		return
	}
	if s.pump != nil {
		if _, err := s.pump.SetIsolatedStreamPump(false); err != nil {
			e.busErr(err)
		}
		s.pump = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := e.sram.StopStream(ctx); err != nil {
		e.busErr(err)
	}
	cancel()
	if s.deadline != nil {
		_ = s.deadline.Close()
		s.deadline = nil
	}
	s.streaming = false
	s.gapNext = true
}

// soundYield ends a running audio stream before another stream (a decoded
// capture, a software-decoded stream) takes the fabric; the audio stream
// then waits, its state saying why, until the fabric is free.
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-018
func (e *Engine) soundYield() {
	if s := e.snd.Load(); s != nil && s.streaming {
		e.soundStop(s)
		s.setState("off", false, nil)
	}
}

// soundDrop forgets a stream session whose fabric another stream holds,
// writing nothing to the fabric (its pump and deadline released).
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundDrop(s *soundState) {
	if s.pump != nil {
		if _, err := s.pump.SetIsolatedStreamPump(false); err != nil {
			e.busErr(err)
		}
		s.pump = nil
	}
	if s.deadline != nil {
		_ = s.deadline.Close()
		s.deadline = nil
	}
	s.streaming, s.gapNext = false, true
}

// soundDrain moves the stream's words into the ring for up to 40 ms.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundDrain(s *soundState) {
	if mc := e.soundMarkWant(s); mc != s.applied && s.marksOn {
		// The level, channel and directions latch while the drum runs.
		if err := e.sram.SetStreamMarks(mc.Ch, mc.Level, mc.Hyst, sramcapture.StreamMarkMode{Rise: mc.Rising, Fall: mc.Falling}); err != nil {
			e.busErr(err)
		}
		s.applied = mc
		s.ring.Mu.Lock()
		s.cfgFrom = s.ring.Next // marks of the configuration before do not count
		s.ring.Mu.Unlock()
		s.mu.Lock()
		s.info.MarkCfg = mc
		s.mu.Unlock()
	}
	got := 0
	for end := e.clk.Now().Add(40 * time.Millisecond); e.clk.Now().Before(end) && !e.stopReq.Load(); {
		raw, err := e.soundNextWords(s)
		if err != nil {
			e.soundLoss(s, err)
			return
		}
		if raw == nil {
			break
		}
		s.ring.PushWords(raw, e.soundScale(), s.gapNext, e.clk.Now())
		s.gapNext = false
		got += len(raw)
	}
	if s.marksOn {
		e.soundReadMarks(s)
	}
	if got == 0 {
		if s.deadline != nil {
			_ = s.deadline.Pause()
		} else {
			e.clk.Sleep(5 * time.Millisecond)
		}
	}
}

// soundNextWords reads the stream's next words: from the bus worker's ring,
// or drained from the banks. nil when none are there yet.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundNextWords(s *soundState) ([]byte, error) {
	if s.pump != nil {
		first, n, _, err := s.pump.ReadIsolatedStream(s.buf[:])
		if err == nil && n > 0 && first != s.expected {
			err = fmt.Errorf("stream ordinal %d, want %d", first, s.expected)
		}
		if err != nil || n == 0 {
			return nil, err
		}
		s.expected += uint64(n / 4) //nolint:gosec // G115: n is a read's non-negative byte count
		return s.buf[:n], nil
	}
	raw := s.buf[:0]
	w := byteSink{dst: &raw}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	block, err := e.sram.DrainStream(ctx, s.expected, &w)
	cancel()
	if errors.Is(err, sramcapture.ErrNoStreamBlock) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.expected = block.FirstWord + uint64(block.Words)
	return raw, nil
}

// soundScale is the front end's scale now.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundScale() SoundScale {
	sc := e.liveScale()
	return SoundScale{VdivV: [2]float64{sc[0].Vdiv, sc[1].Vdiv}, OffV: [2]float64{sc[0].Off, sc[1].Off}}
}

// soundLoss records a loss and ends the stream; the next step starts a new
// one after a gap.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundLoss(s *soundState, err error) {
	e.logf("engine: audio stream lost words after %d: %v", s.expected, err)
	s.mu.Lock()
	s.info.Losses++
	s.mu.Unlock()
	s.reload = true
	e.soundStop(s)
}

// soundReadMarks reads the image's newest marks: a rise and a fall
// counter and the stream word each newest one fell in.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268
func (e *Engine) soundReadMarks(s *soundState) {
	m, err := e.sram.StreamMarks()
	if err != nil {
		return
	}
	ch := s.applied.Ch
	var out []SoundMark
	if m.RiseCount != s.markSeen[0] {
		out = append(out, SoundMark{At: s.base + core.UnwrapWord(m.RiseWord, s.expected), Ch: ch, Rising: true, N: int(m.RiseCount - s.markSeen[0])})
	}
	if m.FallCount != s.markSeen[1] {
		out = append(out, SoundMark{At: s.base + core.UnwrapWord(m.FallWord, s.expected), Ch: ch, Rising: false, N: int(m.FallCount - s.markSeen[1])})
	}
	s.markSeen = [2]uint16{m.RiseCount, m.FallCount}
	if len(out) == 2 && out[1].At < out[0].At {
		out[0], out[1] = out[1], out[0]
	}
	s.ring.AddMarks(out)
}
