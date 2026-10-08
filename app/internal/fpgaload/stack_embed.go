//go:build withstackbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed stack.rbf
var stackRBF []byte

// Stack is the edge-locked stacking image (ADR-STACKING-IMAGE-SPLIT). It shares
// the SRAM capture ABI with General and adds the stack port at CS1 109..127.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-141
func Stack() []byte { return stackRBF }
