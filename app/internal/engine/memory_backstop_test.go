// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"runtime/debug"
	"testing"

	"open-sds/app/internal/sramcapture"
)

// The SRAM loop (the instrument's) runs with the memory backstop above a
// stopped deep record's working set, as the register loop does: under the
// start's 48 MB limit the collector ran without end after a long session.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-006
func TestSRAMLoopSetsTheMemoryBackstop(t *testing.T) {
	before := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(before) })
	debug.SetMemoryLimit(48 << 20)
	capture, err := sramcapture.New(&decodedTestBus{})
	if err != nil {
		t.Fatal(err)
	}
	e := New(Config{Bus: newFakeBus(), SRAM: capture, Logf: t.Logf}) // the real clock: the device's policy
	if !e.armBusy {
		t.Fatal("a real-clock engine is the device's")
	}
	e.stopReq.Store(true)
	e.Run()
	if got := debug.SetMemoryLimit(-1); got < memBackstopMin {
		t.Fatalf("memory limit %d MB, want at least %d MB", got>>20, memBackstopMin>>20)
	}
}
