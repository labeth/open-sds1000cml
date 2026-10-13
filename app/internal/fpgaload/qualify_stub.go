//go:build !withqualifybitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-011
func Qualify() []byte { return nil }
