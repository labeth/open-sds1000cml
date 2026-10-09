// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "open-sds/app/internal/sramcapture"

// envelopeAllowed reports whether a live frame may be read as a display
// envelope (ADR-IMAGE-REGROUP-DECIMATION).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeAllowed(cfg sramcapture.Config, tp trigParams) bool {
	return e.envelopeBlock(cfg, tp) == ""
}

// envelopeBlock names what keeps a live frame from the envelope readout, or
// "" when it may use it (reported as the live_readout status).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeBlock(cfg sramcapture.Config, tp trigParams) string {
	// Everything else reads a live frame as a time-ordered envelope of the
	// full-depth capture (see orderEnvelope): live frames stay fast whatever
	// is enabled, and the full record is read on STOP or SINGLE.
	switch e.acqMode.Load() {
	case AcqAverage:
		return "averaging"
	case AcqPrecision:
		return "precision"
	case AcqEres:
		return "eres"
	}
	switch {
	case e.bodeMode.Load() == BodeOn:
		return "bode" // a single-bin DFT needs true samples; its screens are short
	case e.leased(&e.decodeLease):
		return "raw requested"
	case e.leased(&e.sampleLease):
		return "samples requested"
	case !e.hardwareEnvelope:
		return "fabric lacks envelope"
	}
	return ""
}

// liveDecimates reports whether a live frame blocked from the envelope is
// captured decimated instead (liveSRAMPlan): the noise-reducing modes, whose
// filtering an envelope of extremes would defeat, and an image without the
// envelope reducer. Bode keeps its (short) raw screens and a raw lease its
// full-rate samples.
// TRLC-LINKS: REQ-SDS-010
func liveDecimates(block string) bool {
	return block != "" && block != "bode" && block != "raw requested"
}

// envelopeWindow buckets a live recall window for the display: words per
// bucket, and the centred window it covers; zero when the window is short
// enough (under eight words per bucket) to recall as it is.
// TRLC-LINKS: REQ-SDS-010
func envelopeWindow(offset, words, buckets uint32) (bucket, start, length uint32) {
	// A read, with its 16 warm-up words, stays within one SRAM's worth.
	if limit := uint32(sramcapture.Words - 16); words > limit {
		offset, words = offset+(words-limit)/2, limit
	}
	// The FPGA reduces two-word packets, so buckets hold an even word count.
	// Below two words per bucket the window is at most a few thousand words:
	// recalling it raw is as quick, and keeps every sample.
	bucket = words / buckets &^ 1
	if bucket < 2 {
		return 0, offset, words
	}
	length = bucket * buckets
	return bucket, offset + (words-length)/2, length
}

// orderEnvelope turns (min, max) bucket pairs into a time-ordered stream: each
// pair is emitted in the order that continues from the value before it, so an
// edge inside a bucket reads as one transition, not a spike. Decoders and
// software qualifiers can then use the frame as samples at half-bucket
// spacing; a pulse shorter than a bucket stays visible as a one-sample peak.
// q, when non-empty, is reordered in step with c.
// TRLC-LINKS: REQ-SDS-010
func orderEnvelope(c []uint8, q []uint16) {
	withQ := len(q) == len(c)
	for i := 2; i+1 < len(c); i += 2 {
		prev := int(c[i-1])
		a, b := int(c[i]), int(c[i+1])
		if abs(b-prev) < abs(a-prev) {
			c[i], c[i+1] = c[i+1], c[i]
			if withQ {
				q[i], q[i+1] = q[i+1], q[i]
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
