// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strconv"
	"time"
)

// The screen saver (ADR-SCREEN-SAVER): after the Screen saver time with no
// input the LCD shows a small live scope drifting over black, after the
// Screen off time it is black with its backlight off. Input is anything run
// on the panel goroutine; a key or knob on the scope that wakes the screen
// does nothing else.

// Screen is what the LCD shows.
type Screen int

const (
	ScreenOn    Screen = iota // the workspace
	ScreenSaver               // the drifting live scope
	ScreenOff                 // black, backlight off
)

// String names the state as the panel status reports it.
// TRLC-LINKS: REQ-SDS-221
func (s Screen) String() string {
	switch s {
	case ScreenSaver:
		return "saver"
	case ScreenOff:
		return "off"
	}
	return "on"
}

// The rows' choices in minutes (0 = Off), and the five on F1..F5.
var (
	saverMins  = []int{0, 1, 2, 5, 10, 15, 30, 60}
	saverQuick = []int{0, 5, 15, 30, 60}
	offMins    = []int{0, 5, 10, 15, 30, 60, 120, 240}
	offQuick   = []int{0, 15, 30, 60, 120}
)

// The factory times.
const (
	defSaverMin = 15
	defOffMin   = 60
)

// wakeGrace is how long after a key or knob wakes the screen the scope's
// keys and knobs still only wake it, so a knob turned to wake the screen
// does not also change a setting.
const wakeGrace = 500 * time.Millisecond

// minuteOpts are a row's choices: Off, then minutes up to an hour, then hours.
// TRLC-LINKS: REQ-SDS-221
func minuteOpts(mins []int) []opt {
	out := make([]opt, len(mins))
	for i, m := range mins {
		out[i] = opt{m, minuteText(m)}
	}
	return out
}

// minuteText is a time as the rows write it: Off, 5 min, 1 h.
// TRLC-LINKS: REQ-SDS-221
func minuteText(m int) string {
	switch {
	case m <= 0:
		return "Off"
	case m%60 == 0:
		return strconv.Itoa(m/60) + " h"
	}
	return strconv.Itoa(m) + " min"
}

// oneOf is m when it is one of the choices, else def (a hand-edited or
// future settings file never sets a time the rows cannot show).
// TRLC-LINKS: REQ-SDS-221
func oneOf(m int, choices []int, def int) int {
	for _, c := range choices {
		if c == m {
			return m
		}
	}
	return def
}

// screenLocked is the screen's state at now (c.mu held). The idle time
// starts at the first look when no input has come yet.
// TRLC-LINKS: REQ-SDS-221
func (c *Controller) screenLocked(now time.Time) Screen {
	if c.lastInput.IsZero() {
		c.lastInput = now
	}
	idle := now.Sub(c.lastInput)
	switch {
	case c.offMin > 0 && idle >= time.Duration(c.offMin)*time.Minute:
		return ScreenOff
	case c.saverMin > 0 && idle >= time.Duration(c.saverMin)*time.Minute:
		return ScreenSaver
	}
	return ScreenOn
}

// Screen is what the LCD shows now (the LCD loop asks every tick).
// TRLC-LINKS: REQ-SDS-221
func (c *Controller) Screen() Screen {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.screenLocked(c.now())
}

// noteInput books an input and wakes the screen. It reports true when a key
// or knob on the scope (scope) must do nothing else: it woke the screen, or
// came within wakeGrace of the one that did. A remote input always goes on
// to do its job.
// TRLC-LINKS: REQ-SDS-221
func (c *Controller) noteInput(scope bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	asleep := c.screenLocked(now) != ScreenOn
	c.lastInput = now
	if !scope {
		return false
	}
	if asleep {
		c.wakeUntil = now.Add(wakeGrace)
		return true
	}
	return now.Before(c.wakeUntil)
}
