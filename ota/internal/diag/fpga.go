// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"fmt"
	"strings"
	"time"

	"github.com/labeth/open-sds1000cml/ota/internal/fdinherit"
	"github.com/labeth/open-sds1000cml/ota/internal/gpmc"
)

// plainRegs are the factory fabric's plain registers the agent already reads
// at takeover: the version, the two status words and the fill counter. No
// sample port and no other selector is read: on another board a read can pop
// a FIFO the factory app is draining.
var plainRegs = []uint16{gpmc.SelVersion, gpmc.SelStatus, gpmc.SelStatus + 1, gpmc.SelFill}

// collectFPGA reads the factory fabric's plain registers through the
// /dev/Gpmc descriptor the stick's shell inherited (a fresh open faults),
// the fill counter a few times to show whether acquisition runs.
// TRLC-LINKS: REQ-SDS-284
func collectFPGA(a *Archive) (summary string, ver uint16, ok bool) {
	var b strings.Builder
	fd := fdinherit.Find("/dev/Gpmc")
	fmt.Fprintf(&b, "inherited /dev/Gpmc fd: %d\n", fd)
	fmt.Fprintf(&b, "inherited /dev/fpga_key fd: %d\n", fdinherit.Find("/dev/fpga_key"))
	summary = "no inherited /dev/Gpmc"
	if fd >= 0 {
		r := gpmc.NewReader(fd)
		for _, sel := range plainRegs {
			v, err := r.Read(gpmc.PlaneCS1, sel)
			fmt.Fprintf(&b, "CS1 0x%02x = 0x%04x %v\n", sel, v, errText(err))
			if sel == gpmc.SelVersion {
				summary = fmt.Sprintf("version register 0x%04x (0x%04x on the SDS1102CML+)", v, gpmc.VersionMagic)
				ver, ok = v, err == nil
			}
		}
		for range 5 {
			time.Sleep(50 * time.Millisecond)
			v, err := r.Read(gpmc.PlaneCS1, gpmc.SelFill)
			fmt.Fprintf(&b, "fill 0x%04x %v\n", v&gpmc.FillMask, errText(err))
		}
	}
	a.AddText("fpga/registers.txt", b.String())
	return summary, ver, ok
}

// errText is err's text, or "" for nil.
// TRLC-LINKS: REQ-SDS-284
func errText(err error) string {
	if err == nil {
		return ""
	}
	return "(" + err.Error() + ")"
}
