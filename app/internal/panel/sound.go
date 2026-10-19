// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"sync"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/sound"
)

// SOUND (ADR-SOUND) on the scope's side: the settings the LCD, the web page
// and the phone share, and the service that reads the audio stream for what
// runs on the instrument itself: the LCD's SOUND view (tuner, meters, the
// readout pages, the cycle trace), the V/oct table, the drift log, the
// gate-armed envelope capture and the edge marks the FPGA puts in the
// stream. It reads only while something needs it, so an idle SOUND costs
// nothing; the web page and the phone analyse their own copy of the stream
// in the browser.

// SoundSettings are SOUND's choices.
type SoundSettings struct {
	A4       float64 `json:"a4"`        // the tuner's reference, Hz
	Ch       int     `json:"ch"`        // the tuned channel (the VCO in V/oct; its CV on the other)
	Ref      string  `json:"ref"`       // the meters' 0 dB (sound.Presets)
	Range    string  `json:"range"`     // the tuner's range: "", "bass", "high"
	Slow     bool    `json:"slow"`      // the tuner's smoothing
	Page     int     `json:"page"`      // the LCD's readout page (soundPages)
	Cycles   int     `json:"cycles"`    // periods the cycle trace shows: 1, 2 or 4
	ABIn     int     `json:"ab_in"`     // A/B: the input channel (the output on the other)
	EnvCV    bool    `json:"env_cv"`    // the envelope is a CV read directly, not a tone's amplitude
	Gate     int     `json:"gate"`      // the envelope's trigger: -1 the signal's own level, 0 C1, 1 C2 (a gate)
	GateV    float64 `json:"gate_v"`    // its level, volts (the signal's: above its quiet mean)
	VOctAuto bool    `json:"voct_auto"` // take V/oct points when the CV and pitch hold still
	Drift    bool    `json:"drift"`     // log the pitch a second
}

// The LCD's readout pages, the Adjust knob's order.
const (
	soundPageMod = iota
	soundPageADSR
	soundPageAB
	soundPageHarm
	soundPageVOct
	numSoundPages
)

// soundPageNames name the pages.
var soundPageNames = [numSoundPages]string{"MODULATION", "ENVELOPE", "A/B", "HARMONICS", "V/OCT"}

// defaultSoundSettings are the power-on choices.
// TRLC-LINKS: REQ-SDS-273
func defaultSoundSettings() SoundSettings {
	return SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: -1, GateV: 0.1}
}

// clean bounds every field.
// TRLC-LINKS: REQ-SDS-273
func (s SoundSettings) clean() SoundSettings {
	if !(s.A4 >= 400 && s.A4 <= 480) {
		s.A4 = 440
	}
	s.Ch &= 1
	s.ABIn &= 1
	if sound.PresetByID(s.Ref).ID != s.Ref {
		s.Ref = "dbu"
	}
	if s.Range != "bass" && s.Range != "high" {
		s.Range = ""
	}
	s.Page = ((s.Page % numSoundPages) + numSoundPages) % numSoundPages
	if s.Cycles != 1 && s.Cycles != 4 {
		s.Cycles = 2
	}
	if s.Gate < -1 || s.Gate > 1 {
		s.Gate = -1
	}
	if !(s.GateV >= -50 && s.GateV <= 50) {
		s.GateV = 0.1
	}
	return s
}

// soundEngine is the engine's audio stream (engine/sound_stream.go).
type soundEngine interface {
	LeaseSound(time.Duration)
	SoundInfo() engine.SoundInfo
	SoundLatest(n int, q1, q2 []uint16) engine.SoundBlock
	SoundRead(from uint64, max int, q1, q2 []uint16) engine.SoundBlock
	SetSoundMarks(engine.SoundMarkConfig)
	SoundMarksSince(from uint64) []engine.SoundMark
}

// ADSR capture states.
const (
	adsrIdle      = "idle"
	adsrArmed     = "armed"
	adsrCapturing = "capturing"
	adsrDone      = "done"
)

// adsrMaxS bounds a capture (the ring holds 17 s).
const adsrMaxS = 15.0

// soundCycleKeep is how many cycles the trace keeps for its persistence.
const soundCycleKeep = 6

// stripSeconds is the pitch strip's span.
const stripSeconds = 10.0

