//go:build withbitsbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed bits.rbf
var bitsRBF []byte

// Bits is the digital stream image (ADR-STREAM-BITS): the stream image with
// each line sliced at its level, sixteen periods a word, in the peak words'
// place; the decoded stream runs on it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-035
func Bits() []byte { return bitsRBF }
