// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"io"
	"time"
)

// TraceOpts is one register-trace window.
type TraceOpts struct {
	For             time.Duration   // the longest the window lasts
	Out             string          // the gzip'd trace file
	MaxLines        int             // the most lines it writes
	QuietAfterWrite time.Duration   // end this long after a register write with none after it (0: run For)
	Ready           io.Writer       // told "ready" once every thread is attached
	Stop            <-chan struct{} // closed: end the window now
}
