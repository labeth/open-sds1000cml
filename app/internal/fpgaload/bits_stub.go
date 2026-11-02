//go:build !withbitsbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Bits is the embedded bits image: none in a build without it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-035
func Bits() []byte { return nil }
