// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"testing"

	core "github.com/labeth/open-dso-core/engine"
)

// A look at the settings while a capture waits knows the staged capture
// without composing it, and still sees every change: of the trigger, of
// the plan, of the serial request; a capture the board staged since the
// pass is compared whole.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011, REQ-SDS-013
func TestSettingsLookSeesChanges(t *testing.T) {
	e, _, _ := newPacketTestEngine(t)
	e.running.Store(true)
	c := &sramCycle{e: e}
	plan := func() {
		t.Helper()
		if _, ok := c.Plan(context.Background()); !ok {
			t.Fatal("not planned")
		}
		if c.Watch(true) != core.Continue {
			t.Fatal("the planned capture is taken for a change")
		}
	}
	look := func(what string, want core.Interrupt) {
		t.Helper()
		if got := c.Watch(true); got != want {
			t.Fatalf("%s: look %v, want %v", what, got, want)
		}
	}
	plan()
	rising := !e.trigRising.Load()
	e.SetTrigSlope(rising)
	look("the slope", core.Abort)
	plan()
	e.SetTrigPosFrac(.3)
	look("the trigger position", core.Abort)
	plan()
	midi := SerialParams{Proto: core.SerMIDI, Baud: 31250, ChA: 0, HaveThr: true, Threshold: 140, Bytes: []int{0x90}}
	e.SetSerialParams(midi)
	e.SetSerialMode(SerialTrigger)
	e.serviceImage()
	look("the serial trigger", core.Abort)
	plan()
	if !c.cfg.UART.Enabled {
		t.Fatalf("MIDI not on the UART receiver: %+v", c.cfg.UART)
	}
	midi.Bytes = []int{0x80}
	e.SetSerialParams(midi)
	look("the serial bytes", core.Abort)
	plan()
	e.SetSerialMode(SerialOff)
	plan()
	// The board staged another trigger since the pass took its capture,
	// then the pass's again.
	e.SetTrigSlope(!rising)
	e.planCapture(boardTarget{e})
	e.SetTrigSlope(rising)
	look("the board's other capture", core.Continue)
	e.planCapture(boardTarget{e})
	look("the pass's capture staged again", core.Continue)
	e.SetTrigSlope(!rising)
	look("the slope after a restage", core.Abort)
}
