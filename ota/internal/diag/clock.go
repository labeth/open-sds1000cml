// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import "golang.org/x/sys/unix"

// monoMicros is CLOCK_MONOTONIC in microseconds: the trace and the sweep's
// step log share it, so a step's register traffic is found by its time.
// TRLC-LINKS: REQ-SDS-285
func monoMicros() int64 {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts) != nil {
		return 0
	}
	return ts.Nano() / 1000
}
