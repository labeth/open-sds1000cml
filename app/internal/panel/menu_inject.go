// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"time"
)

// nameCode maps a physical button's API name to its code (the /api/panel
// and SCPI inject surface; the names are stable).
// TRLC-LINKS: REQ-SDS-135
func nameCode(name string) (int, bool) {
	switch strings.ToLower(name) {
	case "run", "runstop":
		return btnRunStop, true
	case "single":
		return btnSingle, true
	case "auto":
		return btnAuto, true
	case "ch1vdivpush": // CH1 V/DIV push (Variable)
		return btnCh1VdivPush, true
	case "ch2vdivpush": // CH2 V/DIV push (Variable)
		return btnCh2VdivPush, true
	case "triglvlpush": // TRIG LEVEL push (Zero): level 0 V
		return btnTrigLvlPush, true
	case "utility", "util": // the BUS tab
		return btnUtility, true
	case "adjustpush", "intensity": // the Adjust push: the workspace's tool
		return btnAdjustPsh, true
	case "f1":
		return btnF1, true
	case "f2":
		return btnF2, true
	case "f3":
		return btnF3, true
	case "f4":
		return btnF4, true
	case "f5":
		return btnF5, true
	case "trigmenu", "trigger":
		return btnTrigMenu, true
	case "acquire", "acq":
		return btnAcquire, true
	case "display", "disp":
		return btnDisplay, true
	case "horizmenu", "horiz":
		return btnHorizMenu, true
	case "menu":
		return btnMenuOnOff, true
	case "measure", "meas":
		return btnMeasure, true
	case "cursors", "cursor":
		return btnCursors, true
	case "math":
		return btnMath, true
	case "ref":
		return btnRef, true
	case "ch1":
		return btnCh1, true
	case "ch2":
		return btnCh2, true
	case "print", "hardcopy":
		return btnPrint, true
	case "saverecall":
		return btnSaveRecall, true
	case "set50":
		return btnSet50, true
	case "defaultsetup":
		return btnDefaultSetup, true
	case "force":
		return btnForce, true
	case "help":
		return btnHelp, true
	case "ch1pospush": // CH1 POSITION push (Zero)
		return btnCh1PosPush, true
	case "ch2pospush": // CH2 POSITION push (Zero)
		return btnCh2PosPush, true
	case "tdivpush": // S/DIV push (Zoom)
		return btnTdivPush, true
	case "horizpospush": // HORIZONTAL POSITION push (Zero)
		return btnHorizPosPush, true
	}
	return 0, false
}

var knobNames = map[string]bool{
	"tdiv": true, "ch1vdiv": true, "ch2vdiv": true, "ch1pos": true,
	"ch2pos": true, "triglevel": true, "adjust": true, "horizpos": true,
}

// InjectButton runs a named button press on the panel goroutine (non-blocking).
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) InjectButton(name string) bool {
	code, ok := nameCode(name)
	if !ok {
		return false
	}
	select {
	case c.inject <- func() { c.button(code) }:
		return true
	default:
		return false
	}
}

// InjectButtonWait runs a named button on the panel goroutine and waits
// (bounded) until it has run. *RST needs this: its DEFAULT SETUP used to land
// after the commands that followed it, so "*RST;TDIV 2US" ended at 500 us
// (bug hunt #2).
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) InjectButtonWait(name string, timeout time.Duration) bool {
	code, ok := nameCode(name)
	if !ok {
		return false
	}
	done := make(chan struct{})
	select {
	case c.inject <- func() { c.button(code); close(done) }:
	case <-time.After(timeout):
		return false
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// InjectKnob runs a named knob step (dir ±1, steps≥1) on the panel goroutine.
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) InjectKnob(name string, dir, steps int) bool {
	if !knobNames[name] {
		return false
	}
	if steps < 1 {
		steps = 1
	}
	if dir >= 0 {
		dir = 1
	} else {
		dir = -1
	}
	select {
	case c.inject <- func() { c.resync(); c.dispatch(name, dir, steps) }:
		return true
	default:
		return false
	}
}
