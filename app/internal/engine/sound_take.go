// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"time"
)

// SOUND's takes (ADR-SOUND): the audio stream works like the rest of the
// scope. RUN in the AUTO trigger mode is the live stream. STOP freezes it:
// the newest seconds become a take, the stream stops and its ring goes. In
// the NORMAL mode, and on SINGLE, the stream runs armed on the FPGA's edge
// marks (the TRIG source, level and slope, or SOUND's gate): the mark that
// fires is the trigger, and once the post-trigger time has streamed the
// stretch around it becomes the take, the trigger inside it at the
// pre-trigger time. SINGLE then stops; NORMAL arms again after the take
// and refreshes it on each trigger. The FPGA decides every trigger: a mark
// that came with others since the last read (the newest of several
// crossings is the one the image keeps) is placed on the first crossing of
// the same level after the arm, inside the stretch the image says holds
// them.

// Take bounds: STOP keeps the newest soundStopTakeS; a triggered take is
// pre plus post, at most soundTakeMaxS (the ring holds 17 s).
const (
	soundStopTakeS = 15.0
	soundTakeMaxS  = 16.0
	soundPreMaxS   = 10.0
	soundPostMinS  = 0.1
)

// SoundTake is a frozen stretch of the audio stream: the samples, scales,
// gaps and marks of First..First+N-1, and the trigger in it.
type SoundTake struct {
	SoundBlock
	ID         uint64
	Why        string // "stop", "single" or "normal"
	HasTrig    bool
	Trig       uint64 // the trigger's sample (stream ordinal)
	TrigCh     int
	TrigRising bool
	TrigLevel  uint8 // the FPGA's level, an 8-bit code
	At         time.Time
}

// SoundTakeTimes bounds pre- and post-trigger seconds.
// TRLC-LINKS: REQ-SDS-275
func SoundTakeTimes(pre, post float64) (float64, float64) {
	if !(pre >= 0) || math.IsInf(pre, 0) {
		pre = 1
	}
	if !(post >= soundPostMinS) || math.IsInf(post, 0) {
		post = 4
	}
	pre = math.Min(pre, soundPreMaxS)
	post = math.Min(post, soundTakeMaxS-soundPostMinS)
	if pre+post > soundTakeMaxS {
		pre = soundTakeMaxS - post
	}
	return pre, post
}

// SetSoundTakeTimes sets the pre- and post-trigger seconds of a triggered
// take (bounded).
// TRLC-LINKS: REQ-SDS-275
func (e *Engine) SetSoundTakeTimes(pre, post float64) {
	s := e.sound()
	pre, post = SoundTakeTimes(pre, post)
	s.preBits.Store(math.Float64bits(pre))
	s.postBits.Store(math.Float64bits(post))
}

// soundTakeTimes is the pre- and post-trigger seconds.
// TRLC-LINKS: REQ-SDS-275
func (s *soundState) takeTimes() (pre, post float64) {
	return math.Float64frombits(s.preBits.Load()), math.Float64frombits(s.postBits.Load())
}

// SetSoundTrigGate makes a take's trigger SOUND's gate (cfg: its channel,
// level and directions) in place of the TRIG settings; on false the TRIG
// settings again.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-268
func (e *Engine) SetSoundTrigGate(cfg SoundMarkConfig, on bool) {
	s := e.sound()
	if !on {
		s.gateCfg.Store(nil)
		return
	}
	cfg.Ch &= 1
	if !cfg.Rising && !cfg.Falling {
		cfg.Rising = true
	}
	s.gateCfg.Store(&cfg)
}

// SoundTake is the take shown (nil: none, the stream is live). It is never
// changed once published: a new take replaces it.
// TRLC-LINKS: REQ-SDS-275
func (e *Engine) SoundTake() *SoundTake {
	s := e.snd.Load()
	if s == nil {
		return nil
	}
	return s.take.Load()
}

