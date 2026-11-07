// Package webscope is the SDS1000CML+ as the shared web server sees it: the
// core's web scope (scopeweb) over this scope's engine, frame fan-out,
// vertical front end and panel controller, with the hooks for the state the
// engine keeps outside the core's instrument model, and the scope's own
// routes. It is a pure producer/consumer against the engine: it only calls
// staging setters and reads frame copies and stat snapshots — never the bus
// (spec 09 §1).
// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package webscope

import (
	"time"

	"github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/scopeweb"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/buildinfo"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Engine is the engine surface the web server needs (the setters come from
// *engine.Engine; WithFrame comes from the frames.Fanout — the arena's
// single-consumer read slot belongs to the fan-out, and every other reader
// works on its snapshot under the fan-out lock).
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
type Engine interface {
	instrument.Controls
	WithFrame(fn func(*sample.Frame))
	QuietRLock()   // block during the engine's load-sensitive windows (settle+drain)
	QuietRUnlock() // release
	SetFramePeriod(ms int) int
	SetStreamMode(on bool) bool
	SetZones(z []engine.Zone)
	SetZoneMode(m int)
	SetMask(m *engine.Mask)
	SetMaskMode(m int)
	ClearMaskFails()
	MaskFails() []engine.MaskFail
	SetSerialParams(p engine.SerialParams)
	SetSerialMode(m int)
	SetBodeMode(on bool, refCh, dutCh int)
	ClearBode()
	BodePoints() []engine.BodePoint

	// Realtime acquisition checker (instrumentation only).
	AcqLog(n int) ([]engine.AcqSample, float64)
	CmdLog(n int) []engine.CmdNote
	NoteCmd(name string, val float64)

	// Live tuning (debug /api/debug/tune) of the frame-rate and CPU knobs.
	Tune(engine.TuneVals) engine.TuneVals
	TuneSnapshot() engine.TuneVals
}

// Analog is the vertical front-end surface (implemented by
// *analog.FrontEnd). It is producer-direct — off the GPMC bus — so the web
// layer drives it without going through the engine (spec 09 §1). May be nil
// when the SPI nodes are unavailable.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
type Analog interface {
	SetVdivV(ch int, v float64) error
	VdivV(ch int) float64  // at the BNC
	RangeV(ch int) float64 // the analog range's V/div the codes are on
	Zoom(ch int) int
	Emitted() bool
	CalSource() string
	DCVolts(ch int, meanCode float64) float64
	SetProbe(ch int, x float64)
	ProbeFactor(ch int) float64
	SetCoupling(ch, mode int) error
	Coupling(ch int) int
	// The v1 page's trigger level is the comparator DAC code: the status
	// document gives its volts and the calibration the page converts a drag
	// with (triglevelcode, trig_zero, trig_cpv).
	TrigVolts(code uint16, srcCh int) float64
	TrigCalActive(srcCh int) (zero, cpv float64)
}

// Levels is the board's trigger level and offsets: offsets in volts at the
// BNC, and the v1 page's trigger control as the DAC code it sends.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162
type Levels interface {
	SetTrigLevelCode(code uint16) uint16
	SetOffset(ch int, v float64) float64
	OffsetIn(ch int, st engine.Stats) float64
}

// Panel is the front-panel injection surface (spec 08 §6): drive any button or
// knob over the API so only the physical matrix decode needs a real press.
// TRLC-LINKS: REQ-SDS-162
type Panel interface {
	InjectButton(name string) bool
	InjectKnob(name string, dir, steps int) bool
}

// superresReporter is the optional device super-res status surface (the panel
// Controller implements it). Handlers type-assert so test doubles can omit it.
// TRLC-LINKS: REQ-SDS-164
type superresReporter interface {
	SuperresStatus() (active, review bool, bits float64, frames, rejected int, status string)
}

// frameWaiter is the optional long-poll surface: implemented by main's
// scopeSource (delegating to the fan-out's WaitNext). Test doubles without
// it degrade to a short seq poll.
// TRLC-LINKS: REQ-SDS-163
type frameWaiter interface {
	WaitNextFrame(last uint64, timeout time.Duration) uint64
}

// busSampler is the engine's lease of bus samples: while it holds, the
// frames are records sampled at the interval leased.
type busSampler interface {
	LeaseBusSamples(float64, time.Duration)
}

// Scope is the SDS1000CML+ as webserver.Scope: the core's web scope
// (scopeweb) over this scope's engine, with the hooks for what the engine
// still keeps outside the core's instrument model. The status document is
// the engine's own counters (engine.Stats) and the front end's state; the
// controls are the engine's setters, the front end's and the board's
// levels (run control in the engine's flags, the trigger level as the DAC
// code the v1 page sends); the scales are the front end's analog range at
// 25 codes a division with the offset read back from its DAC code. Each
// goes to the core when the instrument model takes that state.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
type Scope struct {
	*scopeweb.Scope
	sc     Engine
	fe     Analog
	lv     Levels
	panel  Panel
	screen func() []byte // PNG of the current LCD render (device-screen view)

	// invSrc reports the per-channel display-invert state (SCPI Cn:INVS).
	// The SCPI handler's shadow is the single source of truth; main wires
	// this to scpi.Handler.Inverted via SetInvertSource before serving.
	// Nil (tests, no SCPI) means no inversion.
	invSrc func() [2]bool

	// setups is the numbered setup store behind /api/setup (SetSetupMemory).
	setups webserver.Setups
}

// New is the SDS1000CML+ scope over the engine sc and the board's levels lv;
// fe and panel may be nil, and screen renders the LCD as a PNG.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func New(sc Engine, fe Analog, lv Levels, panel Panel, screen func() []byte) *Scope {
	s := &Scope{sc: sc, fe: fe, lv: lv, panel: panel, screen: screen}
	cfg := scopeweb.Config{
		Frames:   frameSource{s},
		Version:  buildinfo.String(),
		Screen:   screen,
		Routes:   s.extensions(),
		Parts:    s.parts(),
		Status:   s.status,
		Controls: controlSetter{s},
		Scales:   scales{s},
		Quiet:    sc,
	}
	if panel != nil {
		cfg.UI = panel
	}
	s.Scope = scopeweb.New(cfg)
	return s
}

