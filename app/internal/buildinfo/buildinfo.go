// Package buildinfo carries version identity stamped at link time.
// ENGMODEL-OWNER-UNIT: FU-APP-BUILDINFO
package buildinfo

// Set via -ldflags "-X open-sds/app/internal/buildinfo.Version=... -X ...".
var (
	Version = "dev"
	Commit  = "unknown"
	Built   = "unknown"
)

// TRLC-LINKS: REQ-SDS-174
func String() string {
	return Version + " (" + Commit + ", " + Built + ")"
}
