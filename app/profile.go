// ENGMODEL-OWNER-UNIT: FU-APP-APP

// Package scope is the SDS1000CML+'s data sheet as the core reads it: its
// profile (profile.yaml), embedded so the app carries the one the tests
// check against its constants.
package scope

import (
	_ "embed"
	"os"
	"strings"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/profile"
)

//go:embed profile.yaml
var profileYAML []byte

// Profile is the scope's profile for the unit id names.
// TRLC-LINKS: REQ-SDS-010
func Profile(id hal.Identity) (hal.Profile, error) { return profile.Load(profileYAML, id) }

// systemInfo is the vendor's unit record on the internal flash.
const systemInfo = "/usr/bin/siglent/usr/system/system_info.dat"

// Identity is the unit this app runs on: a Siglent SDS1102CML+ (the only
// unit it starts on: the original CML has no Linux) with its serial.
// TRLC-LINKS: REQ-SDS-010
func Identity() hal.Identity {
	return hal.Identity{Vendor: "Siglent", Model: "SDS1102CML+", Serial: UnitSerial()}
}

// UnitSerial is the unit's serial number, best effort: it lives in the
// vendor's system_info.dat as printable ASCII starting "SDS" (its exact
// offset is not pinned); SDS00000000000 when it cannot be read.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-024
func UnitSerial() string {
	if b, err := os.ReadFile(systemInfo); err == nil {
		run := []byte{}
		for _, c := range b {
			if c >= 0x20 && c < 0x7f {
				run = append(run, c)
				if len(run) >= 14 && strings.HasPrefix(string(run), "SDS") {
					return string(run)
				}
			} else {
				run = run[:0]
			}
		}
	}
	return "SDS00000000000"
}
