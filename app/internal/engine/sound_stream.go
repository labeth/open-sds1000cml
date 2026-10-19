// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/sramcapture"
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

// SoundLog2 is the audio stream's decimation (sound.StreamLog2).
const SoundLog2 = 13

// soundRingLog2 sizes the ring: 2^20 samples a channel, 17 s.
const soundRingLog2 = 20

// soundLeaseMax bounds one lease.
const soundLeaseMax = 10 * time.Second

// SoundScale is the front end's scale over a stretch of the stream: volts at
// the analog input are (code-128)*VdivV/25 - OffV, and at the probe tip
// times the probe factor.
type SoundScale struct {
	From  uint64     `json:"from"` // the first sample it applies to
	VdivV [2]float64 `json:"vdiv_v"`
	OffV  [2]float64 `json:"off_v"`
}

// SoundMark is a level crossing the FPGA marked: the sample (stream
// ordinal) whose value crossed, the channel and direction.
type SoundMark struct {
	At     uint64 `json:"at"`
	Ch     int    `json:"ch"`
	Rising bool   `json:"rising"`
}

// SoundMarkConfig asks the FPGA for marks: the channel, the level (8-bit
// code), hysteresis codes, and which directions.
type SoundMarkConfig struct {
	Ch      int   `json:"ch"`
	Level   uint8 `json:"level"`
	Hyst    uint8 `json:"hyst"`
	Rising  bool  `json:"rising"`
	Falling bool  `json:"falling"`
}

// SoundInfo is the audio stream's state.
type SoundInfo struct {
	State    string          `json:"state"` // "off", "starting", "streaming", or why it cannot run
	Active   bool            `json:"active"`
	RateHz   float64         `json:"rate_hz"`
	Next     uint64          `json:"next"`   // the next sample's ordinal
	Oldest   uint64          `json:"oldest"` // the oldest sample still held
	Losses   int             `json:"losses"`
	Marks    bool            `json:"marks"` // the image marks crossings
	MarkCfg  SoundMarkConfig `json:"mark_cfg"`
	Started  time.Time       `json:"started"`
	LastData time.Time       `json:"last_data"`
	Err      string          `json:"err,omitempty"`
}

// SoundBlock is a copy of the ring: samples First..First+N-1 of each
// channel (Q8.8 codes), with the scales, gaps and marks inside it.
type SoundBlock struct {
	First  uint64
	N      int
	Q1, Q2 []uint16
	RateHz float64
	Scales []SoundScale // the first applies from First (or before)
	Gaps   []uint64     // samples a loss came before
	Marks  []SoundMark
	Next   uint64 // the ring's next ordinal when copied
}

// soundRing holds the stream's newest samples, shared between the
// acquisition owner (writer) and the consumers.
type soundRing struct {
	mu     sync.Mutex
	q      [2][]uint16
	next   uint64
	scales []SoundScale
	gaps   []uint64
	marks  []SoundMark
	wake   chan struct{} // closed and replaced on every append
	last   time.Time
}

// newSoundRing makes an empty ring of 2^log2 samples a channel.
// TRLC-LINKS: REQ-SDS-265
func newSoundRing(log2 int) *soundRing {
	n := 1 << log2
	return &soundRing{q: [2][]uint16{make([]uint16, n), make([]uint16, n)}, wake: make(chan struct{})}
}

// oldest is the oldest ordinal held (r.mu held).
// TRLC-LINKS: REQ-SDS-265
func (r *soundRing) oldestLocked() uint64 {
	if size := uint64(len(r.q[0])); r.next > size {
		return r.next - size
	}
	return 0
}

