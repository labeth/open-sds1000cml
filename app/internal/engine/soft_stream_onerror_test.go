// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/sramcapture"
)

// A symbol bus's stream asked to stop on an error (the Stream trigger's
// default, so every symbol bus from Default setup) runs until stopped by
// hand and says so: the FPGA's symbol trigger matches no decode error, and
// the refusal left the Stream view failed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSymbolStreamOnErrorRunsByHand(t *testing.T) {
	capture, err := sramcapture.New(&decodedTestBus{})
	if err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := New(Config{Bus: newFakeBus(), SRAM: capture, Clock: clk.clock(), Logf: t.Logf})
	done := make(chan error, 1)
	go func() {
		_, err := e.beginSoftStream(context.Background(), SerialParams{Proto: serLIN, Baud: 19200, HaveThr: true, Threshold: 128}, StreamOnError, 0)
		done <- err
	}()
	var got error
	for wait := true; wait; {
		select {
		case got = <-done:
			wait = false
		default:
			e.serviceDecodedRequests()
			time.Sleep(time.Millisecond)
		}
	}
	if got != nil && strings.Contains(got.Error(), "decode error") {
		t.Fatalf("refused: %v", got)
	}
	s := &softStream{gapFree: true, sampleS: 1e-6, byHand: true}
	if m := s.modeText(0); !strings.Contains(m, "stops by hand") {
		t.Fatalf("mode %q", m)
	}
}
