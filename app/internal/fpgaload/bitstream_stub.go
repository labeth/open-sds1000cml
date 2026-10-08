//go:build !withbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

// Default returns the embedded default image, or nil when the binary was built
// without one (`make app`). Such a binary verifies the fabric but cannot
// configure it: it runs only against a fabric already carrying the default
// image. `make app-release` builds with -tags withbitstream.
// TRLC-LINKS: REQ-SDS-005
func Default() []byte { return nil }
