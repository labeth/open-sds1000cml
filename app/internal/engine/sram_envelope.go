// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"

	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// envelopeBlock names what keeps a live frame from the envelope readout, or
// "" when it may use it (reported as the live_readout status).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeBlock() string {
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
	case !e.caps().Envelope:
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
// liveScreenSamples and at most the record. A sample finer than the fabric's
// first decimated one (finestDecimatedS) is the full rate's: the decimation
// steps from 2 ns to 32 ns, so a budget under the raw screen planned 32 ns
// for a 10 ns want and no record came that fine.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-214
func busLiveBudget(spanS, sampleS float64) int {
	if !(spanS > 0) || !(sampleS > 0) || sampleS < acq.FinestDecimatedS {
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
