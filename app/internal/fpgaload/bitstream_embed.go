//go:build withbitstream

package fpgaload

import _ "embed"

// standardRBF is the owned standard bitstream, embedded when building with
// `-tags withbitstream`. The release build copies fpga/standard/acq.rbf here
// first (see the Makefile release target); the file is a hardware build artifact
// and is not committed, so this file only compiles once that copy exists.
//
//go:embed acq.rbf
var standardRBF []byte

// Standard returns the embedded owned bitstream.
func Standard() []byte { return standardRBF }
