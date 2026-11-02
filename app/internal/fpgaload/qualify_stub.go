//go:build !withqualifybitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Qualify is the embedded qualify image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-011
func Qualify() []byte { return nil }
