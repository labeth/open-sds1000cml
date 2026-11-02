//go:build !withpacketbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Packet is the embedded packet image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Packet() []byte { return nil }
