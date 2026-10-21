// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/sound"
)

// SOUND's takes on the scope's side (engine/sound_take.go makes them): while
// a take is shown (STOP, or a NORMAL or SINGLE trigger's take) the readouts
// read the take's visible window instead of the newest samples, the LCD's
// plot shows the take with its trigger, Position scrolls it and S/DIV
// zooms; while armed, Position moves the trigger within the take.

// soundTaker is the engine's take control (an engine without it has none).
type soundTaker interface {
	SoundTake() *engine.SoundTake
	SetSoundTakeTimes(pre, post float64)
	SetSoundTrigGate(cfg engine.SoundMarkConfig, on bool)
}

// soundTakeView is the take shown and the window of it the LCD and the
// readouts use (samples from the take's first).
type soundTakeView struct {
	tk   *engine.SoundTake
	from float64
	span float64
	key  [3]float64 // the take and window the readouts last read
	cols soundTakeCols
}

// soundTakeCols is the take's window drawn as columns, kept until the
// window or the plot's width changes.
type soundTakeCols struct {
	key    [4]float64
	lo, hi [2][]float32
	vlo    [2]float64
	vhi    [2]float64
}

// taker is the engine's take control (nil without one).
// TRLC-LINKS: REQ-SDS-275
func (s *soundSvc) taker() soundTaker {
	t, _ := s.c.eng.(soundTaker)
	return t
}

// takeNow is the take to show now: the engine's, unless the stream is live
// (s.mu held).
// TRLC-LINKS: REQ-SDS-275
func (s *soundSvc) takeNow(t soundTaker) *engine.SoundTake {
	if t == nil || s.info.Mode == "live" || s.info.Mode == "" {
		return nil
	}
	return t.SoundTake()
}

// syncTake adopts the engine's take (a new one is shown whole) and reports
// it (s.mu held).
// TRLC-LINKS: REQ-SDS-275
func (s *soundSvc) syncTake(t soundTaker) *engine.SoundTake {
	tk := s.takeNow(t)
	if tk == nil {
		s.tv = soundTakeView{}
		return nil
	}
	if s.tv.tk == nil || s.tv.tk.ID != tk.ID {
		// A NORMAL refresh of the same length keeps the window; another
		// take is shown whole.
		from, span := s.tv.from, s.tv.span
		if s.tv.tk == nil || tk.N != s.tv.tk.N || tk.Why == "stop" {
			from, span = 0, float64(tk.N)
		}
		s.tv = soundTakeView{tk: tk, from: from, span: span}
	}
	return tk
}

// takeLatest is n samples of channel ch around the take window's centre
// (the readouts' "latest" while a take is shown), as a block; false with
// no take.
// TRLC-LINKS: REQ-SDS-275
func (s *soundSvc) takeLatest(n, ch int) ([]float64, engine.SoundBlock, bool) {
	s.mu.Lock()
	tk, c := s.tv.tk, s.tv.from+s.tv.span/2
	s.mu.Unlock()
	if tk == nil {
		return nil, engine.SoundBlock{}, false
	}
	n = min(n, tk.N)
	a := int(math.Round(c - float64(n)/2))
	a = max(0, min(a, tk.N-n))
	b := min(tk.N, a+n)
	blk := engine.SoundBlock{First: tk.First + uint64(a), N: b - a, Q1: tk.Q1[a:b], Q2: tk.Q2[a:b], RateHz: tk.RateHz,
		Scales: tk.Scales, Next: tk.First + uint64(tk.N)}
	for _, g := range tk.Gaps {
		if g > blk.First && g < blk.First+uint64(blk.N) {
			blk.Gaps = append(blk.Gaps, g)
		}
	}
	s.x = s.volts(blk, ch, s.x)
	if k := len(blk.Gaps); k > 0 {
		cut := int(blk.Gaps[k-1] - blk.First)
		return s.x[cut:], blk, true
	}
	return s.x, blk, true
}

