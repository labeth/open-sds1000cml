//go:build !withframebitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Frame() []byte { return nil }
