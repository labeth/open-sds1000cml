// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The owner loop's own cost a frame, the fabric answering from memory: arm,
// poll, recall, condition, place the trigger, publish. The record memory is
// the replay test's recorder with its log off and its payloads kept, so
// what is measured is the loop and the board between it and the fabric.
// TRLC-LINKS: REQ-SDS-008
func BenchmarkOwnerLoopFrames(b *testing.B) {
	for _, bc := range []struct {
		name string
		tdiv float64
	}{
		{"envelope", 500e-6}, // the live envelope and its edge windows
		{"raw", 1e-6},        // a full-rate screen
		{"decimated", 5e-3},  // a decimated envelope
	} {
		b.Run(bc.name, func(b *testing.B) {
			now := time.Unix(1000, 0)
			clock := Clock{Now: func() time.Time { return now }, Sleep: func(d time.Duration) { now = now.Add(d) }}
			capture, err := sramcapture.New(&decodedTestBus{})
			if err != nil {
				b.Fatal(err)
			}
			log := &callLog{off: true}
			e := New(Config{Bus: &recordedCS3{fakeBus: newFakeBus(), log: log}, SRAM: capture, Clock: clock, Logf: func(string, ...any) {}})
			rec := &memRecorder{log: log, caps: map[string]bool{"envelope": true, "box": true}, e: e, triggers: always, fill: 2, cache: map[string][]byte{}}
			useRecorder(e, rec)
			e.SetTdiv(bc.tdiv)
			const warm = 3
			rec.stopAt = warm + b.N
			rec.hooks = map[int]func(*Engine){warm: func(*Engine) { b.ResetTimer() }}
			e.Run()
			b.StopTimer()
			if rec.arm < rec.stopAt {
				b.Fatalf("%d arms of %d", rec.arm, rec.stopAt)
			}
		})
	}
}

// useRecorder makes rec the engine's record memory.
func useRecorder(e *Engine, rec *memRecorder) { e.useMemory(rec) }

// A look at the settings while a capture waits (the loop's Watch every
// SettingsPollEvery: a forced AUTO waits 100 ms and more, a serial trigger
// for its match), the settings unchanged since the pass planned the
// capture: an edge trigger, and MIDI on the general image's UART receiver.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-013
func BenchmarkSettingsLook(b *testing.B) {
	for _, bc := range []struct {
		name   string
		serial bool
	}{{"edge", false}, {"midi", true}} {
		b.Run(bc.name, func(b *testing.B) {
			e, _, _ := newPacketTestEngine(&testing.T{})
			e.running.Store(true)
			if bc.serial {
				e.SetSerialParams(SerialParams{Proto: core.SerMIDI, Baud: 31250, ChA: 0, HaveThr: true, Threshold: 140, Bytes: []int{0x90}})
				e.SetSerialMode(SerialTrigger)
				e.serviceImage()
			}
			c := &sramCycle{e: e}
			if _, ok := c.Plan(context.Background()); !ok {
				b.Fatal("not planned")
			}
			if bc.serial && !c.cfg.UART.Enabled {
				b.Fatalf("MIDI not on the UART receiver: %+v", c.cfg.UART)
			}
			for b.Loop() {
				if c.Watch(true) != core.Continue {
					b.Fatal("the settings look changed")
				}
			}
		})
	}
}