// soundTrigMode reports a triggered mode: NORMAL, or a SINGLE armed.
// TRLC-LINKS: REQ-SDS-275
func (e *Engine) soundTrigMode() bool {
	if e.singleArmed.Load() {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.norm
}

// soundTrigCfg is the trigger's marks: SOUND's gate, or the TRIG source,
// level and slope.
// TRLC-LINKS: REQ-SDS-275
func (e *Engine) soundTrigCfg(s *soundState) SoundMarkConfig {
	if g := s.gateCfg.Load(); g != nil {
		return *g
	}
	rising := e.trigRising.Load()
	return SoundMarkConfig{Ch: int(e.trigSrc.Load()) & 1, Level: uint8(e.trigLevelWord()), Rising: rising, Falling: !rising}
}

// soundMarkWant is the marks the stream should make now: the trigger's
// while a triggered mode runs, else what the SOUND service asked (the
// cycle trace, a gate-armed envelope).
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-272
func (e *Engine) soundMarkWant(s *soundState) SoundMarkConfig {
	if s.trigMode {
		return e.soundTrigCfg(s)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.markCfg
}

// soundFreeze is a step while STOPped: on the step that stops, the take
// (a triggered take kept, else the newest seconds), then the stream ends
// and its ring goes; after, nothing but the beat.
// TRLC-LINKS: REQ-SDS-275
func (e *Engine) soundFreeze(s *soundState) {
	if s.frozen {
		e.sleepBeating(20 * time.Millisecond)
		return
	}
	s.frozen = true
	if t := s.take.Load(); t == nil || !s.trigMode {
		e.soundPublish(s, s.ring.latest(int(soundStopTakeS*s.info.RateHz), nil, nil, s.info.RateHz), "stop", nil, 0)
	}
	s.armed, s.pending = false, false
	if s.streaming {
		e.soundStop(s)
	}
	s.ring.release()
	s.setState("stopped", false, nil)
}

// soundPublish makes block b the take: Why, and the trigger at trig when
// cfg is not nil.
// TRLC-LINKS: REQ-SDS-275
func (e *Engine) soundPublish(s *soundState, b SoundBlock, why string, cfg *SoundMarkConfig, trig uint64) {
	if b.N == 0 {
		return
	}
	s.takeSeq++
	t := &SoundTake{SoundBlock: b, ID: s.takeSeq, Why: why, At: e.clk.Now()}
	if cfg != nil {
		t.HasTrig, t.Trig, t.TrigCh, t.TrigRising, t.TrigLevel = true, trig, cfg.Ch, cfg.Rising, cfg.Level
	}
	s.take.Store(t)
	e.logf("engine: SOUND take %d (%s, %.1f s)", t.ID, why, float64(b.N)/b.RateHz)
}

// soundTrigStep runs the trigger one step after a drain: arm (once the
// pre-trigger time has streamed), take the FPGA's mark that fires, and
// once the post-trigger time has streamed publish the take; SINGLE then
// stops, NORMAL arms again after it.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-272
func (e *Engine) soundTrigStep(s *soundState) {
	if !s.marksOn {
		s.setMode("no edge marks in this FPGA image")
		return
	}
	rate := s.info.RateHz
	pre, post := s.takeTimes()
	preN, postN := uint64(pre*rate), uint64(math.Ceil(post*rate))
	cfg := s.applied
	s.ring.mu.Lock()
	next := s.ring.next
	lastGap := uint64(0)
	if k := len(s.ring.gaps); k > 0 {
		lastGap = s.ring.gaps[k-1]
	}
	var marks []SoundMark
	if s.armed {
		for _, m := range s.ring.marks {
			if m.At >= s.armFrom {
				marks = append(marks, m)
			}
		}
	}
	s.ring.mu.Unlock()
	if !s.armed && !s.pending {
		// Arm: marks of this configuration only, after the pre-trigger time
		// has streamed (from the last gap on, the stretch is one signal).
		s.armed = true
		s.armFrom = max(next, s.cfgFrom, s.nextArm)
		s.armAt = max(s.armFrom, lastGap+preN)
	}
	if s.armed {
		if lastGap+preN > s.armAt {
			s.armAt = lastGap + preN // a gap since: the pre-trigger time again
		}
		if s.cfgFrom > s.armFrom {
			// the level, slope or source changed since the arm: only its marks count
			s.armFrom, s.armAt = s.cfgFrom, max(s.armAt, s.cfgFrom)
		}
		prevAt, havePrev := uint64(0), false
		for _, m := range marks {
			if m.Ch != cfg.Ch || m.Rising != cfg.Rising && !(cfg.Rising && cfg.Falling) {
				continue
			}
			if m.At < s.armAt {
				prevAt, havePrev = m.At, true
				continue
			}
			at := m.At
			if m.N > 1 {
				// Several crossings since the last read: the image kept the
				// newest; the first after the arm (and after the mark before)
				// is on the samples between.
				lo := s.armAt
				if havePrev && prevAt+1 > lo {
					lo = prevAt + 1
				}
				if m.At >= next {
					return // its samples are not here yet
				}
				at = e.soundFirstCrossing(s, lo, m.At, cfg, m.Rising)
			}
			s.armed, s.pending, s.trigAt, s.trigRising = false, true, at, m.Rising
			break
		}
		if s.armed {
			s.setMode("armed")
			return
		}
	}
	if !s.pending {
		return
	}
	s.setMode("triggered")
	if next < s.trigAt+postN {
		return
	}
	s.pending = false
	from := s.trigAt - min(preN, s.trigAt)
	b := s.ring.read(from, int(s.trigAt+postN-from), nil, nil, rate)
	if len(b.Gaps) > 0 || b.First != from {
		s.nextArm = next // a gap in the stretch: arm again
		return
	}
	single := e.singleArmed.Load()
	why := "normal"
	if single {
		why = "single"
	}
	tc := cfg
	tc.Rising, tc.Falling = s.trigRising, !s.trigRising
	e.soundPublish(s, b, why, &tc, s.trigAt)
	s.nextArm = s.trigAt + postN
	if single {
		e.singleArmed.Store(false)
		e.running.Store(false)
		e.mu.Lock()
		e.stats.Running = false
		e.stats.Single = false
		e.mu.Unlock()
	}
}

// soundFirstCrossing is the first sample in [lo, hi] at which channel cfg.Ch
// crosses the FPGA's level the way the comparator does (from at or below
// the level less the hysteresis, to at or above it; mirrored falling);
// hi (the image's mark) when the samples show none.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-272
func (e *Engine) soundFirstCrossing(s *soundState, lo, hi uint64, cfg SoundMarkConfig, rising bool) uint64 {
	hyst := int(cfg.Hyst)
	if hyst == 0 {
		hyst = 4 // the fabric's default
	}
	lead := uint64(s.info.RateHz / 20) // the state before lo
	from := lo - min(lead, lo)
	b := s.ring.read(from, int(hi-from+1), nil, nil, s.info.RateHz)
	q := b.Q1
	if cfg.Ch == 1 {
		q = b.Q2
	}
	if lo >= b.First {
		if i, ok := firstCrossingFrom(q, int(cfg.Level), hyst, rising, int(lo-b.First)); ok {
			return b.First + uint64(i)
		}
	}
	return hi
}

// firstCrossingFrom is the first crossing at index start or later; the
// comparator's arming runs from the start of q (a crossing before start
// disarms it as in the fabric).
// TRLC-LINKS: REQ-SDS-275
func firstCrossingFrom(q []uint16, level, hyst int, rising bool, start int) (int, bool) {
	armed := false
	for i, w := range q {
		c, l := int(w>>8), level
		if !rising {
			c, l = -c, -l
		}
		switch {
		case c <= l-hyst:
			armed = true
		case armed && c >= l:
			if i >= start {
				return i, true
			}
			armed = false
		}
	}
	return 0, false
}