// voctMax bounds the V/oct table: the automatic points come every time the
// CV steps and holds (a sequencer looping all night), and every report
// copies and fits the table.
const voctMax = 128

// addVOct appends a V/oct point, the oldest going past voctMax (s.mu held).
// TRLC-LINKS: REQ-SDS-267
func (s *soundSvc) addVOct(p sound.VOctPoint) {
	s.voct = append(s.voct, p)
	if n := len(s.voct); n > voctMax {
		s.voct = append(s.voct[:0], s.voct[n-voctMax:]...)
	}
}

// soundTick is the service's period.
const soundTick = 125 * time.Millisecond

// SoundLevel is one channel's meter reading.
type SoundLevel struct {
	OK      bool    `json:"ok"`
	Vpp     float64 `json:"vpp"`
	RMS     float64 `json:"rms"`
	Peak    float64 `json:"peak"`
	DC      float64 `json:"dc"`
	CrestDB float64 `json:"crest_db"`
	DB      float64 `json:"db"`      // RMS against the reference
	PeakDB  float64 `json:"peak_db"` // the peak's sine-RMS equivalent against it
	Clip    bool    `json:"clip"`
}

// stripPoint is one pitch-strip reading.
type stripPoint struct {
	t, cents float64
	ok       bool
}

// soundSvc is the scope's SOUND service.
type soundSvc struct {
	c *Controller

	mu   sync.Mutex
	set  SoundSettings
	info engine.SoundInfo

	// Who wants it, until when: the LCD's view sets lcd, a web or phone page
	// wants the shared readouts (api) or the edge marks (marks).
	apiUntil   time.Time
	marksUntil time.Time
	lcd        bool

	tuner   sound.Tuner
	hz      float64
	clarity float64
	strip   []stripPoint
	phase   float64 // the strobe's position, 0..1
	lastT   float64
	levels  [2]SoundLevel
	cv      float64 // the CV channel's mean (V/oct)
	fs      float64

	mod      sound.Modulation
	lfo      sound.LFO
	sb       sound.Sidebands
	ab       sound.AB
	harm     sound.HarmonicReport
	heavyAt  time.Time
	sbAt     time.Time
	cycles   [][]float64 // newest last, volts
	cycle    sound.Cycle
	cycleAt  uint64 // the mark of the newest cycle drawn
	cycleWhy string
	markLvl  int // the level (code) asked for the cycle marks, -1 none

	adsr     string
	adsrWhy  string
	armedAt  uint64
	gateOn   uint64
	gateOff  uint64
	hasOff   bool
	adsrPeak float64
	armFloor float64 // the quietest level seen while armed: the source's own floor
	quietFor float64
	adsrRes  sound.ADSR
	adsrEnv  []float64
	adsrEFs  float64
	adsrFrom uint64

	voct  []sound.VOctPoint
	auto  sound.VOctAuto
	drift sound.Drift

	q1, q2 []uint16
	x, y   []float64
	t0     time.Time
}

// newSoundSvc makes the service with the power-on settings.
// TRLC-LINKS: REQ-SDS-273
func newSoundSvc(c *Controller) *soundSvc {
	return &soundSvc{c: c, set: defaultSoundSettings(), adsr: adsrIdle, markLvl: -1, t0: time.Now()}
}

// engine is the controller's engine as an audio stream (nil without one).
// TRLC-LINKS: REQ-SDS-265
func (s *soundSvc) engine() soundEngine {
	e, _ := s.c.eng.(soundEngine)
	return e
}

// Settings returns SOUND's settings.
// TRLC-LINKS: REQ-SDS-273
func (s *soundSvc) Settings() SoundSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

// update changes the settings through fn (bounded after).
// TRLC-LINKS: REQ-SDS-273
func (s *soundSvc) update(fn func(*SoundSettings)) SoundSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.set
	fn(&s.set)
	s.set = s.set.clean()
	if s.set.Ch != old.Ch || s.set.Range != old.Range || s.set.Slow != old.Slow {
		s.tuner = sound.Tuner{Slow: s.set.Slow}
		s.strip, s.cycles, s.hz = nil, nil, 0
	}
	if s.set.Drift && !old.Drift {
		s.drift = sound.Drift{}
	}
	return s.set
}

