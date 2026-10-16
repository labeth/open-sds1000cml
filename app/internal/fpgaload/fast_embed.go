//go:build withfastbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed fast.rbf
var fastRBF []byte

// Fast is the image with SPI decoded and triggered on the samples to a
// 125 MHz clock (ADR-SAMPLE-DOMAIN-SPI): the SRAM capture ABI, raw samples,
// envelope recall, decoded events and the sequence trigger.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-229
func Fast() []byte { return fastRBF }
