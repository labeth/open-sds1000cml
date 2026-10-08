//go:build withgeneralbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

//go:embed general.rbf
var generalRBF []byte

// General is separate from Default: their register ABIs are incompatible.
// TRLC-LINKS: REQ-SDS-005
func General() []byte { return generalRBF }
