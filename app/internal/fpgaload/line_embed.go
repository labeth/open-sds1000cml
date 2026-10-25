//go:build withlinebitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed line.rbf
var lineRBF []byte

// Line is the line-code protocol image (ADR-IMAGE-REGROUP-DECIMATION): the
// SRAM capture ABI with the Manchester trigger, the sequence trigger,
// envelope recall and box decimation (ADR-TRIGGER-ANY-TIMEBASE).
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Line() []byte { return lineRBF }
