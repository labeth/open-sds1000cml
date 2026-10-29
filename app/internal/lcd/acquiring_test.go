// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"strings"
	"testing"
)

// The screen says ACQUIRING while a record fills, and WAITING FOR TRIGGER,
// with the trigger and the seconds, while a trigger the FPGA decides waits;
// after a few seconds it adds what to try.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-206
func TestAcquiringLines(t *testing.T) {
	if l := acquiringLines(HUD{RecordS: 2}); len(l) != 1 || !strings.HasPrefix(l[0], "ACQUIRING") || !strings.Contains(l[0], "record") {
		t.Fatalf("filling: %q", l)
	}
	l := acquiringLines(HUD{Waiting: true, SerialArmed: true, SerialMatch: "SOF ??", WaitS: 3})
	if len(l) != 2 || l[0] != "WAITING FOR TRIGGER" || !strings.Contains(l[1], "serial match SOF ??") || !strings.Contains(l[1], "3 s") {
		t.Fatalf("serial, 3 s: %q", l)
	}
	l = acquiringLines(HUD{Waiting: true, SerialArmed: true, WaitS: 7})
	if len(l) != 3 || !strings.Contains(l[2], "AUTO") {
		t.Fatalf("serial, 7 s: %q", l)
	}
	l = acquiringLines(HUD{Waiting: true, TrigSrc: 1, TrigRising: true, TrigLvlV: 1.65, WaitS: 9})
	if len(l) != 3 || !strings.Contains(l[1], "C2 rising") || !strings.Contains(l[2], "level") {
		t.Fatalf("edge, 9 s: %q", l)
	}
}
