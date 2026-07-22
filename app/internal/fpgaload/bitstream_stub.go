//go:build !withbitstream

package fpgaload

// Standard returns the embedded owned bitstream, or nil when the binary was
// built without one. The default build embeds nothing: the standard .rbf is a
// hardware build artifact (candidate pin assignment, ~360 KB) that is not
// committed, so an ordinary `go build` produces a verify-only binary that runs
// only against a fabric already carrying the standard build. Build the shipping
// binary with `-tags withbitstream` (see the Makefile release target), which
// embeds fpga/standard/acq.rbf and lets the binary configure a cold-boot fabric.
func Standard() []byte { return nil }
