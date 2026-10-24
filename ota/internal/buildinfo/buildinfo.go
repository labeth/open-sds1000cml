// Package buildinfo carries version identity stamped at link time.
// ENGMODEL-OWNER-UNIT: FU-OTA-BUILDINFO
package buildinfo

import "sync"

// Set via -ldflags "-X open-sds/ota/internal/buildinfo.Version=... -X ...".
var (
	Version = "dev"
	Commit  = "unknown"
	Built   = "unknown"
)

// String is the identity line, made once: the stamped values do not change
// after link time.
// TRLC-LINKS: REQ-SDS-178
func String() string { return identity() }

var identity = sync.OnceValue(func() string { return Version + " (" + Commit + ", " + Built + ")" })
