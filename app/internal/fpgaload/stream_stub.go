//go:build !withstreambitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-035
func Stream() []byte { return nil }
