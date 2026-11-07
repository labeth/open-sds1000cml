// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

// Package board is the SDS1000CML+'s side of the controllers' boundary for
// the levels its DACs hold: the trigger comparator's level and the channels'
// offsets. The controllers (panel, SCPI, web, settings) speak volts; the DAC
// codes, their ranges and the calibration that converts them stay here, in
// the analog front end and in the engine's hardware files.
package board

import (
	"math"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Engine is what the levels need of the engine: the DAC codes it stages at
// the frame boundary, and its state.
type Engine interface {
	Snapshot() engine.Stats
	SetTrigLevelCode(code uint16) uint16
	SetOffsetDAC(ch int, code uint16)
}

// FrontEnd is the analog front end's calibration of the trigger comparator
// and the offset DACs (*analog.FrontEnd).
type FrontEnd interface {
	TrigCode(volts float64, srcCh int) float64
	TrigVolts(code uint16, srcCh int) float64
	SetOffset(ch int, volts float64) uint16
	OffsetVolts(ch int, code uint16) float64
}

// Levels converts volts to the DAC codes the engine stages and back. Without
// a front end (no SPI) the trigger level follows the measured global fit and
// the offsets the boot detent's law.
type Levels struct {
	eng Engine
	fe  FrontEnd // nil: the global fits
}

// New returns the levels over eng and fe; fe may be nil.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-095
func New(eng Engine, fe FrontEnd) *Levels { return &Levels{eng: eng, fe: fe} }

// The measured global trigger fit used without a front end: code =
// trigZeroFit - trigCPVFit·V at the BNC.
const (
	trigZeroFit = 31437.0
	trigCPVFit  = 911.0
)

// trigCodeOf is the DAC code for the trigger level at v volts at source
// src's probe tip, unrounded.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) trigCodeOf(v float64, src int) float64 {
	if l.fe != nil {
		return l.fe.TrigCode(v, src)
	}
	return trigZeroFit - trigCPVFit*v
}

// trigVoltsOf is the level DAC code c holds, in volts at source src's tip.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) trigVoltsOf(c uint16, src int) float64 {
	if l.fe != nil {
		return l.fe.TrigVolts(c, src)
	}
	return engine.TrigLevelVolts(c)
}

// clampCode is code rounded onto the DAC's operating range.
// TRLC-LINKS: REQ-SDS-098
func clampCode(code float64) uint16 {
	return uint16(min(max(math.Round(code), engine.TrigCodeMin), engine.TrigCodeMax))
}

// SetTrigLevel puts the trigger comparator at v volts at the probe tip of
// source src: the nearest DAC step, within the DAC's range. It returns the
// level applied.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-212
func (l *Levels) SetTrigLevel(v float64, src int) float64 {
	return l.trigVoltsOf(l.eng.SetTrigLevelCode(clampCode(l.trigCodeOf(v, src))), src)
}

// TrigLevel is the comparator's level in volts at source src's tip; ok false
// while it was never set (the boot comparator, untouched).
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) TrigLevel(src int) (v float64, ok bool) {
	return l.TrigLevelIn(l.eng.Snapshot(), src)
}

// TrigLevelIn is TrigLevel in engine state st already read.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) TrigLevelIn(st engine.Stats, src int) (v float64, ok bool) {
	if st.TrigCode == 0 {
		return 0, false
	}
	return l.trigVoltsOf(st.TrigCode, src), true
}

// TrigLevelOr is TrigLevel, or the level at v volts (as SetTrigLevel would
// place it) while it was never set.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) TrigLevelOr(v float64, src int) float64 {
	return l.TrigLevelOrIn(l.eng.Snapshot(), v, src)
}

// TrigLevelOrIn is TrigLevelOr in engine state st already read.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) TrigLevelOrIn(st engine.Stats, v float64, src int) float64 {
	if lv, ok := l.TrigLevelIn(st, src); ok {
		return lv
	}
	return l.trigVoltsOf(clampCode(l.trigCodeOf(v, src)), src)
}

// TrigStepsV is n steps of the level's global fit in volts at the BNC: a
// knob's nominal step, whatever the source's own calibration.
// TRLC-LINKS: REQ-SDS-098
func TrigStepsV(n int) float64 { return float64(n) / trigCPVFit }

