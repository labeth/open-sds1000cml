// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"time"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/panel"
)

// SOUND (ADR-SOUND) on the scope's side of the web server: the engine's
// audio stream, which every page request keeps leased for a few seconds
// (when the last page goes, the stream stops and the scope captures
// again), and the panel's SOUND service. The stream takes the whole
// acquisition, so the last one to act wins: a key on the LCD takes it back
// and the pages' requests are refused (the page paused) until one takes
// the scope again (the action "take_scope").

// soundSource is the engine's audio stream (engine/sound_stream.go).
type soundSource interface {
	LeaseSound(time.Duration)
	SoundInfo() engine.SoundInfo
	SoundRead(from uint64, limit int, q1, q2 []uint16) engine.SoundBlock
	SoundWait(after uint64, timeout time.Duration) uint64
}

// soundPanel is the panel's SOUND service (panel/sound.go).
type soundPanel interface {
	SoundSettings() panel.SoundSettings
	SetSoundSettings(panel.SoundSettings) panel.SoundSettings
	SoundAction(string) error
	SoundReport(api, marks bool) panel.SoundReport
	SoundWant(api, marks bool)
}

// soundLeaser is the panel's arbitration of the stream between the LCD and
// the pages (panel/ws_sound.go).
type soundLeaser interface {
	SoundRemoteLease(who string, lease func()) bool
}

// soundLease is how long a request keeps the stream running.
const soundLease = 3 * time.Second

// probeOf is channel ch's probe factor (1 without a front end).
// TRLC-LINKS: REQ-SDS-265
func (s *Scope) probeOf(ch int) float64 {
	if s.fe == nil {
		return 1
	}
	if p := s.fe.ProbeFactor(ch); p > 0 {
		return p
	}
	return 1
}

// scalesOut is the scales as volts at the probe tip.
// TRLC-LINKS: REQ-SDS-265
func (s *Scope) scalesOut(scales []engine.SoundScale) []wire.SoundScale {
	var out []wire.SoundScale
	for _, sc := range scales {
		var o wire.SoundScale
		o.From = sc.From
		for ch := range 2 {
			vpc, off := sc.SoundVolts(ch)
			p := s.probeOf(ch)
			o.VPC[ch], o.Off[ch] = vpc*p, off*p
		}
		out = append(out, o)
	}
	return out
}

// soundTaker is the engine's takes (engine/sound_take.go).
type soundTaker interface {
	SoundTake() *engine.SoundTake
}

// Sound is the engine's audio stream, held through the panel's arbitration
// between the LCD and the pages.
// TRLC-LINKS: REQ-SDS-265
func (s *Scope) Sound() (webserver.Sound, bool) {
	src, ok := s.sc.(soundSource)
	if !ok {
		return nil, false
	}
	snd := soundStream{s: s, src: src}
	if tk, ok := s.sc.(soundTaker); ok {
		return soundWithTakes{snd, tk}, true
	}
	return snd, true
}

// SoundControls is the panel's SOUND service; its report carries the
// stream's state from the engine.
// TRLC-LINKS: REQ-SDS-274
func (s *Scope) SoundControls() (webserver.SoundControls, bool) {
	sp, ok := s.panel.(soundPanel)
	return soundControls{s: s, sp: sp}, ok
}

// soundStream is the engine's stream as the pages read it.
type soundStream struct {
	s   *Scope
	src soundSource
}

// Lease holds the stream for a page's request: through the panel, which
// refuses it while the LCD holds the acquisition (the page is paused), or
// directly without one.
// TRLC-LINKS: REQ-SDS-277, REQ-SDS-265
func (sn soundStream) Lease(who string) string {
	if sl, ok := sn.s.panel.(soundLeaser); ok {
		if !sl.SoundRemoteLease(who, func() { sn.src.LeaseSound(soundLease) }) {
			return panel.SoundPausedText
		}
		return ""
	}
	sn.src.LeaseSound(soundLease)
	return ""
}

// Info is the stream's state.
// TRLC-LINKS: REQ-SDS-265
func (sn soundStream) Info() wire.SoundInfo {
	in := sn.src.SoundInfo()
	return wire.SoundInfo{
		State: in.State, Active: in.Active, RateHz: in.RateHz, Next: in.Next, Losses: in.Losses,
		Marks: in.Marks, MarkCh: in.MarkCfg.Ch, MarkLevel: in.MarkCfg.Level, Err: in.Err,
	}
}

// Read copies the stream from ordinal from, its scales as volts at the tip.
// TRLC-LINKS: REQ-SDS-265
func (sn soundStream) Read(from uint64, limit int) wire.SoundBlock {
	return sn.s.soundBlock(sn.src.SoundRead(from, limit, nil, nil))
}

// Wait parks until sample after has come, or timeout.
// TRLC-LINKS: REQ-SDS-265
func (sn soundStream) Wait(after uint64, timeout time.Duration) { sn.src.SoundWait(after, timeout) }

// soundWithTakes is the stream with the engine's takes.
type soundWithTakes struct {
	soundStream
	tk soundTaker
}

// Take is the take shown; nil with none.
// TRLC-LINKS: REQ-SDS-275
func (st soundWithTakes) Take() *wire.SoundTake {
	t := st.tk.SoundTake()
	if t == nil {
		return nil
	}
	return &wire.SoundTake{
		SoundBlock: st.s.soundBlock(t.SoundBlock), ID: t.ID, Why: t.Why,
		HasTrig: t.HasTrig, Trig: t.Trig, TrigCh: t.TrigCh, TrigRising: t.TrigRising, TrigLevel: t.TrigLevel,
	}
}

// soundBlock is an engine block with its scales as volts at the tip.
// TRLC-LINKS: REQ-SDS-265
func (s *Scope) soundBlock(b engine.SoundBlock) wire.SoundBlock {
	return wire.SoundBlock{
		First: b.First, N: b.N, Q1: b.Q1, Q2: b.Q2, RateHz: b.RateHz,
		Scales: s.scalesOut(b.Scales), Gaps: b.Gaps, Marks: b.Marks, Next: b.Next,
	}
}

// soundControls is the panel's SOUND service.
type soundControls struct {
	s  *Scope
	sp soundPanel
}

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundSettings() panel.SoundSettings { return c.sp.SoundSettings() }

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SetSoundSettings(v panel.SoundSettings) panel.SoundSettings {
	return c.sp.SetSoundSettings(v)
}

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundAction(name string) error { return c.sp.SoundAction(name) }

// SoundReport is the panel's report with the stream's state now (the
// service reads it only while it runs).
// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundReport(api, marks bool) any {
	rep := c.sp.SoundReport(api, marks)
	if src, ok := c.s.sc.(soundSource); ok {
		rep.Info = src.SoundInfo()
	}
	return rep
}

// TRLC-LINKS: REQ-SDS-274
func (c soundControls) SoundWant(api, marks bool) { c.sp.SoundWant(api, marks) }
