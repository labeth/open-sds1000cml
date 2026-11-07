// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import ui "github.com/labeth/open-dso-core/uimodel/panel"

// Button codes are sel-index<<8 | bit: the selector word (0=0x64, 1=0x65,
// 2=0x66, 3=0x67) and its bit (spec 08 §2).
// TRLC-LINKS: REQ-SDS-135
func bcode(selIdx, bit int) int { return selIdx<<8 | bit }

// keyCodes are the wired keys and knob pushes (spec 08 §2, §6) by the UI
// model's key names. They stay here because they are this board's PCB
// wiring.
var keyCodes = map[int]string{
	bcode(1, 2): ui.KeyRunStop, bcode(1, 10): ui.KeySingle, bcode(3, 10): ui.KeyAuto,
	bcode(1, 5): ui.KeyF1, bcode(1, 13): ui.KeyF2, bcode(2, 5): ui.KeyF3, bcode(2, 13): ui.KeyF4, bcode(3, 5): ui.KeyF5,
	bcode(0, 10): ui.KeyTrigMenu, bcode(0, 11): ui.KeyAcquire, bcode(1, 3): ui.KeyDisplay, bcode(1, 12): ui.KeyHorizMenu,
	bcode(0, 13): ui.KeyMenu, bcode(1, 4): ui.KeyMeasure, bcode(0, 12): ui.KeyCursors, bcode(2, 12): ui.KeyMath,
	bcode(2, 10): ui.KeyDefaultSetup, bcode(3, 12): ui.KeyRef, bcode(1, 11): ui.KeySaveRecall, bcode(3, 13): ui.KeyPrint,
	bcode(3, 11): ui.KeyHelp, bcode(2, 4): ui.KeyCh1, bcode(3, 4): ui.KeyCh2, bcode(2, 3): ui.KeyUtility,
	bcode(2, 2): ui.KeySet50, bcode(3, 2): ui.KeyForce,
	// Knob push-switches (spec 08 §6.3), with their printed jobs.
	bcode(1, 9): ui.KeyCh1VdivPush, bcode(2, 1): ui.KeyCh2VdivPush, bcode(0, 9): ui.KeyTrigLvlPush,
	bcode(0, 1): ui.KeyCh1PosPush, bcode(3, 1): ui.KeyCh2PosPush, bcode(3, 9): ui.KeyHorizPosPush,
	bcode(2, 9): ui.KeyTdivPush, bcode(1, 1): ui.KeyAdjustPush,
}

const knobPhaseMask = 0xC0C0 // encoder phase bits in every selector word

// knobDef is one row of the FIXED FPGA priority order (spec 08 §3): exactly
// one knob is serviced per interrupt — the first whose selector has a low
// phase bit. Direction comes from WHICH bit rests low (never from a phase
// change — that misses sustained rotation).
// TRLC-LINKS: REQ-SDS-135
type knobDef struct {
	name    string
	selIdx  int
	bitLo   uint // low → CW (+1)
	bitHi   uint // low → CCW (−1)
	stepped bool // stepped: 1/detent; continuous: accel-clamped 0x69
}

var knobs = []knobDef{
	{ui.KnobHorizPos, 3, 14, 15, false},
	{ui.KnobCh2Pos, 3, 6, 7, false},
	{ui.KnobTdiv, 2, 14, 15, true},
	{ui.KnobCh2Vdiv, 2, 6, 7, true},
	{ui.KnobCh1Vdiv, 1, 14, 15, true},
	{ui.KnobAdjust, 1, 6, 7, false},
	{ui.KnobTrigLevel, 0, 14, 15, false},
	{ui.KnobCh1Pos, 0, 6, 7, false},
}

// accel maps the raw 0x69 magnitude to steps (continuous knobs): runaway
// guard at 200 FIRST (must be ≥100 or the ≥20→100 row is unreachable).
// TRLC-LINKS: REQ-SDS-135
func accel(raw uint16) int {
	if raw > 200 {
		raw = 200
	}
	switch {
	case raw >= 20:
		return 100
	case raw >= 10:
		return 50
	default:
		return int(raw)
	}
}
