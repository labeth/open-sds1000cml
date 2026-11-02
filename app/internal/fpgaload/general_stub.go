//go:build !withgeneralbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// General is the embedded general image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005
func General() []byte { return nil }
