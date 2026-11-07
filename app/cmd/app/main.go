// Command app is the scope application. The OTA agent starts it as a direct
// child (the app <-> OTA contract, ota/README.md): it inherits the boot's
// /dev/Gpmc and /dev/fpga_key descriptors, loads its embedded FPGA image and
// builds the board (app.Run), which the core's scope application runs: the
// acquisition engine, the LCD, the front panel, the web UI and SCPI, the
// settings and the agent's health token, until SIGTERM. Started again by
// itself with --acquisition-worker it is the bus worker that owns the GPMC
// transfers (app.Worker). It has no other way to run (ADR-ONE-APP).
// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"os"

	"github.com/labeth/open-sds1000cml/app/internal/app"
)

// TRLC-LINKS: REQ-SDS-167
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--acquisition-worker" {
		os.Exit(app.Worker())
	}
	os.Exit(app.Run())
}
