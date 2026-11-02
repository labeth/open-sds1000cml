// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

// softRingPlan is the history ring's decimation for a digital stream at
// decimation streamLog (feature, longest and quiet the protocol's timing,
// pushS the time between decodes): ok false when the stream's words come
// too fast for the on-chip slack to ride out a pump gap, or when no ring
// both covers the host's latency, the frame and the margin and keeps three
// ring words in the protocol's shortest feature.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func softRingPlan(streamLog int, feature, longest, quiet, pushS float64) (n int, why string, ok bool) {
	return softRingPlanRate(500e6/float64(uint64(1)<<streamLog), feature, longest, quiet, pushS)
}