// parts are the core engine's parts this engine runs, as far as it (or a
// test double) has them.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-265, REQ-SDS-018
func (s *Scope) parts() scopeweb.Parts {
	p := scopeweb.Parts{
		Zones:  s.sc,
		Serial: serialTrigger{s},
		Masks:  maskTest{s},
		Bode:   s.sc,
		Leases: leases{s},
	}
	if src, ok := s.sc.(scopeweb.SoundStream); ok {
		p.Sound = src
	}
	if tk, ok := s.sc.(scopeweb.SoundTaker); ok {
		p.SoundTakes = tk
	}
	if a, ok := s.panel.(scopeweb.SoundArbiter); ok {
		p.SoundLease = a
	}
	if sp, ok := s.panel.(soundPanel); ok {
		p.SoundControl = soundControls{s: s, sp: sp}
	}
	if cd, ok := s.sc.(scopeweb.ClientDecoder); ok {
		p.ClientDecode = cd
	}
	if bs, ok := s.sc.(busSampler); ok {
		p.RecordLease = bs.LeaseBusSamples
	}
	return p
}

// SetInvertSource wires the SCPI INVS shadow — the single source of truth for
// display-level trace inversion — into the /api/status snapshot (inv1/inv2),
// which the page applies in its trace draw path. Call before serving.
// TRLC-LINKS: REQ-SDS-164
func (s *Scope) SetInvertSource(fn func() [2]bool) { s.invSrc = fn }

// SetSetupMemory wires the setup store behind /api/setup: settings.Slots,
// the same store as the front panel's SAVE/RECALL key and SCPI *SAV/*RCL, so
// a setup saved in the browser recalls on the scope. Call before serving;
// without it the endpoint answers that setup memory is unavailable.
// TRLC-LINKS: REQ-SDS-219
func (s *Scope) SetSetupMemory(m webserver.Setups) { s.setups = m }

// Setups is the setup store wired with SetSetupMemory.
// TRLC-LINKS: REQ-SDS-219
func (s *Scope) Setups() (webserver.Setups, bool) { return s.setups, s.setups != nil }

// frameSource is the engine's fan-out as the web scope reads it.
type frameSource struct{ s *Scope }

// TRLC-LINKS: REQ-SDS-163
func (f frameSource) WithFrame(fn func(*sample.Frame)) { f.s.sc.WithFrame(fn) }

// WaitNext parks until a frame after since has published, or timeout.
// TRLC-LINKS: REQ-SDS-163
func (f frameSource) WaitNext(since uint64, timeout time.Duration) uint64 {
	if fw, ok := f.s.sc.(frameWaiter); ok {
		return fw.WaitNextFrame(since, timeout)
	}
	// test doubles: degrade to a short seq poll
	var seq uint64
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		f.s.sc.WithFrame(func(fr *sample.Frame) {
			if fr != nil {
				seq = fr.Seq
			}
		})
		if seq != since {
			return seq
		}
		time.Sleep(20 * time.Millisecond)
	}
	return seq
}
