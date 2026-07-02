// Package buildinfo carries version identity stamped at link time.
package buildinfo

// Set via -ldflags "-X open-sds/ota/internal/buildinfo.Version=... -X ...".
var (
	Version = "dev"
	Commit  = "unknown"
	Built   = "unknown"
)

func String() string {
	return Version + " (" + Commit + ", " + Built + ")"
}
