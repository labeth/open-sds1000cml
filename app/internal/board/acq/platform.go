// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/labeth/open-dso-core/hal"
)

type platformFacet Board

// BringUp reports the unit's identity. The composition loads the general
// image and checks the fabric's revision before it builds the board
// (bringUpFabric), so nothing is loaded here.
// TRLC-LINKS: REQ-SDS-001
func (p *platformFacet) BringUp(context.Context) (hal.Identity, error) {
	b := (*Board)(p)
	if b.mem == nil {
		return hal.Identity{}, hal.Refuse("bring-up", "no SRAM capture fabric answered")
	}
	return b.profile.Identity, nil
}

// Handover is what the supervisor gave the app: the inherited GPMC and key
// descriptors, the health token and the state directory.
// TRLC-LINKS: REQ-SDS-001
func (p *platformFacet) Handover() hal.Handover { return p.handover }

// Service writes what other goroutines staged: at a commit point with no
// capture armed, or while a stream runs (between its words or chunks, as
// the scope's own loops write them), the front end's settings staged
// through FrontEnd().Set and the relay and gain words its own setters left
// (a detent's change re-stages its offset DAC), then on the panel plane the
// lamp word, the offset DACs and the trigger level DAC; inside a read (as a
// Readout yield) the lamp word only.
// TRLC-LINKS: REQ-SDS-001
func (p *platformFacet) Service() {
	b := (*Board)(p)
	if b.timeSvc.Load() {
		t0 := time.Now()
		defer func() { b.noteServiceTime(time.Since(t0)) }()
	}
	if b.reading {
		b.panel.flushLamps()
		return
	}
	if (b.state != hal.Armed || b.streaming()) && b.frontEnd != nil {
		wrote, err := b.writeFrontEnd()
		if err != nil && b.panel.writeErr != nil {
			b.panel.writeErr(fmt.Errorf("front end: %w", err))
		}
		b.stopped = b.stopped || wrote // the next Arm is a commit arm all the same
	}
	b.panel.flush()
}

// Worker is nil: the composition starts the acquisition worker itself
// (bus.Dev's worker), before the board exists.
// TRLC-LINKS: REQ-SDS-001
func (p *platformFacet) Worker() func() int { return nil }

// Reboot is the supervisor's: the app never reboots the unit.
// TRLC-LINKS: REQ-SDS-001
func (p *platformFacet) Reboot(string) error {
	return hal.Refuse("reboot", "the update agent reboots the unit, not the app")
}

// ConverterHealth is what the converter checks found (adc_health.go), as
// the HAL reports it. ANY.
// TRLC-LINKS: REQ-SDS-231
func (p *platformFacet) ConverterHealth() hal.ConverterHealth {
	h := (*Board)(p).ConverterHealth()
	return hal.ConverterHealth{Fault: h.Fault, Recoveries: h.Recoveries, Recovery: h.Recovery, CheckMs: h.CheckMs}
}

// CheckDue reports the periodic converter check due (adc_health.go). ANY
// from the owner's view.
// TRLC-LINKS: REQ-SDS-231
func (p *platformFacet) CheckDue() bool {
	b := (*Board)(p)
	return (b.mem != nil || b.adc.port.Snapshot != nil) && !b.now().Before(b.adc.next)
}

// CheckConverters runs the periodic converter check, with the image loaded
// again as the recovery, and reports whether it was (the record is lost,
// and what the fabric had applied). OWNER, LONG.
// TRLC-LINKS: REQ-SDS-231
func (p *platformFacet) CheckConverters(context.Context) bool {
	b := (*Board)(p)
	n := b.reloads
	b.CheckCoresDue(b.reloadImage)
	return b.reloads != n
}

var (
	_ hal.ConverterMonitor = (*platformFacet)(nil)
	_ hal.ConverterChecker = (*platformFacet)(nil)
)

// Diag is the board's counters.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-128
func (p *platformFacet) Diag() json.RawMessage { return (*Board)(p).diagJSON() }

// StageLamps stages the panel's lamp latch word. ANY.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) StageLamps(word uint16) { b.panel.stageLamps(word) }

