// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

// Command sdsdiag is the diagnostics stick's program (see internal/diag).
package main

import (
	"os"

	"github.com/labeth/open-sds1000cml/ota/internal/diag"
)

// TRLC-LINKS: REQ-SDS-284
func main() { os.Exit(diag.Run(os.Args[1:], os.Stderr)) }