// want marks a page's use of the shared readouts (api) or the edge marks
// for d.
// TRLC-LINKS: REQ-SDS-274
func (s *soundSvc) want(api, marks bool, d time.Duration) {
	s.mu.Lock()
	until := time.Now().Add(d)
	if api {
		s.apiUntil = until
	}
	if marks {
		s.marksUntil = until
	}
	s.mu.Unlock()
}

// setLCD says whether the LCD shows the SOUND view.
// TRLC-LINKS: REQ-SDS-273
func (s *soundSvc) setLCD(on bool) {
	s.mu.Lock()
	s.lcd = on
	s.mu.Unlock()
}

// needed reports whether anything uses the service now, and whether the
// readouts are wanted (beyond the background jobs).
// TRLC-LINKS: REQ-SDS-273
func (s *soundSvc) needed(now time.Time) (any, readouts bool) {
	readouts = s.lcd || now.Before(s.apiUntil)
	any = readouts || now.Before(s.marksUntil) || s.set.VOctAuto || s.set.Drift || s.adsr == adsrArmed || s.adsr == adsrCapturing
	return
}

// run ticks the service until stop.
// TRLC-LINKS: REQ-SDS-273
func (s *soundSvc) run(stop <-chan struct{}) {
	t := time.NewTicker(soundTick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			s.step(now)
		}
	}
}

// step is one tick: lease the stream while needed, read it and update what
// is in use.
// TRLC-LINKS: REQ-SDS-273
func (s *soundSvc) step(now time.Time) {
	e := s.engine()
	if e == nil {
		return
	}
	s.mu.Lock()
	use, readouts := s.needed(now)
	set := s.set
	s.mu.Unlock()
	if !use {
		return
	}
	e.LeaseSound(3 * time.Second)
	info := e.SoundInfo()
	s.mu.Lock()
	s.info = info
	s.fs = info.RateHz
	s.mu.Unlock()
	if !info.Active || info.Next == 0 {
		return
	}
	t := now.Sub(s.t0).Seconds()
	if readouts || set.VOctAuto || set.Drift {
		s.readTuner(e, set, t)
	}
	if readouts {
		s.readLevels(e, set)
	}
	s.runADSR(e, set)
	s.manageMarks(e, set, now)
	if readouts {
		s.readPage(e, set, now)
		s.readCycle(e, set)
	}
}

// volts converts block b's channel ch to volts at the tip, piece by piece
// over its scales.
// TRLC-LINKS: REQ-SDS-265
func (s *soundSvc) volts(b engine.SoundBlock, ch int, dst []float64) []float64 {
	q := b.Q1
	if ch == 1 {
		q = b.Q2
	}
	dst = dst[:0]
	probe := 1.0
	if s.c.fe != nil {
		probe = s.c.fe.ProbeFactor(ch)
	}
	// No scale yet reads codes about mid-scale as volts a code.
	k, vpc, off := -1, 1/256.0*256, 0.0
	for i, v := range q {
		ord := b.First + uint64(i)
		for k+1 < len(b.Scales) && (k < 0 || b.Scales[k+1].From <= ord) {
			k++
			vpc, off = b.Scales[k].SoundVolts(ch)
			vpc, off = vpc*probe, off*probe
		}
		dst = append(dst, (float64(v)-32768)*vpc/256-off)
	}
	return dst
}

// latest is the newest n samples of channel ch in volts (and the block).
// TRLC-LINKS: REQ-SDS-265
func (s *soundSvc) latest(e soundEngine, n, ch int) ([]float64, engine.SoundBlock) {
	b := e.SoundLatest(n, s.q1, s.q2)
	s.q1, s.q2 = b.Q1, b.Q2
	s.x = s.volts(b, ch, s.x)
	// After a gap only what follows it is one signal.
	if k := len(b.Gaps); k > 0 {
		cut := int(b.Gaps[k-1] - b.First)
		return s.x[cut:], b
	}
	return s.x, b
}

