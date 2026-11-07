// Package webscope is the SDS1000CML+ as the shared web server sees it: it
// implements open-dso-core's webserver.Scope over the engine, the frame
// fan-out, the vertical front end and the panel controller, and adds the
// scope's own routes. It is a pure producer/consumer against the engine: it
// only calls staging setters and reads frame copies and stat snapshots —
// never the bus (spec 09 §1).
// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package webscope

import (
	"time"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/shape"
	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Engine is the engine surface the web server needs (the setters come from
// *engine.Engine; WithFrame comes from the frames.Fanout — the arena's
// single-consumer read slot belongs to the fan-out, and every other reader
// works on its snapshot under the fan-out lock).
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
type Engine interface {
	Snapshot() engine.Stats
	WithFrame(fn func(*sample.Frame))
	QuietRLock()   // block during the engine's load-sensitive windows (settle+drain)
	QuietRUnlock() // release
	SetRunning(bool)
	SetNorm(bool)
	SetTdiv(float64) (engine.Band, bool)
	SetTrigSlope(rising bool)
	SetTrigSource(ch int)
	SetETS(on bool)
	SetTrigType(t int)
	SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int)
	SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int)
	SetVideoParams(std, line int, neg bool)
	SetAcqMode(m int)
	SetAvgCount(n int)
	SetEresLen(l int)
	SetSingle()
	SetTrigPosFrac(frac float64)
	SetMemDepth(samples int) int
	SetFramePeriod(ms int) int
	SetStreamMode(on bool) bool
	SetHoldoff(sec float64) float64
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
// scopeSource (delegating to frames.Fanout.WaitNext). Handlers type-assert
// for it so test doubles without it degrade to a short seq poll.
// TRLC-LINKS: REQ-SDS-163
type frameWaiter interface {
	WaitNextFrame(last uint64, timeout time.Duration) uint64
}

// Scope is the SDS1000CML+ as webserver.Scope. Frame reads happen inside
// Engine.WithFrame; a reply is fully assembled under the fan-out read lock
// and the server writes it after the lock is released.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
type Scope struct {
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

	// meas is the frame measurements' cache: computed once per published
	// frame and its measurement inputs, shared by every request.
	meas shape.MeasCache
}

// New is the SDS1000CML+ scope over the engine sc and the board's levels lv;
// fe and panel may be nil, and screen renders the LCD as a PNG.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func New(sc Engine, fe Analog, lv Levels, panel Panel, screen func() []byte) *Scope {
	return &Scope{sc: sc, fe: fe, lv: lv, panel: panel, screen: screen}
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

// UI is the panel controller: its keys and knobs, and the remote surfaces
// it implements (webserver.UIRemote, UIWorkspaces, UIAutoFor, UIKeys,
// UIPageStream, UIPageLeave).
// TRLC-LINKS: REQ-SDS-162
func (s *Scope) UI() (webserver.UIModel, bool) { return s.panel, s.panel != nil }

// Refs is the panel's saved references (and webserver.RefActions where it
// can change them).
// TRLC-LINKS: REQ-SDS-138, REQ-SDS-227
func (s *Scope) Refs() (webserver.Refs, bool) {
	r, ok := s.panel.(webserver.Refs)
	return r, ok
}

// Stream is the panel's decoded stream transcript.
// TRLC-LINKS: REQ-SDS-018
func (s *Scope) Stream() (webserver.Stream, bool) {
	if s.panel == nil {
		return nil, false
	}
	return stream{s.panel}, true
}

// Records takes sampled records where the engine leases its bus samples.
// TRLC-LINKS: REQ-SDS-280
func (s *Scope) Records() (webserver.Records, bool) {
	l, ok := s.sc.(busSampler)
	return records{s, l}, ok
}

// Triggers is the engine's zone and protocol triggers.
// TRLC-LINKS: REQ-SDS-162
func (s *Scope) Triggers() (webserver.Triggers, bool) { return triggers{s}, true }

// Masks is the engine's mask test.
// TRLC-LINKS: REQ-SDS-162
func (s *Scope) Masks() (webserver.Masks, bool) { return masks{s}, true }

// Bode is the engine's frequency-response curve.
// TRLC-LINKS: REQ-SDS-162
func (s *Scope) Bode() (webserver.Bode, bool) { return bode{s.sc}, true }

// Screen is the LCD as rendered.
// TRLC-LINKS: REQ-SDS-161
func (s *Scope) Screen() (webserver.Screen, bool) { return screenPNG(s.screen), s.screen != nil }

// Setups is the setup store wired with SetSetupMemory.
// TRLC-LINKS: REQ-SDS-219
func (s *Scope) Setups() (webserver.Setups, bool) { return s.setups, s.setups != nil }

// screenPNG is the LCD render as webserver.Screen.
type screenPNG func() []byte

// TRLC-LINKS: REQ-SDS-161
func (f screenPNG) PNG() []byte { return f() }

// bode is the engine's Bode curve as wire points.
type bode struct{ sc Engine }

// TRLC-LINKS: REQ-SDS-162
func (b bode) BodePoints() []wire.BodePoint {
	pts := b.sc.BodePoints()
	out := make([]wire.BodePoint, len(pts))
	for i, p := range pts {
		out[i] = wire.BodePoint{FreqHz: p.FreqHz, GainDB: p.GainDB, PhaseDeg: p.PhaseDeg}
	}
	return out
}

// waitFrame parks until a frame after since has published, or timeout.
// TRLC-LINKS: REQ-SDS-163
func (s *Scope) waitFrame(since uint64, timeout time.Duration) {
	if fw, ok := s.sc.(frameWaiter); ok {
		fw.WaitNextFrame(since, timeout)
		return
	}
	// test doubles: degrade to a short seq poll
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		var seq uint64
		s.sc.WithFrame(func(f *sample.Frame) {
			if f != nil {
				seq = f.Seq
			}
		})
		if seq != since {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
