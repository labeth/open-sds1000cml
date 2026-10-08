//go:build withpacketbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed packet.rbf
var packetRBF []byte

// Packet is the packet protocol image (ADR-PROTOCOL-PACKET-IMAGE): the SRAM
// capture ABI with ARINC 429, CAN and FlexRay triggers and raw samples only.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-013
func Packet() []byte { return packetRBF }
