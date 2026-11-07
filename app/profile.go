// ENGMODEL-OWNER-UNIT: FU-APP-APP

// Package scope is the SDS1000CML+'s data sheet as the core reads it: its
// profile (profile.yaml), embedded so the app carries the one the tests
// check against its constants.
package scope

import (
	_ "embed"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/profile"
)

//go:embed profile.yaml
var profileYAML []byte

// Profile is the scope's profile for the unit id names.
// TRLC-LINKS: REQ-SDS-010
func Profile(id hal.Identity) (hal.Profile, error) { return profile.Load(profileYAML, id) }
