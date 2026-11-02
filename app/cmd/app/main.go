// Command app is the scope application. The OTA agent starts it as a direct
// child (the app <-> OTA contract, ota/README.md): it inherits the boot's
// /dev/Gpmc and /dev/fpga_key descriptors, loads its embedded FPGA image, runs
// the acquisition engine and the scope's LCD, front panel, web UI and SCPI,
// writes the agent's health token and exits on SIGTERM. Started again by
// itself with --acquisition-worker it is the bus worker that owns the GPMC
// transfers (app.Worker). It has no other way to run
// (ADR-ONE-APP).
// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"os"

	"open-sds/app/internal/app"
)

// TRLC-LINKS: REQ-SDS-167
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--acquisition-worker" {
		os.Exit(app.Worker())
	}
	app.Run()
}