// stepTake reads the take's window once each time it changes: the tuner
// (no smoothing: a frozen take), the meters over the window, the page.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-266, REQ-SDS-267
func (s *soundSvc) stepTake(e soundEngine, set SoundSettings, now time.Time, readouts bool) {
	s.mu.Lock()
	key := [3]float64{float64(s.tv.tk.ID), s.tv.from, s.tv.span}
	if key == s.tv.key || !readouts {
		s.mu.Unlock()
		return
	}
	s.tv.key = key
	fs := s.fs
	s.heavyAt, s.sbAt = time.Time{}, time.Time{}
	s.mu.Unlock()
	x, _ := s.latest(e, sound.TunerWindow(0, fs), set.Ch)
	p := sound.DetectPitch(x, fs, sound.PitchRange(set.Range))
	s.mu.Lock()
	s.hz, s.clarity = 0, p.Clarity
	if p.OK {
		s.hz = p.Hz
	}
	s.tuner = sound.Tuner{Slow: set.Slow}
	s.mu.Unlock()
	s.readLevels(e, set)
	s.readPage(e, set, now)
}

// takeScroll moves the take's window by dir tenths of it.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-273
func (s *soundSvc) takeScroll(dir int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tv.tk == nil {
		return false
	}
	s.tv.from = math.Max(0, math.Min(float64(s.tv.tk.N)-s.tv.span, s.tv.from+float64(dir)*s.tv.span/10))
	return true
}

// takeZoom narrows (dir > 0) or widens the window about its centre, by
// half-octaves, from a tenth of a millisecond's samples to the whole take.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-273
func (s *soundSvc) takeZoom(dir int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tv.tk == nil {
		return false
	}
	n := float64(s.tv.tk.N)
	c := s.tv.from + s.tv.span/2
	span := math.Max(math.Min(64, n), math.Min(n, s.tv.span*math.Pow(2, -0.5*float64(dir))))
	s.tv.span = span
	s.tv.from = math.Max(0, math.Min(n-span, c-span/2))
	return true
}

// takeToTrigger shows the trigger at the window's centre, or (already
// there, or no trigger) the whole take.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-273
func (s *soundSvc) takeToTrigger() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	tk := s.tv.tk
	if tk == nil {
		return ""
	}
	n := float64(tk.N)
	if tk.HasTrig {
		at := float64(tk.Trig - tk.First)
		from := math.Max(0, math.Min(n-s.tv.span, at-s.tv.span/2))
		if math.Abs(from-s.tv.from) > 1 && s.tv.span < n {
			s.tv.from = from
			return "Take: the trigger at the centre"
		}
	}
	s.tv.from, s.tv.span = 0, n
	return "Take: all of it"
}

// moveTrigger moves the trigger within a triggered take by dir twentieths
// of it (the pre-trigger time against the post, their sum kept).
// TRLC-LINKS: REQ-SDS-275
func (s *soundSvc) moveTrigger(dir int) SoundSettings {
	return s.update(func(st *SoundSettings) {
		total := st.PreS + st.PostS
		st.PreS = math.Max(0, math.Min(total-0.1, st.PreS+float64(dir)*total/20))
		st.PostS = total - st.PreS
	})
}

// pushTakeSettings hands the take's times and its trigger's source to the
// engine (the gate's level needs the stream's scale: the service step).
// TRLC-LINKS: REQ-SDS-275
func (s *soundSvc) pushTakeSettings(set SoundSettings) {
	if t := s.taker(); t != nil {
		t.SetSoundTakeTimes(set.PreS, set.PostS)
		if !set.TrigGate {
			t.SetSoundTrigGate(engine.SoundMarkConfig{}, false)
		}
	}
}

