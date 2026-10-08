// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "open-sds/app/internal/sramcapture"

// envelopeAllowed reports whether a live frame may be read as a display
// envelope (ADR-IMAGE-REGROUP-DECIMATION). Paths that consume every sample —
// software protocol or window qualification, averaging, precision and ERES
// filtering, zone triggers — keep full recall.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeAllowed(cfg sramcapture.Config, tp trigParams) bool {
	switch e.acqMode.Load() {
	case AcqAverage, AcqPrecision, AcqEres:
		return false
	}
	if tp.typ != TrigEdge || e.zoneMode.Load() == ZoneTrigger || e.decodeViewOn() {
		return false
	}
	if e.serialMode.Load() == SerialTrigger && hardwareSerialName(cfg) == "" {
		return false
	}
	return e.hardwareEnvelope
}

// envelopeWindow buckets a live recall window for the display: words per
// bucket, and the centred window it covers; zero when the window is short
// enough (under eight words per bucket) to recall as it is.
// TRLC-LINKS: REQ-SDS-010
func envelopeWindow(offset, words uint32) (bucket, start, length uint32) {
	// A read, with its 16 warm-up words, stays within one SRAM's worth.
	if limit := uint32(sramcapture.Words - 16); words > limit {
		offset, words = offset+(words-limit)/2, limit
	}
	// The FPGA reduces two-word packets, so buckets hold an even word count.
	// Below eight words per bucket the recall is short anyway: keep raw detail.
	bucket = words / sramcapture.EnvelopeBuckets &^ 1
	if bucket < 8 {
		return 0, offset, words
	}
	length = bucket * sramcapture.EnvelopeBuckets
	return bucket, offset + (words-length)/2, length
}
