//go:build !withstackbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-141
func Stack() []byte { return nil }
