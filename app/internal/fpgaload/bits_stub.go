//go:build !withbitsbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-035
func Bits() []byte { return nil }
