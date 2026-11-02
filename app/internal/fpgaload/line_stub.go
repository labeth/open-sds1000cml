//go:build !withlinebitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Line is the embedded line image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Line() []byte { return nil }
