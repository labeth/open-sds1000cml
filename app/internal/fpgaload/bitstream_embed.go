//go:build withbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import _ "embed"

// defaultRBF is the default acq2 image, embedded by `make app-release`, which
// copies ../fpga/default/out/default.rbf here first. The file is a build
// product (ignored by git); this file compiles only once that copy exists.
//
//go:embed default.rbf
var defaultRBF []byte

// Default returns the embedded default image.
// TRLC-LINKS: REQ-SDS-005
func Default() []byte { return defaultRBF }
