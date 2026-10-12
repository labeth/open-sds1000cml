// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"
	"time"
)

// The screen shows the saver after the Screen saver time without input and
// goes off after the Screen off time; Off never comes, and a Screen off
// shorter than the saver goes straight to off.
// TRLC-LINKS: REQ-SDS-221
func TestScreenSaverTimes(t *testing.T) {
	c, _, _ := newC(t)
	k := withClock(c)
	for i, step := range []struct {
		after time.Duration
		want  Screen
	}{{0, ScreenOn}, {15*time.Minute - time.Second, ScreenOn}, {time.Second, ScreenSaver},
		{45*time.Minute - time.Second, ScreenSaver}, {time.Second, ScreenOff}} {
		k.advance(step.after)
		if got := c.Screen(); got != step.want {
			t.Fatalf("step %d: %v, want %v", i, got, step.want)
		}
	}
	if got := c.PanelStatus().Screen; got != "off" {
		t.Fatalf("the status says %q", got)
	}
	for _, set := range [][2]string{{"sys.saver", "Off"}, {"sys.screen_off", "Off"}} {
		if res := c.SheetSet(set[0], nil, set[1]); !res.OK {
			t.Fatalf("%s %s: %+v", set[0], set[1], res)
		}
	}
	k.advance(10 * time.Hour)
	if got := c.Screen(); got != ScreenOn {
		t.Fatalf("both Off, 10 h later: %v", got)
	}
	c.SheetSet("sys.saver", nil, "30 min")
	c.SheetSet("sys.screen_off", nil, "5 min")
	k.advance(5 * time.Minute)
	if got := c.Screen(); got != ScreenOff {
		t.Fatalf("Screen off before the saver: %v", got)
	}
}

// A key or knob on the scope that wakes the screen does nothing else, nor
// does one in the next half second; after that they work again.
// TRLC-LINKS: REQ-SDS-221, REQ-SDS-135
func TestScreenScopeKeyOnlyWakes(t *testing.T) {
	c, eng, _ := newC(t)
	k := withClock(c)
	c.Screen()
	k.advance(15 * time.Minute)
	run := idle()
	run[1] &^= 1 << 2 // RUN/STOP
	press := func() {
		c.decode(run, true)
		c.decode(idle(), true)
	}
	press()
	if len(eng.calls) != 0 || c.Screen() != ScreenOn {
		t.Fatalf("the press that woke the saver: calls %v, screen %v", eng.calls, c.Screen())
	}
	k.advance(400 * time.Millisecond)
	press()
	if len(eng.calls) != 0 {
		t.Fatalf("a press 0.4 s after the wake: %v", eng.calls)
	}
	k.advance(200 * time.Millisecond)
	press()
	if len(eng.calls) != 1 || eng.calls[0].what != "run" {
		t.Fatalf("a press 0.6 s after the wake: %v", eng.calls)
	}

	k.advance(time.Hour)
	if got := c.Screen(); got != ScreenOff {
		t.Fatalf("an hour later: %v", got)
	}
	tdiv := idle()
	tdiv[2] &^= 1 << 14 // TIME/DIV one detent clockwise
	tdiv[4] = 1
	turn := func() {
		c.decode(tdiv, true)
		c.decode(idle(), true)
	}
	turn()
	if len(eng.calls) != 1 || c.Screen() != ScreenOn {
		t.Fatalf("the detent that woke the dark screen: calls %v, screen %v", eng.calls, c.Screen())
	}
	k.advance(time.Second)
	turn()
	if len(eng.calls) != 2 || eng.calls[1].what != "tdiv" {
		t.Fatalf("the next detent: %v", eng.calls)
	}
}

// A change from the phone or the web's sheets wakes the screen and takes
// effect.
// TRLC-LINKS: REQ-SDS-221, REQ-SDS-220
func TestScreenRemoteWakesAndActs(t *testing.T) {
	c, _, _ := newC(t)
	k := withClock(c)
	c.Screen()
	k.advance(2 * time.Hour)
	if got := c.Screen(); got != ScreenOff {
		t.Fatalf("2 h idle: %v", got)
	}
	if res := c.SheetSet("sys.remote_notices", nil, "Off"); !res.OK {
		t.Fatalf("the remote's set: %+v", res)
	}
	if got := c.Screen(); got != ScreenOn || rowText(c, "sys.remote_notices") != "Off" {
		t.Fatalf("after the remote's set: screen %v, the row %q", got, rowText(c, "sys.remote_notices"))
	}
}

// The rows: their factory times, the choices, a time they do not offer,
// Default Setup leaving them as they are, and the Position push's reset.
// TRLC-LINKS: REQ-SDS-221
func TestScreenRows(t *testing.T) {
	c, _, _ := newC(t)
	withClock(c)
	if a, b := rowText(c, "sys.saver"), rowText(c, "sys.screen_off"); a != "15 min" || b != "1 h" {
		t.Fatalf("factory: saver %q, off %q", a, b)
	}
	for _, set := range [][3]string{{"sys.saver", "1 min", "1 min"}, {"sys.saver", "1 h", "1 h"},
		{"sys.screen_off", "4 h", "4 h"}, {"sys.screen_off", "Off", "Off"}} {
		if res := c.SheetSet(set[0], nil, set[1]); !res.OK || rowText(c, set[0]) != set[2] {
			t.Fatalf("%s %q: %+v, the row %q", set[0], set[1], res, rowText(c, set[0]))
		}
	}
	if res := c.SheetSet("sys.saver", nil, "7 min"); res.OK || rowText(c, "sys.saver") != "1 h" {
		t.Fatalf("7 min is no choice: %+v, the row %q", res, rowText(c, "sys.saver"))
	}
	if res := c.SheetAction("sys.default", ""); !res.OK {
		t.Fatalf("Default setup: %+v", res)
	}
	if a, b := rowText(c, "sys.saver"), rowText(c, "sys.screen_off"); a != "1 h" || b != "Off" {
		t.Fatalf("Default setup changed the screen saver: %q, %q", a, b)
	}
	for _, id := range []string{"sys.saver", "sys.screen_off"} {
		if err := rowByID[id].def(c); err != nil {
			t.Fatal(err)
		}
	}
	if a, b := rowText(c, "sys.saver"), rowText(c, "sys.screen_off"); a != "15 min" || b != "1 h" {
		t.Fatalf("the Position push's factory times: %q, %q", a, b)
	}
}
