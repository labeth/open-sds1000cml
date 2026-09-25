// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"fmt"
	"open-sds/app/internal/bus"
)

// applySRAMReadTiming is revision-10-only, invoked before the engine owns CS1.
// It writes no timing file. A failed fast-path check must restore and verify
// the original setting; an unverified transport must not start acquisition.
// TRLC-LINKS: REQ-SDS-034
func applySRAMReadTiming(port bus.TimingPort, verify func() error) (fast bool, err error) {
	old, err := port.Read()
	if err != nil {
		return false, err
	}
	next := old.WithRdCycle(10).WithRdAccess(8).WithOEOff(min(old.OEOff(), 10)).WithCSRdOff(min(old.CSRdOff(), 10)).WithGap(5)
	err = port.Apply(next)
	if err == nil {
		err = verify()
	}
	if err == nil {
		return true, nil
	}
	if restore := port.Restore(old); restore != nil {
		return false, fmt.Errorf("fast timing failed (%v), restore failed: %w", err, restore)
	}
	if baseline := verify(); baseline != nil {
		return false, fmt.Errorf("fast timing failed (%v), original timing also failed: %w", err, baseline)
	}
	return false, nil
}
