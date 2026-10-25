//go:build withframebitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed frame.rbf
var frameRBF []byte

// Frame is the MIL-1553, SENT and USB protocol image (ADR-TRIGGER-ANY-TIMEBASE):
// the SRAM capture ABI with their triggers, the sequence trigger, envelope
// recall and box decimation.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Frame() []byte { return frameRBF }
