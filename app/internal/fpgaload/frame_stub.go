//go:build !withframebitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Frame is the embedded frame image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Frame() []byte { return nil }
