// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
)

// The Holdoff row steps Off and 1-2-5 up to its 10 s limit from the scope's
// Position knob, not in decades stopping at 1 s.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-011
func TestHoldoffStepsOneTwoFiveToTenSeconds(t *testing.T) {
	c, _, _ := newC(t)
	press(t, c, "trigmenu")
	goRow(t, c, "trig.holdoff")
	var seen []string
	for i := 0; i < 20; i++ {
		seen = append(seen, rowText(c, "trig.holdoff"))
		turn(c, "horizpos", 1)
	}
	want := "Off 100us 200us 500us 1ms 2ms 5ms 10ms 20ms 50ms 100ms 200ms 500ms 1s 2s 5s 10s 10s"
	if got := strings.Join(seen[:18], " "); got != want {
		t.Fatalf("the ladder:\n got %s\nwant %s", got, want)
	}
}