// pushWords appends stream words (CH1 Q8.8, CH2 Q8.8, little-endian) at
// scale sc; gap says words were lost before them.
// TRLC-LINKS: REQ-SDS-265
func (r *soundRing) pushWords(raw []byte, sc SoundScale, gap bool, now time.Time) {
	n := len(raw) / 4
	if n == 0 && !gap {
		return
	}
	r.mu.Lock()
	mask := uint64(len(r.q[0]) - 1)
	if gap && r.next > 0 && (len(r.gaps) == 0 || r.gaps[len(r.gaps)-1] != r.next) {
		r.gaps = append(r.gaps, r.next)
	}
	if k := len(r.scales); k == 0 || r.scales[k-1].VdivV != sc.VdivV || r.scales[k-1].OffV != sc.OffV {
		sc.From = r.next
		r.scales = append(r.scales, sc)
	}
	q1, q2 := r.q[0], r.q[1]
	for i := 0; i < n; i++ {
		j := (r.next + uint64(i)) & mask
		q1[j] = uint16(raw[4*i]) | uint16(raw[4*i+1])<<8
		q2[j] = uint16(raw[4*i+2]) | uint16(raw[4*i+3])<<8
	}
	r.next += uint64(n)
	r.trimLocked()
	if n > 0 {
		r.last = now
	}
	close(r.wake)
	r.wake = make(chan struct{})
	r.mu.Unlock()
}

// addMarks records marks (ordinals already in the stream's sample count).
// TRLC-LINKS: REQ-SDS-272
func (r *soundRing) addMarks(ms []SoundMark) {
	if len(ms) == 0 {
		return
	}
	r.mu.Lock()
	r.marks = append(r.marks, ms...)
	r.trimLocked()
	r.mu.Unlock()
}

// trimLocked drops scales, gaps and marks older than the ring (keeping the
// scale in force at its oldest sample).
// TRLC-LINKS: REQ-SDS-265
func (r *soundRing) trimLocked() {
	old := r.oldestLocked()
	for len(r.scales) > 1 && r.scales[1].From <= old {
		r.scales = r.scales[1:]
	}
	for len(r.gaps) > 0 && r.gaps[0] < old {
		r.gaps = r.gaps[1:]
	}
	for len(r.marks) > 0 && r.marks[0].At < old {
		r.marks = r.marks[1:]
	}
	if len(r.marks) > 4096 {
		r.marks = r.marks[len(r.marks)-4096:]
	}
}

// read copies up to max samples from ordinal from (moved up to the oldest
// held) into q1, q2 (grown as needed).
// TRLC-LINKS: REQ-SDS-265
func (r *soundRing) read(from uint64, max int, q1, q2 []uint16, rate float64) SoundBlock {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := SoundBlock{RateHz: rate, Next: r.next}
	if old := r.oldestLocked(); from < old {
		from = old
	}
	if from > r.next {
		from = r.next
	}
	n := int(min(uint64(max), r.next-from))
	b.First, b.N = from, n
	b.Q1, b.Q2 = append(q1[:0], make([]uint16, n)...), append(q2[:0], make([]uint16, n)...)
	mask := uint64(len(r.q[0]) - 1)
	for i := 0; i < n; i++ {
		j := (from + uint64(i)) & mask
		b.Q1[i], b.Q2[i] = r.q[0][j], r.q[1][j]
	}
	end := from + uint64(n)
	for i, s := range r.scales {
		if s.From < end && (i+1 == len(r.scales) || r.scales[i+1].From > from) {
			b.Scales = append(b.Scales, s)
		}
	}
	for _, g := range r.gaps {
		if g > from && g < end {
			b.Gaps = append(b.Gaps, g)
		}
	}
	for _, m := range r.marks {
		if m.At >= from && m.At < end {
			b.Marks = append(b.Marks, m)
		}
	}
	return b
}

// latest copies the newest n samples.
// TRLC-LINKS: REQ-SDS-265
func (r *soundRing) latest(n int, q1, q2 []uint16, rate float64) SoundBlock {
	r.mu.Lock()
	next := r.next
	r.mu.Unlock()
	from := uint64(0)
	if next > uint64(n) {
		from = next - uint64(n)
	}
	return r.read(from, n, q1, q2, rate)
}

// wait returns once the ring holds samples past after, or at timeout.
// TRLC-LINKS: REQ-SDS-265
func (r *soundRing) wait(after uint64, timeout time.Duration) uint64 {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		r.mu.Lock()
		next, wake := r.next, r.wake
		r.mu.Unlock()
		if next > after {
			return next
		}
		select {
		case <-wake:
		case <-deadline.C:
			return next
		}
	}
}

