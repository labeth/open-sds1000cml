//go:build !withbitstream

package fpgaload

// Default returns the embedded default image, or nil when the binary was built
// without one (`make app`). Such a binary verifies the fabric but cannot
// configure it: it runs only against a fabric already carrying the default
// image. `make app-release` builds with -tags withbitstream.
func Default() []byte { return nil }
