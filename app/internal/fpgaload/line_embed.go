//go:build withlinebitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed line.rbf
var lineRBF []byte

// Line is the line-code protocol image (ADR-IMAGE-REGROUP-DECIMATION): the
// SRAM capture ABI with Manchester, MIL-1553, USB and SENT triggers, the
// sequence trigger and envelope recall, raw samples only.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Line() []byte { return lineRBF }
