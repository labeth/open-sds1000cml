// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "open-sds/app/internal/superres"

// NewStackState returns an empty accumulation for bins fine bins.
// TRLC-LINKS: REQ-SDS-141
func NewStackState(bins int) *StackState {
	return &StackState{Moments: make([]superres.FPGAStackMoments, 2*bins)}
}
