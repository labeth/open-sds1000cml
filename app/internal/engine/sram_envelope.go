// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"

	"open-sds/app/internal/sramcapture"
)

// envelopeAllowed reports whether a live frame may be read as a display
// envelope (ADR-IMAGE-REGROUP-DECIMATION).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeAllowed(cfg sramcapture.Config, tp trigParams) bool {
	return e.envelopeBlock(&cfg, tp) == ""
}

// envelopeBlock names what keeps a live frame from the envelope readout, or
// "" when it may use it (reported as the live_readout status).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeBlock(cfg *sramcapture.Config, tp trigParams) string {
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
	case e.rawLeased():
		return blockRaw
	case e.leased(&e.spectrumLease):
		return blockSpectrum
	case e.leased(&e.searchLease):
		return blockSearch
	case e.leased(&e.busLease):
		return blockBus
	case e.leased(&e.sampleLease):
		return blockSamples
	case !e.hardwareEnvelope:
		return "fabric lacks envelope"
	}
	return ""
}

// blockRaw is the envelope block of a raw lease (LeaseDecodeView, LeaseRawFor).
const blockRaw = "raw requested"

// blockSpectrum is the envelope block of a spectrum lease (LeaseSpectrumView).
const blockSpectrum = "spectrum requested"

// blockSearch is the envelope block of a search lease (LeaseSearchView).
const blockSearch = "search requested"

// blockSamples is the envelope block of a sample lease (LeaseSampleView).
const blockSamples = "samples requested"

// blockBus is the envelope block of a bus decode's lease (LeaseBusSamples).
const blockBus = "bus samples requested"

// SearchLiveSamples bounds a bus search's decimated screen: four times the
// live view's, so a screen of 20ms/div still samples every 8 us (a Wiegand
// pulse of 50 us spans six samples, an IR remote's 562 us mark 68), read in
// about a tenth of a second.
const SearchLiveSamples = 32768

// leaseRecord is the record of a sample, search or spectrum lease's capture
// in screens (autoset, BUS's search, the spectrum: they read the screen
// only): a quarter screen either side of it, not the live view's four
// screens - a capture waits out its record's prehistory and the rest after
// the trigger, and AUTO's fit at 5ms/div read 16 of them (a second more each
// at 20ms/div); the spectrum at 10us/div ran 56 frames a second on four.
const leaseRecord = 1.5

// SpectrumRawSamples is the longest screen a spectrum lease reads at the full
// rate: the web's largest FFT (FFT_MAX); a longer one is decimated, filtered.
const SpectrumRawSamples = 32768

// liveDecimatesPlan is liveDecimates for plan p: a spectrum keeps a screen of
// up to SpectrumRawSamples at the full rate (5us/div: 25000 samples, 250 MHz
// of spectrum against the decimated capture's 4.2 MHz) and is decimated
// beyond it.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-070
func liveDecimatesPlan(block string, p SRAMPlan) bool {
	if block == blockSpectrum && p.Screen <= SpectrumRawSamples {
		return false
	}
	return liveDecimates(block)
}

// spectrumLiveSamples bounds a spectrum's decimated screen: twice the FFT's
// points (framecalc.MaxFFT, the web's FFT_MAX: 8192), so the screen holds
// 8192-16384 samples and the transform takes them one for one, filtered by the
// capture's own decimation. A screen of up to 32768 took block means of three
// (a boxcar, its harmonics folding back) from a read of twice the time: the
// spectrum at 200us/div updated 3 times a second.
const spectrumLiveSamples = 16384

// liveBudget is the live screen's bound in record words: a spectrum's is
// spectrumLiveSamples (a raw screen of up to SpectrumRawSamples, two a word,
// stays at the full rate), a bus decode's a sample each busS over the
// screen's spanS at least (busLiveBudget), every other liveScreenSamples.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-070
func liveBudget(block string, spanS, busS float64) int {
	switch block {
	case blockBus:
		return busLiveBudget(spanS, busS)
	case blockSpectrum:
		return spectrumLiveSamples
	case blockSearch:
		return SearchLiveSamples
	}
	return liveScreenSamples
}

// busLiveBudget is a bus decode's live screen in record words: a sample each
// sampleS over spanS (twice that, as the decimation steps by two), at least
// liveScreenSamples and at most the record.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-214
func busLiveBudget(spanS, sampleS float64) int {
	if !(spanS > 0) || !(sampleS > 0) {
		return int(sramcapture.Words)
	}
	return int(min(float64(sramcapture.Words), max(float64(liveScreenSamples), math.Ceil(2*spanS/sampleS))))
}

// liveDecimates reports whether a live frame blocked from the envelope is
// captured decimated instead (liveSRAMPlan): the noise-reducing modes, whose
// filtering an envelope of extremes would defeat, and an image without the
// envelope reducer. Bode keeps its (short) raw screens and a raw lease its
// full-rate samples.
// TRLC-LINKS: REQ-SDS-010
func liveDecimates(block string) bool {
	return block != "" && block != "bode" && block != blockRaw
}

// envelopeWindow buckets a live recall window for the display: words per
// bucket, and the window it covers - the requested one widened about its
// centre to whole buckets, within the record's total words; zero when the
// window is short enough (under two words per bucket) to recall as it is.
// Whole buckets of the rounded-down size covered less than the screen
// (2us/div: 8192 of 10000 samples), and the display stretched them over it.
// A window over one SRAM's worth narrows by its first front words first (the
// room a serial trigger keeps before the screen), then about its centre:
// centred, a T55xx command's last millisecond fell off the screen's end.
// TRLC-LINKS: REQ-SDS-010
func envelopeWindow(offset, words, buckets, total, front uint32) (bucket, start, length uint32) {
	// A read, with its 16 warm-up words, stays within one SRAM's worth.
	limit := min(uint32(sramcapture.Words-16), total)
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

// orderEnvelopeHead orders a record's first (min, max) pair, which has no
// earlier sample to continue from, so it leads into the next pair: left as
// (min, max), a falling edge in the first bucket reads low, high, low - a
// one-sample pulse at the record's start that VIDEO takes for a sync pulse.
// Call it before orderEnvelope on a fresh record.
// TRLC-LINKS: REQ-SDS-010
func orderEnvelopeHead(c []uint8, q []uint16) {
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