// soundState is the owner's stream session and the consumers' view of it.
type soundState struct {
	lease atomic.Int64 // unix nanoseconds the lease runs to
	ring  *soundRing

	mu      sync.Mutex
	info    SoundInfo
	markCfg SoundMarkConfig
	markGen uint64

	// Owner-only.
	streaming bool
	isolated  bool
	deadline  *bus.DecodedDeadline
	expected  uint64 // stream words drained (the stream's own ordinal)
	base      uint64 // the ring ordinal of this stream's word 0
	buf       [65536]byte
	retryAt   time.Time
	gapNext   bool
	marksOn   bool
	markSeen  [2]uint16 // the image's rise and fall counts last read
	markWant  uint64    // the mark config generation in force
	reload    bool      // a loss ended the last stream: load the image again first
}

// sound returns the sound state, made on first use.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) sound() *soundState {
	e.soundOnce.Do(func() {
		s := &soundState{ring: newSoundRing(soundRingLog2)}
		s.info.State = "off"
		s.info.RateHz = 500e6 / float64(uint64(1)<<SoundLog2)
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
	s.lease.Store(e.clk.Now().Add(min(d, soundLeaseMax)).UnixNano())
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
	in.MarkCfg = s.markCfg
	s.mu.Unlock()
	s.ring.mu.Lock()
	in.Next, in.Oldest, in.LastData = s.ring.next, s.ring.oldestLocked(), s.ring.last
	s.ring.mu.Unlock()
	return in
}

// SoundRead copies up to max samples of each channel from ordinal from (or
// the oldest held) into q1, q2.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) SoundRead(from uint64, max int, q1, q2 []uint16) SoundBlock {
	s := e.sound()
	return s.ring.read(from, max, q1, q2, s.info.RateHz)
}

// SoundLatest copies the newest n samples of each channel.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) SoundLatest(n int, q1, q2 []uint16) SoundBlock {
	s := e.sound()
	return s.ring.latest(n, q1, q2, s.info.RateHz)
}

// SoundMarksSince is the marks from ordinal from on.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268
func (e *Engine) SoundMarksSince(from uint64) []SoundMark {
	r := e.sound().ring
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SoundMark
	for _, m := range r.marks {
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
	return e.sound().ring.wait(after, timeout)
}

// SetSoundMarks asks the FPGA to mark crossings of a level on a channel
// (the cycle view's and a gate's trigger); it takes effect on the running
// stream at the next step.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268
func (e *Engine) SetSoundMarks(c SoundMarkConfig) {
	s := e.sound()
	c.Ch &= 1
	s.mu.Lock()
	if c != s.markCfg {
		s.markCfg = c
		s.markGen++
	}
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
	case e.decodedSession != nil || e.soft != nil:
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
		}
		return false
	}
	if why := e.soundBusy(); why != "" {
		s.setState(why, false, nil)
		return false
	}
	e.beatN.Add(1)
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
	return true
}

