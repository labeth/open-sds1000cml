//go:build !withfastbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-229
func Fast() []byte { return nil }
