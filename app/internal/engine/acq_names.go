// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// The capture plan arithmetic and the record's frame format are the
// acquisition board's; the scope's packages keep naming them through the
// engine.

// CodeFormat is the converters' code format as the frames carry it.
var CodeFormat = acq.CodeFormat

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

type SRAMPlan = acq.SRAMPlan
