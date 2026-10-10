//go:build withstreambitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed stream.rbf
var streamRBF []byte

// Stream is the stream image (ADR-STREAM-IMAGE): the SRAM capture ABI with
// precision decimation and the continuous stream banks (revision 11), no
// protocol triggers and no envelope recall. Roll mode runs on it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-035
func Stream() []byte { return streamRBF }
