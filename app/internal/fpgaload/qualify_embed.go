//go:build withqualifybitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed qualify.rbf
var qualifyRBF []byte

// Qualify is the qualifier image (ADR-HARDWARE-QUALIFIERS): the SRAM capture
// ABI with precision decimation, envelope recall and the pulse, slope and
// video triggers decided in the FPGA; no protocol triggers.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-011
func Qualify() []byte { return qualifyRBF }