// readTuner reads the pitch of the tuned channel and feeds the strip, the
// strobe, the drift log and the V/oct table.
// TRLC-LINKS: REQ-SDS-267
func (s *soundSvc) readTuner(e soundEngine, set SoundSettings, t float64) {
	s.mu.Lock()
	prev, fs := s.hz, s.fs
	s.mu.Unlock()
	n := sound.TunerWindow(prev, fs)
	x, _ := s.latest(e, n, set.Ch)
	var p sound.Pitch
	if len(x) >= n/2 {
		p = sound.DetectPitch(x, fs, sound.PitchRange(set.Range))
	}
	cv := 0.0
	if set.VOctAuto || set.Page == soundPageVOct {
		y, _ := s.latest(e, int(0.1*fs), 1-set.Ch)
		cv = mean(y)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tuner.Slow = set.Slow
	s.hz = s.tuner.Add(p)
	s.clarity = p.Clarity
	s.cv = cv
	cents := 0.0
	if s.hz > 0 {
		cents = sound.NoteOf(s.hz, set.A4).Cents
		dt := t - s.lastT
		if dt > 0 && dt < 1 {
			// The strobe drifts a band a second for every 2 cents off.
			s.phase = math.Mod(s.phase+cents/2*dt+1, 1)
		}
	}
	s.lastT = t
	s.strip = append(s.strip, stripPoint{t: t, cents: cents, ok: s.hz > 0})
	for len(s.strip) > 0 && t-s.strip[0].t > stripSeconds {
		s.strip = s.strip[1:]
	}
	if set.Drift {
		s.drift.Add(t, s.hz)
	}
	if set.VOctAuto {
		if pt, ok := s.auto.Add(t, cv, s.hz); ok {
			s.addVOct(pt)
		}
	}
}

// mean is the average of x.
// TRLC-LINKS: REQ-SDS-266
func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	t := 0.0
	for _, v := range x {
		t += v
	}
	return t / float64(len(x))
}

// readLevels reads both meters over the last fifth of a second.
// TRLC-LINKS: REQ-SDS-266
func (s *soundSvc) readLevels(e soundEngine, set SoundSettings) {
	ref := sound.PresetByID(set.Ref).Vrms
	var lv [2]SoundLevel
	for ch := 0; ch < 2; ch++ {
		x, b := s.latest(e, int(0.2*s.fs), ch)
		if len(x) == 0 {
			continue
		}
		m := sound.Measure(x)
		q := b.Q1
		if ch == 1 {
			q = b.Q2
		}
		lv[ch] = SoundLevel{OK: true, Vpp: m.Vpp, RMS: m.RMS, Peak: m.Peak, DC: m.DC, CrestDB: m.CrestDB,
			DB: sound.DB(m.RMS, ref), PeakDB: sound.DB(m.Peak/math.Sqrt2, ref), Clip: sound.ClipCodes(q)}
	}
	s.mu.Lock()
	s.levels = lv
	s.mu.Unlock()
}

// readPage runs the shown page's analysis: modulation every two seconds
// (its four-second window's demodulation is the heaviest analysis on the
// instrument; a tremolo's figures move slowly), the sidebands with it, A/B
// and harmonics twice a second.
// TRLC-LINKS: REQ-SDS-269, REQ-SDS-270, REQ-SDS-271
func (s *soundSvc) readPage(e soundEngine, set SoundSettings, now time.Time) {
	s.mu.Lock()
	hz, fs, at := s.hz, s.fs, s.heavyAt
	s.mu.Unlock()
	switch set.Page {
	case soundPageMod:
		if now.Sub(at) < 2*time.Second {
			return
		}
		x, _ := s.latest(e, int(4*fs), set.Ch)
		var m sound.Modulation
		var l sound.LFO
		if hz > 0 {
			m = sound.AnalyzeModulation(sound.Demodulate(x, fs, hz, 1000))
		} else {
			l = sound.AnalyzeLFO(x, fs)
		}
		sb := s.sb
		if now.Sub(s.sbAt) >= 2*time.Second && hz > 0 {
			sp := sound.AudioSpectrum(x, fs, 32768)
			sb = sound.AnalyzeSidebands(sp.DBV, sp.BinHz)
			s.sbAt = now
		}
		s.mu.Lock()
		s.mod, s.lfo, s.sb, s.heavyAt = m, l, sb, now
		s.mu.Unlock()
	case soundPageAB:
		if now.Sub(at) < 500*time.Millisecond {
			return
		}
		a, _ := s.latest(e, 16384, set.ABIn)
		a = append([]float64(nil), a...)
		b, _ := s.latest(e, 16384, 1-set.ABIn)
		r := sound.CompareAB(a, b, fs, hz)
		s.mu.Lock()
		s.ab, s.heavyAt = r, now
		s.mu.Unlock()
	case soundPageHarm:
		if now.Sub(at) < 500*time.Millisecond {
			return
		}
		x, _ := s.latest(e, 16384, set.Ch)
		h := sound.Harmonics(sound.AudioSpectrum(x, fs, 16384), hz)
		s.mu.Lock()
		s.harm, s.heavyAt = h, now
		s.mu.Unlock()
	}
}