// manageTakeGate sets the take's trigger to SOUND's gate (its channel and
// level, rising) while the setting asks for it.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-268
func (s *soundSvc) manageTakeGate(e soundEngine, set SoundSettings) {
	t := s.taker()
	if t == nil || !set.TrigGate {
		return
	}
	ch := set.Ch
	if set.Gate >= 0 {
		ch = set.Gate
	}
	_, b := s.latest(e, 1, ch)
	if len(b.Scales) == 0 {
		return
	}
	t.SetSoundTrigGate(engine.SoundMarkConfig{Ch: ch, Level: s.codeOf(b, ch, set.GateV), Hyst: 2, Rising: true}, true)
}

// soundTakeWidth is the columns the LCD draws a take's window in.
const soundTakeWidth = 780

// takePaneLocked is the take for the LCD's plot: columns of the window's
// lowest and highest volts of each channel, the trigger, where the window
// sits (s.mu held).
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-273
func (s *soundSvc) takePaneLocked(width int) *wsview.SoundTake {
	tk := s.tv.tk
	if tk == nil || width < 16 {
		return nil
	}
	fs := tk.RateHz
	key := [4]float64{float64(tk.ID), s.tv.from, s.tv.span, float64(width)}
	if s.tv.cols.key != key {
		s.tv.cols = s.takeColumns(tk, width)
		s.tv.cols.key = key
	}
	c := s.tv.cols
	out := &wsview.SoundTake{Lo: c.lo, Hi: c.hi, VLo: c.vlo, VHi: c.vhi, TrigX: -1,
		From: s.tv.from / float64(tk.N), To: (s.tv.from + s.tv.span) / float64(tk.N)}
	t0 := float64(tk.First)
	if tk.HasTrig {
		t0 = float64(tk.Trig)
		if at := float64(tk.Trig-tk.First) - s.tv.from; at >= 0 && at <= s.tv.span {
			out.TrigX = at / s.tv.span
		}
		out.TrigCh = tk.TrigCh
	}
	start := (float64(tk.First) + s.tv.from - t0) / fs
	what := map[string]string{"stop": "STOP", "single": "SINGLE", "normal": "NORMAL"}[tk.Why]
	out.Title = fmt.Sprintf("%s take %.1f s  view %s at %+.3f s", what, float64(tk.N)/fs, si(s.tv.span/fs, "s"), start)
	out.Hint = "Position: scroll  push: trigger/all  S/DIV: zoom  RUN: live"
	return out
}

// takeColumns reduces the window to width columns of both channels' lowest
// and highest volts (sound.MinMaxColumns).
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-273
func (s *soundSvc) takeColumns(tk *engine.SoundTake, width int) soundTakeCols {
	a := int(s.tv.from)
	b := min(tk.N, int(math.Ceil(s.tv.from+s.tv.span)))
	blk := engine.SoundBlock{First: tk.First + uint64(a), N: b - a, Q1: tk.Q1[a:b], Q2: tk.Q2[a:b], RateHz: tk.RateHz, Scales: tk.Scales}
	var out soundTakeCols
	for ch := 0; ch < 2; ch++ {
		x := s.volts(blk, ch, nil)
		lo, hi := sound.MinMaxColumns(x, width)
		out.lo[ch], out.hi[ch] = make([]float32, len(lo)), make([]float32, len(hi))
		vlo, vhi := math.Inf(1), math.Inf(-1)
		for i := range lo {
			out.lo[ch][i], out.hi[ch][i] = float32(lo[i]), float32(hi[i])
			vlo, vhi = math.Min(vlo, lo[i]), math.Max(vhi, hi[i])
		}
		if !(vhi > vlo) {
			vlo, vhi = vlo-1e-3, vhi+1e-3
		}
		if math.IsInf(vlo, 0) || math.IsInf(vhi, 0) {
			vlo, vhi = -1, 1
		}
		pad := 0.05 * (vhi - vlo)
		out.vlo[ch], out.vhi[ch] = vlo-pad, vhi+pad
	}
	return out
}