// soundStart loads the stream image if needed and starts the stream.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundStart(s *soundState) error {
	s.setState("starting", false, nil)
	// After a fault the stream image can fault every new stream at its first
	// word until it is loaded again (seen on the instrument); a fresh load
	// costs about a second, once per loss.
	if e.protocolImage != imageStream || s.reload || e.streamReload {
		s.reload, e.streamReload = false, false
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := e.sram.Halt(ctx)
		cancel()
		if err != nil {
			return err
		}
		e.loadImage(imageStream)
		if e.protocolImage != imageStream {
			return fmt.Errorf("the stream image did not load")
		}
	}
	s.mu.Lock()
	mc, gen := s.markCfg, s.markGen
	s.mu.Unlock()
	cfg := sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: SoundLog2, PreWords: sramcapture.Words - 17, PostWords: 17,
		TriggerChannel: uint8(mc.Ch), TriggerLevel: mc.Level, TriggerHysteresis: mc.Hyst, Normal: true}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	marks := e.sram.StreamMarksSupported()
	if err := e.sram.StartStreamMarked(ctx, cfg, sramcapture.StreamMarkMode{Rise: marks && mc.Rising, Fall: marks && mc.Falling}); err != nil {
		return err
	}
	s.streaming, s.expected, s.markWant, s.marksOn = true, 0, gen, marks
	s.markSeen = [2]uint16{}
	s.ring.mu.Lock()
	s.base = s.ring.next
	s.ring.mu.Unlock()
	if p, ok := e.b.(streamPumper); ok {
		owned, err := p.SetIsolatedStreamPump(true)
		if err != nil {
			e.soundStop(s)
			return err
		}
		s.isolated = owned
	}
	if dev, ok := e.b.(*bus.Dev); ok && !s.isolated && s.deadline == nil {
		if d, err := dev.StartDecodedDeadline(); err == nil {
			s.deadline = d
		}
	}
	s.mu.Lock()
	s.info.Marks = marks
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
	if s.isolated {
		if _, err := e.b.(streamPumper).SetIsolatedStreamPump(false); err != nil {
			e.busErr(err)
		}
		s.isolated = false
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
	if s.isolated {
		if _, err := e.b.(streamPumper).SetIsolatedStreamPump(false); err != nil {
			e.busErr(err)
		}
		s.isolated = false
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
	s.mu.Lock()
	gen := s.markGen
	mc := s.markCfg
	s.mu.Unlock()
	if gen != s.markWant && s.marksOn {
		// The level and channel latch while the drum runs; the directions
		// with the stream mode, which a restart writes.
		if err := e.sram.SetStreamMarks(mc.Ch, mc.Level, mc.Hyst, sramcapture.StreamMarkMode{Rise: mc.Rising, Fall: mc.Falling}); err != nil {
			e.busErr(err)
		}
		s.markWant = gen
	}
	got := 0
	for end := e.clk.Now().Add(40 * time.Millisecond); e.clk.Now().Before(end) && !e.stopReq.Load(); {
		var raw []byte
		if s.isolated {
			first, n, _, err := e.b.(streamPumper).ReadIsolatedStream(s.buf[:])
			if err == nil && n > 0 && first != s.expected {
				err = fmt.Errorf("stream ordinal %d, want %d", first, s.expected)
			}
			if err != nil {
				e.soundLoss(s, err)
				return
			}
			if n == 0 {
				break
			}
			raw = s.buf[:n]
			s.expected += uint64(n / 4)
		} else {
			w := byteSink{dst: &raw}
			raw = s.buf[:0]
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			block, err := e.sram.DrainStream(ctx, s.expected, &w)
			cancel()
			if err == sramcapture.ErrNoStreamBlock {
				break
			}
			if err != nil {
				e.soundLoss(s, err)
				return
			}
			s.expected = block.FirstWord + uint64(block.Words)
		}
		s.ring.pushWords(raw, e.soundScale(), s.gapNext, e.clk.Now())
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

// soundScale is the front end's scale now.
// TRLC-LINKS: REQ-SDS-265
func (e *Engine) soundScale() SoundScale {
	sc := e.liveScale()
	return SoundScale{VdivV: [2]float64{sc[0].vdiv, sc[1].vdiv}, OffV: [2]float64{sc[0].off, sc[1].off}}
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
	s.mu.Lock()
	ch := s.markCfg.Ch
	s.mu.Unlock()
	var out []SoundMark
	if m.RiseCount != s.markSeen[0] {
		out = append(out, SoundMark{At: s.base + unwrapWord(m.RiseWord, s.expected), Ch: ch, Rising: true})
	}
	if m.FallCount != s.markSeen[1] {
		out = append(out, SoundMark{At: s.base + unwrapWord(m.FallWord, s.expected), Ch: ch, Rising: false})
	}
	s.markSeen = [2]uint16{m.RiseCount, m.FallCount}
	if len(out) == 2 && out[1].At < out[0].At {
		out[0], out[1] = out[1], out[0]
	}
	s.ring.addMarks(out)
}

// unwrapWord extends the image's 32-bit word count w to the stream's
// ordinal near ref (marks can run ahead of the words drained).
// TRLC-LINKS: REQ-SDS-272
func unwrapWord(w uint32, ref uint64) uint64 {
	v := ref&^0xffffffff | uint64(w)
	switch {
	case v > ref+1<<31:
		if v >= 1<<32 {
			v -= 1 << 32
		}
	case v+1<<31 < ref:
		v += 1 << 32
	}
	return v
}

// SoundVolts is the scale's volts per code and offset at the analog input
// for channel ch (times the probe factor at the tip).
// TRLC-LINKS: REQ-SDS-265
func (s SoundScale) SoundVolts(ch int) (vpc, off float64) {
	ch &= 1
	if !(s.VdivV[ch] > 0) || math.IsInf(s.VdivV[ch], 0) {
		return 0, 0
	}
	return s.VdivV[ch] / 25, s.OffV[ch]
}
