// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"strings"
	"testing"
	"time"
)

// A phone's SOUND page leasing the audio stream does not start it while a
// BUS decoded stream is starting (its image loads between two owner calls):
// the audio stream lost its words to that load, loaded the stream image
// back, and the BUS stream fought it for the fabric or failed.
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-018
func TestSoundWaitsForAStartingDecodedStream(t *testing.T) {
	e, clk := newTestEngine(t, newFakeBus())
	e.LeaseSound(3 * time.Second)
	e.decodedStarting = clk.clock().Now().Add(30 * time.Second)
	if e.soundStep() {
		t.Fatal("the audio stream started under a starting decoded stream")
	}
	if in := e.SoundInfo(); !strings.Contains(in.State, "decoded stream") || in.Active {
		t.Fatalf("SOUND state %q active %v", in.State, in.Active)
	}
}
