// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"strings"

	core "github.com/labeth/open-dso-core/supervisor/diag"
)

// factoryApp is the vendor app's executable name.
const factoryApp = "SDS1000_arm.app"

// factoryPID is the running factory app's pid, or 0.
// TRLC-LINKS: REQ-SDS-284
func factoryPID(ps []core.Proc) int {
	for _, p := range ps {
		if strings.HasSuffix(p.Exe, "/"+factoryApp) || p.Comm == factoryApp[:min(len(factoryApp), 15)] { // comm holds 15 characters
			return p.PID
		}
	}
	return 0
}
