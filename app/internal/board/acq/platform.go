// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"encoding/json"
	"fmt"

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
// capture armed the front end's settings staged through FrontEnd().Set and
// the relay and gain words its own setters left (a detent's change
// re-stages its offset DAC), then on the panel plane the lamp word,
// the offset DACs and the trigger level DAC; inside a read (as a Readout
// yield) the lamp word only.
// TRLC-LINKS: REQ-SDS-001
func (p *platformFacet) Service() {
	b := (*Board)(p)
	if b.reading {
		b.panel.flushLamps()
		return
	}
	if b.state != hal.Armed && b.frontEnd != nil {
		wrote, err := b.frontEnd.write()
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
// TRLC-LINKS: REQ-SDS-001
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
