// Package buildinfo carries version identity stamped at link time.
// ENGMODEL-OWNER-UNIT: FU-APP-BUILDINFO
package buildinfo

import "sync"

// Set via -ldflags "-X open-sds/app/internal/buildinfo.Version=... -X ...".
var (
	Version = "dev"
	Commit  = "unknown"
	Built   = "unknown"
)

// last is the string String made and the values it made it from: the
// status poll asks for it a few times a second.
var last struct {
	sync.Mutex
	version, commit, built, s string
}

// TRLC-LINKS: REQ-SDS-174
func String() string {
	last.Lock()
	defer last.Unlock()
	if last.s == "" || last.version != Version || last.commit != Commit || last.built != Built {
		last.version, last.commit, last.built = Version, Commit, Built
		last.s = Version + " (" + Commit + ", " + Built + ")"
	}
	return last.s
}
