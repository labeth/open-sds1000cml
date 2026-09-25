// Package buildinfo carries version identity stamped at link time.
// ENGMODEL-OWNER-UNIT: FU-OTA-BUILDINFO
package buildinfo

// Set via -ldflags "-X open-sds/ota/internal/buildinfo.Version=... -X ...".
var (
	Version = "dev"
	Commit  = "unknown"
	Built   = "unknown"
)

// TRLC-LINKS: REQ-SDS-178
func String() string {
	return Version + " (" + Commit + ", " + Built + ")"
}
