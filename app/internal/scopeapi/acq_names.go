// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package scopeapi

import (
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// The capture plan arithmetic and the record's frame format are the
// acquisition board's; the scope's packages name them through this one.

// CodeFormat is the converters' code format as the frames carry it.
var CodeFormat = acq.CodeFormat

// CodesPerDiv and RollCodesPerDiv are the acquisition board's code scales.
const (
	CodesPerDiv     = acq.CodesPerDiv
	RollCodesPerDiv = acq.RollCodesPerDiv
)

// HalfScale is acq.HalfScale.
// TRLC-LINKS: REQ-SDS-010
func HalfScale(f *sample.Frame) bool { return acq.HalfScale(f) }

// FullRateTdiv is acq.FullRateTdiv.
// TRLC-LINKS: REQ-SDS-010
func FullRateTdiv(tdivs []float64) float64 { return acq.FullRateTdiv(tdivs) }

// PlanPrecisionSRAM is acq.PlanPrecisionSRAM.
// TRLC-LINKS: REQ-SDS-010
func PlanPrecisionSRAM(tdiv float64, log uint8) acq.SRAMPlan { return acq.PlanPrecisionSRAM(tdiv, log) }

// PlanSRAM is acq.PlanSRAM.
// TRLC-LINKS: REQ-SDS-010
func PlanSRAM(tdiv float64) acq.SRAMPlan { return acq.PlanSRAM(tdiv) }

// SPIDelayFPGA is acq.SPIDelayFPGA.
// TRLC-LINKS: REQ-SDS-010
func SPIDelayFPGA(ns float64, clockCh int) float64 { return acq.SPIDelayFPGA(ns, clockCh) }

// SRAMPlan is acq.SRAMPlan.
type SRAMPlan = acq.SRAMPlan

// InterleaveCalibration is the acquisition board's converter calibration.
type InterleaveCalibration = acq.InterleaveCalibration
