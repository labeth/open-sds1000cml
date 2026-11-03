// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

//go:build !(linux && arm)

package diag

import "errors"

// Trace needs the 32-bit ARM syscall registers: on another build it says so.
// TRLC-LINKS: REQ-SDS-285
func Trace(_ int, _ TraceOpts) error {
	return errors.New("trace: the register trace runs on the scope's 32-bit ARM only")
}
