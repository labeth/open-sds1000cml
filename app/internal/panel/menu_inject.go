package panel

import "strings"

func nameCode(name string) (int, bool) {
	switch strings.ToLower(name) {
	case "run", "runstop":
		return btnRunStop, true
	case "single":
		return btnSingle, true
	case "auto":
		return btnAuto, true
	case "ch1vdivpush": // push CH1 V/DIV knob → trigger source C1
		return btnCh1VdivPush, true
	case "ch2vdivpush": // push CH2 V/DIV knob → trigger source C2
		return btnCh2VdivPush, true
	case "triglvlpush": // push TRIG LEVEL knob → flip slope rise/fall
		return btnTrigLvlPush, true
	case "utility", "util": // toggle super-res like SINGLE (arm ⇄ cancel)
		return btnUtility, true
	case "adjustpush", "intensity": // ADJUST/intensity knob push → toggle SR review
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
	}
	return 0, false
}

var knobNames = map[string]bool{
	"tdiv": true, "ch1vdiv": true, "ch2vdiv": true, "ch1pos": true,
	"ch2pos": true, "triglevel": true, "adjust": true, "horizpos": true,
}

// InjectButton runs a named button press on the panel goroutine (non-blocking).
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

// InjectKnob runs a named knob step (dir ±1, steps≥1) on the panel goroutine.
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
