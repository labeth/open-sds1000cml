// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The planner accepts the stream triggers' patterns: an error token, and a
// value within the unit; the manual element is out of every unit's range.
// TRLC-LINKS: REQ-SDS-018
func TestStreamTriggerPatternsPlan(t *testing.T) {
	p := SerialParams{Proto: core.SerUART, Baud: 115200, ChA: 1, HaveThr: true, Threshold: 169}
	p.Pattern = []PatternElem{{Kind: core.PatternError}}
	if c := acq.PlanSequence(p); c.Length != 1 || c.Elements[0].Kind != sramcapture.SequenceError {
		t.Fatalf("error trigger: %+v", c)
	}
	p.Pattern = []PatternElem{{Kind: core.PatternData, Value: 0x55, Mask: core.UnitMask(p)}}
	if c := acq.PlanSequence(p); c.Length != 1 || c.Elements[0].Value != 0x55 {
		t.Fatalf("value trigger: %+v", c)
	}
	if streamNever <= 0x1fffffff {
		t.Fatal("manual element inside a decoder unit's range")
	}
}

// A stream left for live captures keeps no record for the display (its
// recall held the next capture for seconds), and a stream started again holds
// its image until the next capture begins.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func TestStreamLeftForLiveOrStartedAgain(t *testing.T) {
	e := webSessionEngine(t, false, time.Second)
	e.clk = core.RealClock()
	e.adopt = &adoptedRecord{}
	e.DiscardDecodedRecord()
	if e.takeAdopted() != nil {
		t.Fatal("the discarded record still goes to the display")
	}
	got := make(chan error, 1)
	go func() { got <- e.HoldDecodedImage(context.Background()) }()
	for done := false; !done; {
		e.serviceDecodedRequests()
		select {
		case err := <-got:
			if err != nil {
				t.Fatal(err)
			}
			done = true
		case <-time.After(time.Millisecond):
		}
	}
	if !e.clk.Now().Before(e.decodedStarting) || e.decodedStarting.After(e.clk.Now().Add(11*time.Second)) {
		t.Fatalf("image held until %v", e.decodedStarting)
	}
}
