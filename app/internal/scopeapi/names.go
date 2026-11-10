// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

// Package scopeapi is the SDS1000CML+'s scope vocabulary as its app, web,
// SCPI, panel and settings share it: the control and status types (the
// core engine's and the instrument model's), the timebase ladder, the
// trigger level's DAC codes and the board's planning helpers.
package scopeapi

import (
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/superres"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/iface"
)

// TrigCodeMin and TrigCodeMax clamp the UI trigger-level code. Spec 05 §1.2's
// 27000-35000 was the analog comparator DAC's window, about +4.9/-3.9 V:
// the SRAM images trigger on sample codes, and that window held the
// level inside ±2 div at 2 V/div and ±0.4 div at 10 V/div. The full 16
// bits reach about +34.5/-37.4 V; 0 still means no comparator.
const (
	TrigCodeMin = 1
	TrigCodeMax = 65535
)

// TrigLevelVolts converts a DAC code to approximate volts using the measured
// global fit (docs/trigcal-notes.md): code = 31437 − 911·V.
// TRLC-LINKS: REQ-SDS-016
func TrigLevelVolts(code uint16) float64 { return (31437 - float64(code)) / 911 }

// maxRecordCols is the most samples a native-fast record holds.
const maxRecordCols = iface.PretrigMax

// RollMinTdiv is the fastest timebase AUTO rolls at (slower ones roll too): from
// here a screen spans 100 ms or more, too long for 10 frames a second.
const RollMinTdiv = 10e-3

// SearchLiveSamples bounds a bus search's decimated screen: four times the
// live view's, so a screen of 20ms/div still samples every 8 us (a Wiegand
// pulse of 50 us spans six samples, an IR remote's 562 us mark 68), read in
// about a tenth of a second.
const SearchLiveSamples = 32768

// AcqSample is one capture as the realtime acquisition checker recorded
// it: the core engine's record.
type AcqSample = core.AcqSample

// CmdNote records one web set-control invocation (name + numeric value +
// the published Seq at the time), so the acq log can be read against "what we
// did just before". Pure instrumentation.
// TRLC-LINKS: REQ-SDS-127
type CmdNote struct {
	Name string  `json:"name"`
	Val  float64 `json:"val"`
	Seq  uint64  `json:"at_seq"`
}

// TuneVals is the live-tunable knob set (the board's tuning).
type TuneVals = acq.TuneVals

type (
	// FPGAStackRequest is the core's hardware stacking session.
	FPGAStackRequest = superres.StackRequest
	// FPGAStackResult is its outcome.
	FPGAStackResult = superres.StackResult
)