// codeOf is the 8-bit converter code volts v sits at on channel ch (the
// newest scale), clamped to the converter's range.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268
func (s *soundSvc) codeOf(b engine.SoundBlock, ch int, v float64) uint8 {
	if len(b.Scales) == 0 {
		return 128
	}
	vpc, off := b.Scales[len(b.Scales)-1].SoundVolts(ch)
	probe := 1.0
	if s.c.fe != nil {
		probe = s.c.fe.ProbeFactor(ch)
	}
	if !(vpc > 0) {
		return 128
	}
	c := 128 + (v/probe+off)/vpc
	return uint8(math.Max(2, math.Min(253, math.Round(c))))
}

// manageMarks keeps the FPGA's marks where they are wanted: the envelope's
// gate while it is armed or capturing, else the tuned channel's mid level
// for the cycle trace (the level follows the signal's centre as an
// autoset places a trigger level).
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268
func (s *soundSvc) manageMarks(e soundEngine, set SoundSettings, now time.Time) {
	s.mu.Lock()
	state := s.adsr
	cycles := s.lcd || now.Before(s.marksUntil)
	s.mu.Unlock()
	switch {
	case state == adsrArmed || state == adsrCapturing:
		ch, lvl := set.Ch, 0.0
		x, b := s.latest(e, int(0.3*s.fs), set.Ch)
		if set.Gate >= 0 {
			ch = set.Gate
			_, b = s.latest(e, 1, ch)
			lvl = set.GateV
		} else {
			lvl = mean(x) + set.GateV
			if set.EnvCV {
				lvl = set.GateV
			}
		}
		e.SetSoundMarks(engine.SoundMarkConfig{Ch: ch, Level: s.codeOf(b, ch, lvl), Hyst: 2, Rising: true, Falling: set.Gate >= 0 || set.EnvCV})
		s.mu.Lock()
		s.markLvl = -1
		s.mu.Unlock()
	case cycles:
		x, b := s.latest(e, int(0.2*s.fs), set.Ch)
		if len(x) == 0 {
			return
		}
		m := sound.Measure(x)
		code := int(s.codeOf(b, set.Ch, (m.Min+m.Max)/2))
		lo, hi := int(s.codeOf(b, set.Ch, m.Min)), int(s.codeOf(b, set.Ch, m.Max))
		hyst := uint8(max(1, min(20, (hi-lo)/8)))
		s.mu.Lock()
		moved := s.markLvl < 0 || absI(code-s.markLvl) > 2
		if moved {
			s.markLvl = code
		}
		s.mu.Unlock()
		if moved {
			e.SetSoundMarks(engine.SoundMarkConfig{Ch: set.Ch, Level: uint8(code), Hyst: hyst, Rising: true})
		}
	}
}