// StepTrigLevel moves the comparator n DAC steps from where it is, or from
// the level at 0 V while it was never set (positive n raises the level),
// within the DAC's range, and returns the level applied.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-213
func (l *Levels) StepTrigLevel(n, src int) float64 {
	c := l.eng.Snapshot().TrigCode
	if c == 0 {
		c = clampCode(trigZeroFit)
	}
	// The code falls as the level rises.
	nc := min(max(int(c)-n, engine.TrigCodeMin), engine.TrigCodeMax)
	return l.trigVoltsOf(l.eng.SetTrigLevelCode(uint16(nc)), src)
}

// TrigLevelGrid is the level's step and range at source src's tip now: one
// DAC step, the volts of the DAC's ends.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) TrigLevelGrid(src int) hal.Grid {
	lo, hi := l.trigVoltsOf(engine.TrigCodeMax, src), l.trigVoltsOf(engine.TrigCodeMin, src)
	return hal.Grid{Step: (hi - lo) / float64(engine.TrigCodeMax-engine.TrigCodeMin), Min: lo, Max: hi}
}

// SetTrigLevelCode stages comparator DAC code c as given: the v1 web page's
// trigger control, which converts its drag to a code with the calibration
// the status document publishes. It returns the code staged.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-162
func (l *Levels) SetTrigLevelCode(c uint16) uint16 { return l.eng.SetTrigLevelCode(c) }

// TrigLevelSaved is a trigger level kept to put back exactly: a setting
// undone or recalled returns to the same DAC step whatever the vertical
// setting did meanwhile. The zero value is no level (the boot comparator).
type TrigLevelSaved struct{ code uint16 }

// SaveTrigLevel keeps the comparator's level.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) SaveTrigLevel() TrigLevelSaved { return SaveTrigLevelIn(l.eng.Snapshot()) }

// SaveTrigLevelIn keeps the level in engine state st already read.
// TRLC-LINKS: REQ-SDS-098
func SaveTrigLevelIn(st engine.Stats) TrigLevelSaved { return TrigLevelSaved{st.TrigCode} }

// RestoreTrigLevel puts a kept level back; the boot comparator's is left.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) RestoreTrigLevel(s TrigLevelSaved) {
	if s.code != 0 {
		l.eng.SetTrigLevelCode(s.code)
	}
}

// Set reports whether a level was kept (not the boot comparator's).
// TRLC-LINKS: REQ-SDS-098
func (s TrigLevelSaved) Set() bool { return s.code != 0 }

// ResetTrigLevel is the default setup's level: 0 V on C1's calibration, the
// code truncated, not rounded.
// TRLC-LINKS: REQ-SDS-098
func (l *Levels) ResetTrigLevel() {
	l.eng.SetTrigLevelCode(uint16(l.trigCodeOf(0, 0)))
}

// SetOffset stages channel ch's offset at v volts at the BNC (the front end
// re-anchors it on every V/div change); it returns the offset applied, the
// staged DAC code's volts.
// TRLC-LINKS: REQ-SDS-095
func (l *Levels) SetOffset(ch int, v float64) float64 {
	if l.fe != nil {
		return l.fe.OffsetVolts(ch, l.fe.SetOffset(ch, v))
	}
	code := analog.OffsetCode(ch, v)
	l.eng.SetOffsetDAC(ch, code)
	return analog.OffsetVolts(ch, code)
}

// Offset is channel ch's applied offset in volts at the BNC (the staged DAC
// code's); 0 while it was never set (the inherited boot offset).
// TRLC-LINKS: REQ-SDS-095
func (l *Levels) Offset(ch int) float64 {
	st := l.eng.Snapshot()
	return l.offsetOf(ch, st)
}

// OffsetIn is Offset from engine state st already read.
// TRLC-LINKS: REQ-SDS-095
func (l *Levels) OffsetIn(ch int, st engine.Stats) float64 { return l.offsetOf(ch, st) }

// OffsetStagedIn is the volts of the offset DAC code staged in st for
// channel ch through the front end's law, code 0 (nothing staged since boot)
// included; 0 without a front end.
// TRLC-LINKS: REQ-SDS-095
func (l *Levels) OffsetStagedIn(ch int, st engine.Stats) float64 {
	if l.fe == nil {
		return 0
	}
	code := st.OffC1
	if ch == 1 {
		code = st.OffC2
	}
	return l.fe.OffsetVolts(ch, code)
}

// offsetOf is channel ch's applied offset in st.
// TRLC-LINKS: REQ-SDS-095
func (l *Levels) offsetOf(ch int, st engine.Stats) float64 {
	code := st.OffC1
	if ch == 1 {
		code = st.OffC2
	}
	if code == 0 {
		return 0
	}
	if l.fe != nil {
		return l.fe.OffsetVolts(ch, code)
	}
	return analog.OffsetVolts(ch, code)
}
