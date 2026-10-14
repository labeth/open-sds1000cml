// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"testing"

	"open-sds/app/internal/engine"
)

// The trigger readout writes the armed match as the BUS table does.
// TRLC-LINKS: REQ-SDS-168
func TestMatchText(t *testing.T) {
	if got := matchText(engine.SerialParams{Bytes: []int{0xAA, 0x0F}}); got != "AA 0F" {
		t.Fatalf("bytes: %q", got)
	}
	p := engine.SerialParams{Pattern: []engine.PatternElem{{Kind: 2, Value: 0xA0, Mask: 0xF0}, {Kind: 4}, {Kind: 2, Value: 0x55, Mask: 0xFF}}}
	if got := matchText(p); got != "A? ERR 55" {
		t.Fatalf("pattern: %q", got)
	}
}