// absI is |v|.
// TRLC-LINKS: REQ-SDS-272
func absI(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// readCycle draws the newest marked cycle: the FPGA's mark placed on its
// crossing, then the cycles periods after it. With no mark, nothing is
// drawn (the trigger is the FPGA's alone).
// TRLC-LINKS: REQ-SDS-272
func (s *soundSvc) readCycle(e soundEngine, set SoundSettings) {
	s.mu.Lock()
	hz, fs, info, lvl := s.hz, s.fs, s.info, s.markLvl
	s.mu.Unlock()
	why := ""
	switch {
	case !info.Marks:
		why = "this FPGA image marks no edges in the stream"
	case hz <= 0:
		why = "no pitch to lock to"
	case lvl < 0:
		why = "waiting for the edge marks"
	}
	if why != "" {
		s.mu.Lock()
		s.cycleWhy = why
		s.mu.Unlock()
		return
	}
	per := fs / hz
	span := per * float64(set.Cycles)
	// The newest rising mark with its cycles all in.
	b := e.SoundLatest(int(math.Min(0.5*fs, 4*span+fs*0.05)), s.q1, s.q2)
	s.q1, s.q2 = b.Q1, b.Q2
	var mark *engine.SoundMark
	for i := len(b.Marks) - 1; i >= 0; i-- {
		m := b.Marks[i]
		if m.Rising && m.Ch == set.Ch && float64(m.At)+span+8 < float64(b.First)+float64(b.N) && m.At >= b.First+8 {
			mark = &m
			break
		}
	}
	if mark == nil {
		s.mu.Lock()
		s.cycleWhy = "no edge marked yet"
		s.mu.Unlock()
		return
	}
	if mark.At == s.cycleAt {
		return
	}
	x := s.volts(b, set.Ch, s.x)
	s.x = x
	lvV := 0.0
	if len(b.Scales) > 0 {
		vpc, off := b.Scales[len(b.Scales)-1].SoundVolts(set.Ch)
		probe := 1.0
		if s.c.fe != nil {
			probe = s.c.fe.ProbeFactor(set.Ch)
		}
		lvV = (float64(lvl)-128)*vpc*probe - off*probe
	}
	at, ok := sound.PlaceMark(x, int(mark.At-b.First), lvV, true)
	if !ok {
		s.mu.Lock()
		s.cycleAt, s.cycleWhy = mark.At, "the marked edge has no crossing beside it"
		s.mu.Unlock()
		return
	}
	c := sound.ExtractCycles(x, fs, at, per, set.Cycles, 600)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cycleAt, s.cycleWhy = mark.At, ""
	if !c.OK {
		return
	}
	s.cycle = c
	s.cycles = append(s.cycles, c.Trace)
	if len(s.cycles) > soundCycleKeep {
		s.cycles = s.cycles[len(s.cycles)-soundCycleKeep:]
	}
}

// arm starts (or cancels) a gate-armed envelope capture.
// TRLC-LINKS: REQ-SDS-268
func (s *soundSvc) arm(on bool) {
	e := s.engine()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !on {
		s.adsr, s.adsrWhy = adsrIdle, "cancelled"
		return
	}
	s.adsr, s.adsrWhy, s.hasOff, s.adsrPeak, s.quietFor = adsrArmed, "", false, 0, 0
	s.armFloor = math.Inf(1)
	s.adsrRes, s.adsrEnv = sound.ADSR{}, nil
	if e != nil {
		e.LeaseSound(3 * time.Second)
		// Marks count from a third of a second on: the gate's level and
		// channel reach the FPGA within a service tick and a drain step, and
		// marks of the cycle trace's level before them must not start it.
		in := e.SoundInfo()
		s.armedAt = in.Next + uint64(0.33*in.RateHz)
	}
}

// runADSR advances an armed capture on the FPGA's marks: the first rising
// mark after the arm starts it, a falling one (a gate) ends the gate, and
// the capture ends once the sound has died away or at the bound.
// TRLC-LINKS: REQ-SDS-268
func (s *soundSvc) runADSR(e soundEngine, set SoundSettings) {
	s.mu.Lock()
	state, armedAt, gateOn, fs := s.adsr, s.armedAt, s.gateOn, s.fs
	s.mu.Unlock()
	if state != adsrArmed && state != adsrCapturing {
		return
	}
	gateCh := set.Ch
	if set.Gate >= 0 {
		gateCh = set.Gate
	}
	info := e.SoundInfo()
	if state == adsrArmed {
		if x, _ := s.latest(e, int(0.1*fs), set.Ch); len(x) > 0 && set.Gate >= 0 && !set.EnvCV {
			pk := sound.Measure(x).Peak
			s.mu.Lock()
			s.armFloor = math.Min(s.armFloor, pk)
			s.mu.Unlock()
		}
		for _, m := range e.SoundMarksSince(armedAt) {
			if m.Rising && m.Ch == gateCh && m.At >= armedAt {
				s.mu.Lock()
				s.adsr, s.gateOn = adsrCapturing, m.At
				s.mu.Unlock()
				return
			}
		}
		return
	}
	// Capturing.
	if set.Gate >= 0 || set.EnvCV {
		marks := e.SoundMarksSince(gateOn)
		s.mu.Lock()
		if !s.hasOff {
			for _, m := range marks {
				if !m.Rising && m.Ch == gateCh && m.At > gateOn {
					s.gateOff, s.hasOff = m.At, true
					break
				}
			}
		}
		s.mu.Unlock()
	}
	// The sound's level over the last tenth of a second against the
	// capture's peak so far: quiet for half a second ends it.
	x, _ := s.latest(e, int(0.1*fs), set.Ch)
	l := sound.Measure(x)
	lvl := l.Peak
	if set.EnvCV {
		lvl = math.Abs(l.DC)
	}
	s.mu.Lock()
	s.adsrPeak = math.Max(s.adsrPeak, lvl)
	// Quiet is 3 % of the peak, or above the source's own floor (hum, a
	// DAC's ripple) where that is higher, up to a third of the peak.
	quiet := 0.03 * s.adsrPeak
	if !math.IsInf(s.armFloor, 1) {
		quiet = math.Min(0.3*s.adsrPeak, math.Max(quiet, 2*s.armFloor))
	}
	if lvl < quiet && (s.hasOff || set.Gate < 0 && !set.EnvCV) {
		s.quietFor += soundTick.Seconds()
	} else {
		s.quietFor = 0
	}
	length := float64(info.Next-gateOn) / fs
	done := s.quietFor >= 0.5 || length >= adsrMaxS
	s.mu.Unlock()
	if done {
		s.finishADSR(e, set)
	}
}

// finishADSR reads the capture from a tenth of a second before the gate and
// takes its envelope's reading.
// TRLC-LINKS: REQ-SDS-268
func (s *soundSvc) finishADSR(e soundEngine, set SoundSettings) {
	s.mu.Lock()
	gateOn, gateOff, hasOff, fs := s.gateOn, s.gateOff, s.hasOff, s.fs
	s.mu.Unlock()
	pre := uint64(0.1 * fs)
	from := gateOn - min(gateOn, pre)
	info := e.SoundInfo()
	b := e.SoundRead(from, int(min(info.Next-from, 1<<20)), s.q1, s.q2)
	s.q1, s.q2 = b.Q1, b.Q2
	x := s.volts(b, set.Ch, nil)
	var env []float64
	var efs float64
	if set.EnvCV {
		env, efs = sound.BlockMeans(x, fs, 0.001)
	} else {
		env, efs = sound.Envelope(x, fs, 0.02, 0.001)
	}
	on, off := float64(gateOn-b.First)/fs, -1.0
	if hasOff {
		off = float64(gateOff-b.First) / fs
	}
	r := sound.AnalyzeADSR(env, efs, on, off)
	s.mu.Lock()
	s.adsr, s.adsrRes, s.adsrEnv, s.adsrEFs, s.adsrFrom = adsrDone, r, env, efs, b.First
	if !r.OK {
		s.adsrWhy = r.Why
	}
	s.mu.Unlock()
}

// voctCapture takes a V/oct point now (the Adjust push, or the page's
// button).
// TRLC-LINKS: REQ-SDS-267
func (s *soundSvc) voctCapture() error {
	s.mu.Lock()
	hz, fs, ch := s.hz, s.fs, s.set.Ch
	s.mu.Unlock()
	if !(hz > 0) {
		return fmt.Errorf("no pitch on the VCO's channel")
	}
	cv := 0.0
	if e := s.engine(); e != nil && fs > 0 {
		// the CV now, read here (the service reads it only for the V/oct
		// page and the automatic points)
		cv = mean(s.volts(e.SoundLatest(int(0.1*fs), nil, nil), 1-ch, nil))
	}
	s.mu.Lock()
	s.cv = cv
	s.addVOct(sound.VOctPoint{V: cv, Hz: hz})
	s.mu.Unlock()
	return nil
}

// action runs a SOUND action by name.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-268, REQ-SDS-274
func (s *soundSvc) action(name string) error {
	switch name {
	case "adsr_arm":
		s.arm(true)
	case "adsr_cancel":
		s.arm(false)
	case "voct_capture":
		return s.voctCapture()
	case "voct_undo":
		s.mu.Lock()
		if n := len(s.voct); n > 0 {
			s.voct = s.voct[:n-1]
		}
		s.mu.Unlock()
	case "voct_clear":
		s.mu.Lock()
		s.voct, s.auto = nil, sound.VOctAuto{}
		s.mu.Unlock()
	case "drift_clear":
		s.mu.Lock()
		s.drift = sound.Drift{}
		s.mu.Unlock()
	case "strip_clear":
		s.mu.Lock()
		s.strip = nil
		s.mu.Unlock()
	default:
		return fmt.Errorf("no SOUND action %q", name)
	}
	return nil
}

// SoundReport is what the shared readouts hold for the web page and the
// phone (the service's own state).
type SoundReport struct {
	Info     engine.SoundInfo   `json:"info"`
	Settings SoundSettings      `json:"settings"`
	Hz       float64            `json:"hz"`
	Note     string             `json:"note"`
	Cents    float64            `json:"cents"`
	Clarity  float64            `json:"clarity"`
	Levels   [2]SoundLevel      `json:"levels"`
	CV       float64            `json:"cv"`
	ADSR     string             `json:"adsr"`
	ADSRWhy  string             `json:"adsr_why,omitempty"`
	ADSRRes  sound.ADSR         `json:"adsr_res"`
	ADSREnv  []float64          `json:"adsr_env,omitempty"`
	ADSREFs  float64            `json:"adsr_env_fs"`
	VOct     []sound.VOctPoint  `json:"voct"`
	VOctFit  sound.VOctFit      `json:"voct_fit"`
	Drift    []sound.DriftPoint `json:"drift"`
	// The readout pages' figures, as the LCD's SOUND view last read them (a
	// page reads only while the LCD shows it).
	Mod  sound.Modulation     `json:"mod"`
	LFO  sound.LFO            `json:"lfo"`
	SB   sound.Sidebands      `json:"sidebands"`
	AB   sound.AB             `json:"ab"`
	Harm sound.HarmonicReport `json:"harm"`
}

// driftReportMax bounds the drift points a report carries: a page draws the
// log across its width, and two hours of it (7200 points) cost the scope a
// third of a megabyte of JSON a report.
const driftReportMax = 1000

// reduceDrift is every k-th point of the drift log, at most max of them,
// and always its newest.
// TRLC-LINKS: REQ-SDS-267
func reduceDrift(pts []sound.DriftPoint, max int) []sound.DriftPoint {
	k := (len(pts) + max - 1) / max
	if k <= 1 {
		return append([]sound.DriftPoint(nil), pts...)
	}
	out := make([]sound.DriftPoint, 0, max+1)
	for i := 0; i < len(pts); i += k {
		out = append(out, pts[i])
	}
	if out[len(out)-1] != pts[len(pts)-1] {
		out = append(out, pts[len(pts)-1])
	}
	return out
}

// Report snapshots the shared readouts; the envelope comes reduced to at
// most 2000 points and the drift log to about driftReportMax.
// TRLC-LINKS: REQ-SDS-274
func (s *soundSvc) Report() SoundReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := SoundReport{Info: s.info, Settings: s.set, Hz: s.hz, Clarity: s.clarity, Levels: s.levels, CV: s.cv,
		ADSR: s.adsr, ADSRWhy: s.adsrWhy, ADSRRes: s.adsrRes, ADSREFs: s.adsrEFs,
		VOct: append([]sound.VOctPoint(nil), s.voct...), Drift: reduceDrift(s.drift.Points, driftReportMax),
		Mod: s.mod, LFO: s.lfo, SB: s.sb, AB: s.ab, Harm: s.harm}
	if s.hz > 0 {
		n := sound.NoteOf(s.hz, s.set.A4)
		r.Note, r.Cents = n.String(), n.Cents
	}
	r.VOctFit = sound.FitVOct(s.voct, s.set.A4)
	if k := len(s.adsrEnv); k > 0 {
		step := max(1, (k+1999)/2000)
		for i := 0; i < k; i += step {
			r.ADSREnv = append(r.ADSREnv, s.adsrEnv[i])
		}
		r.ADSREFs = s.adsrEFs / float64(step)
	}
	return r
}
