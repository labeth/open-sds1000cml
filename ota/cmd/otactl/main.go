// Command otactl is the host-side controller for the open-sds OTA agents:
// the core's otactl (open-dso-core/supervisor/otactl) with this scope's
// stick layout.
//
// It talks to a device either directly over TCP (the agent's OTA_LISTEN
// fallback, default :5900) or through NATS (embedded broker via `otactl
// serve`). Point it at a device with -tcp <ip:port> or -nats <url> -device <id>.
//
// Examples:
//
//	otactl serve                                   # embedded NATS broker for the lab
//	otactl -tcp 192.168.1.209:5900 status
//	otactl -tcp 192.168.1.209:5900 probe --gpmc
//	otactl -tcp 192.168.1.209:5900 takeover --dry-run
//	otactl -tcp 192.168.1.209:5900 update-app ./app-arm
//	otactl -tcp 192.168.1.209:5900 update-agent ./agent-arm
//	otactl power -shelly 192.168.1.223 cycle
//
// ENGMODEL-OWNER-UNIT: FU-OTA-OTACTL
package main

import (
	"os"

	"github.com/labeth/open-dso-core/supervisor/otactl"

	"github.com/labeth/open-sds1000cml/ota/internal/buildinfo"
)

// defaultStageDir is where uploads stage on the scope: the agent's staging
// directory on the stick (mounted at U-disk0), the one writable file system,
// as the scope's root file system, /tmp included, is read-only.
const defaultStageDir = "/usr/bin/siglent/usr/media/U-disk0/agent-slots/staging"

// TRLC-LINKS: REQ-SDS-109, REQ-SDS-110, REQ-SDS-111, REQ-SDS-112, REQ-SDS-175
func main() {
	os.Exit(otactl.Run(os.Args[1:], os.Stdout, os.Stderr, options()))
}

// options are the SDS1000CML+'s: its build and its stick's staging.
// TRLC-LINKS: REQ-SDS-175
func options() otactl.Options {
	return otactl.Options{
		Version:  buildinfo.String(),
		StageDir: defaultStageDir,
		StageDoc: "the stick's\n                    " + defaultStageDir,
	}
}
