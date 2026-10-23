// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"runtime"
	"syscall"
)

// searchNice is the bus search's thread priority while it decodes (nice).
const searchNice = -10

// atSearchPriority runs fn (a bus search's decoding) on its own thread at
// nice searchNice: on the scope's one core the capture loop and the display
// took turns with it, and a search of 1.4 s of decoding took 2.9 s. The
// others keep a share (the screen and the web page stay responsive), which
// real-time priority would not leave them. Elsewhere fn runs as is.
// TRLC-LINKS: REQ-SDS-214
func atSearchPriority(fn func()) {
	if runtime.GOARCH != "arm" {
		fn()
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tid := syscall.Gettid()
	prev, err := syscall.Getpriority(syscall.PRIO_PROCESS, tid)
	if err != nil || syscall.Setpriority(syscall.PRIO_PROCESS, tid, searchNice) != nil {
		fn()
		return
	}
	defer syscall.Setpriority(syscall.PRIO_PROCESS, tid, 20-prev) // Getpriority returns 20 - nice
	fn()
}
