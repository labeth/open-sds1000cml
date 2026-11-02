//go:build !withfastbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Fast is the embedded fast image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-229
func Fast() []byte { return nil }