// StageOffsetDAC stages channel ch's offset DAC code. ANY.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) StageOffsetDAC(ch int, code uint16) { b.panel.stageOffset(ch, code) }

// StageLevelDAC stages the trigger comparator DAC code. ANY.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) StageLevelDAC(code uint16) { b.panel.stageLevel(code) }

// FlushPanel writes everything staged on the panel plane and reports
// whether a level code was written, and which. OWNER.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) FlushPanel() (level bool, code uint16) { return b.panel.flush() }

// FlushLamps writes a staged lamp word. OWNER.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) FlushLamps() { b.panel.flushLamps() }

// FlushLampsLocked writes a staged lamp word while the owner conditions a
// frame, serialized with the panel scan by the fabric's lock. ANY, only
// while the owner holds no bus access of its own.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) FlushLampsLocked() { b.mem.Locked(b.panel.flushLamps) }

// StartFabric is the fabric's start, the owner's first step on the image
// the composition loaded: its capabilities probed and the converters
// brought up on it, as after every image load. OWNER, LONG.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-231
func (b *Board) StartFabric() {
	if b.mem == nil {
		return
	}
	b.imageLoaded = ImageGeneral
	b.ProbeImage(b.logf)
	b.StartConverters("the start", b.reloadImage)
}

// SetLevelDAC attaches the trigger comparator DAC's code as the
// controllers' level gives it (the levels' code for the instrument
// model's level; ok false while the level was never set: the boot
// comparator stays). Program stages it on the panel plane when it moves.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-098
func (b *Board) SetLevelDAC(fn func() (code uint16, ok bool)) { b.levelDAC = fn }

// stageLevelDAC stages the comparator DAC's code when it moved: the first
// code always, then on change. OWNER.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-098
func (b *Board) stageLevelDAC() {
	if b.levelDAC == nil {
		return
	}
	code, ok := b.levelDAC()
	if !ok || code == 0 || b.dacStaged && code == b.dacCode {
		return
	}
	b.dacCode, b.dacStaged = code, true
	b.StageLevelDAC(code)
}

// NoteBusError counts a bus error and logs the first few and every
// hundredth, as the engine counted its own. ANY.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) NoteBusError(err error) {
	if n := b.busErrors.Add(1); n <= 5 || n%100 == 0 {
		b.logf("engine: bus error #%d: %v", n, err)
	}
}

// BusErrors is the bus errors counted (NoteBusError). ANY.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) BusErrors() uint64 { return b.busErrors.Load() }

// ARINCAux is ARINC 429's slicer as codes, the four levels the last bus
// trigger programmed (0: none learned yet), for a decoded stream's
// receiver. ANY.
// TRLC-LINKS: REQ-SDS-013
func (b *Board) ARINCAux() uint32 { return b.arincAux.Load() }

// SetBeat takes the owner's heartbeat, which the board's LONG work beats
// between its steps (hal.BeatBoard): an image load, a converter recovery,
// a stacking session, a stream's start on its own image. OWNER, once.
// TRLC-LINKS: REQ-SDS-025
func (b *Board) SetBeat(beat func()) { b.beatFn = beat }

// beat beats the owner's heartbeat after a step of LONG work. OWNER.
// TRLC-LINKS: REQ-SDS-025
func (b *Board) beat() {
	if b.beatFn != nil {
		b.beatFn()
	}
}

var _ hal.BeatBoard = (*Board)(nil)

// TimeService has Service time itself while on (a decoded capture's
// service figures); off forgets the longest. ANY.
// TRLC-LINKS: REQ-SDS-127
func (b *Board) TimeService(on bool) {
	if b.timeSvc.Swap(on) && !on {
		b.svcMax.Store(0)
	}
}

// noteServiceTime keeps the longest Service while timed. OWNER.
// TRLC-LINKS: REQ-SDS-127
func (b *Board) noteServiceTime(d time.Duration) {
	if int64(d) > b.svcMax.Load() {
		b.svcMax.Store(int64(d))
	}
}

// ServiceMax is the longest Service since TimeService(true). ANY.
// TRLC-LINKS: REQ-SDS-127
func (b *Board) ServiceMax() time.Duration { return time.Duration(b.svcMax.Load()) }
