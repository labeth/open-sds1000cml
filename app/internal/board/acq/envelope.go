// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// FinestDecimatedS is the fabric's first decimated sample interval (/16 of
// the 2 ns full rate).
const FinestDecimatedS = 32e-9

// EnvelopeWindow buckets a live recall window for the display: words per
// bucket, and the window it covers - the requested one widened about its
// centre to whole buckets, within the record's total words; zero when the
// window is short enough (under two words per bucket) to recall as it is.
// Whole buckets of the rounded-down size covered less than the screen
// (2us/div: 8192 of 10000 samples), and the display stretched them over it.
// A window over one SRAM's worth narrows by its first front words first (the
// room a serial trigger keeps before the screen), then about its centre:
// centred, a T55xx command's last millisecond fell off the screen's end.
// TRLC-LINKS: REQ-SDS-010
func EnvelopeWindow(offset, words, buckets, total, front uint32) (bucket, start, length uint32) {
	// A read, with its 16 warm-up words, stays within one SRAM's worth.
	limit := min(sramcapture.Words-16, total)
	front = min(front, words)
	if words > limit {
		drop := min(words-limit, front)
		offset, words, front = offset+drop+(words-limit-drop)/2, limit, front-drop
	}
	// Below two words per bucket the window is at most a few thousand words:
	// recalling it raw is as quick, and keeps every sample.
	if buckets == 0 || words < 2*buckets {
		return 0, offset, words
	}
	// The FPGA reduces two-word packets, so buckets hold an even word count.
	bucket = (words + buckets - 1) / buckets
	bucket += bucket & 1
	for bucket > 2 && bucket*buckets > limit {
		bucket -= 2
	}
	length = bucket * buckets
	centre := offset + words/2
	start = centre - min(centre, length/2)
	if length < words {
		drop := min(words-length, front)
		start = offset + drop + (words-length-drop)/2
	}
	if start+length > total {
		start = total - min(total, length)
	}
	return bucket, start, length
}

// OrderEnvelope turns (min, max) bucket pairs into a time-ordered stream: each
// pair is emitted in the order that continues from the value before it, so an
// edge inside a bucket reads as one transition, not a spike. Decoders and
// software qualifiers can then use the frame as samples at half-bucket
// spacing; a pulse shorter than a bucket stays visible as a one-sample peak.
// q, when non-empty, is reordered in step with c.
// TRLC-LINKS: REQ-SDS-010
func OrderEnvelope(c []uint8, q []uint16) {
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

// OrderEnvelopeHead orders a record's first (min, max) pair, which has no
// earlier sample to continue from, so it leads into the next pair: left as
// (min, max), a falling edge in the first bucket reads low, high, low - a
// one-sample pulse at the record's start that VIDEO takes for a sync pulse.
// Call it before orderEnvelope on a fresh record.
// TRLC-LINKS: REQ-SDS-010
func OrderEnvelopeHead(c []uint8, q []uint16) {
	if len(c) < 4 {
		return
	}
	m := (int(c[2]) + int(c[3])) / 2
	if abs(int(c[0])-m) < abs(int(c[1])-m) {
		c[0], c[1] = c[1], c[0]
		if len(q) == len(c) {
			q[0], q[1] = q[1], q[0]
		}
	}
}

// TRLC-LINKS: REQ-SDS-010
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
