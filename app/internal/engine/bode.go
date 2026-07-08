package engine

import (
	"math"
	"sort"
	"sync"
)

// Frequency Response Analysis (Bode plot) — docs/bode-plan.md. With FRA armed,
// every locked frame yields ONE transfer-function point between a REFERENCE
// channel (DUT input) and a DUT-output channel: the fundamental frequency, the
// magnitude ratio in dB, and the phase difference in degrees. Points accumulate
// into log-frequency bins as an external/FPGA source sweeps, building the Bode
// curve. The engine is the single source of truth; the web and the LCD render
// the same accumulated points.

const (
	BodeOff = 0
	BodeOn  = 1

	bodeBinsPerDecade = 30   // log-frequency accumulation resolution
	bodeMaxPoints     = 4096 // hard cap on the accumulated set
	bodeMinCycles     = 4.0  // need at least this many cycles in the record
)

// BodePoint is one accumulated transfer-function sample.
type BodePoint struct {
	FreqHz   float64
	GainDB   float64
	PhaseDeg float64
	Seq      uint64 // capture ordinal of the frame that set this bin (freshness)
}

type bodeState struct {
	mu    sync.Mutex
	refCh int // 0 = C1, 1 = C2 (DUT input reference)
	dutCh int // 0 = C1, 1 = C2 (DUT output)
	bins  map[int]BodePoint
	cap   uint64 // capture ordinal

	// last live point (for the status readout), guarded by mu
	live      BodePoint
	liveValid bool
}

// SetBodeMode arms/disarms FRA and sets the reference + DUT channels. Arming
// with a fresh channel pair does NOT clear the accumulated curve (the operator
// may re-arm mid-sweep); use ClearBode to reset.
func (e *Engine) SetBodeMode(on bool, refCh, dutCh int) {
	e.bode.mu.Lock()
	e.bode.refCh = refCh & 1
	e.bode.dutCh = dutCh & 1
	if e.bode.bins == nil {
		e.bode.bins = make(map[int]BodePoint)
	}
	e.bode.mu.Unlock()
	if on {
		e.bodeMode.Store(BodeOn)
	} else {
		e.bodeMode.Store(BodeOff)
	}
}

// ClearBode empties the accumulated curve.
func (e *Engine) ClearBode() {
	e.bode.mu.Lock()
	e.bode.bins = make(map[int]BodePoint)
	e.bode.liveValid = false
	e.bode.mu.Unlock()
}

// BodePoints returns the accumulated curve sorted by frequency.
func (e *Engine) BodePoints() []BodePoint {
	e.bode.mu.Lock()
	defer e.bode.mu.Unlock()
	out := make([]BodePoint, 0, len(e.bode.bins))
	for _, p := range e.bode.bins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FreqHz < out[j].FreqHz })
	return out
}

// bodeEval computes the transfer-function point for a locked frame and, if
// valid, updates its log-frequency bin. Runs on the engine goroutine.
func (e *Engine) bodeEval(f *Frame, valid int, sampleS float64) {
	if sampleS <= 0 || valid < 8 {
		return
	}
	e.bode.mu.Lock()
	refCh, dutCh := e.bode.refCh, e.bode.dutCh
	e.bode.mu.Unlock()

	ref := f.C1
	dut := f.C1
	if refCh == 1 {
		ref = f.C2
	}
	if dutCh == 1 {
		dut = f.C2
	}
	if len(ref) < valid || len(dut) < valid {
		return
	}
	ref, dut = ref[:valid], dut[:valid]

	f0 := fundamentalHz(ref, sampleS)
	if f0 <= 0 {
		e.bodeInvalidate()
		return
	}
	cyc := f0 * float64(valid) * sampleS
	if cyc < bodeMinCycles || f0 >= 0.5/sampleS {
		e.bodeInvalidate() // too few cycles to trust, or at/over Nyquist
		return
	}
	re1, im1 := singleBinDFT(ref, f0, sampleS)
	re2, im2 := singleBinDFT(dut, f0, sampleS)
	mag1 := math.Hypot(re1, im1)
	mag2 := math.Hypot(re2, im2)
	// noise floor: the reference must carry real signal (its DFT bin well above
	// the per-sample quantisation noise scaled by record length).
	floor := 0.5 * math.Sqrt(float64(valid))
	if mag1 < floor {
		e.bodeInvalidate()
		return
	}
	gainDB := 20 * math.Log10(mag2/mag1)
	if math.IsInf(gainDB, 0) || math.IsNaN(gainDB) {
		e.bodeInvalidate()
		return
	}
	// phase of X2·conj(X1) = arg(X2) − arg(X1), wrapped to (−180, 180]
	pr := re2*re1 + im2*im1
	pi := im2*re1 - re2*im1
	phaseDeg := math.Atan2(pi, pr) * 180 / math.Pi

	p := BodePoint{FreqHz: f0, GainDB: gainDB, PhaseDeg: phaseDeg}
	e.bode.mu.Lock()
	p.Seq = e.bode.cap + 1
	e.bode.cap = p.Seq
	if e.bode.bins == nil {
		e.bode.bins = make(map[int]BodePoint)
	}
	bin := int(math.Round(math.Log10(f0) * bodeBinsPerDecade))
	if len(e.bode.bins) < bodeMaxPoints || func() bool { _, ok := e.bode.bins[bin]; return ok }() {
		e.bode.bins[bin] = p
	}
	e.bode.live, e.bode.liveValid = p, true
	e.bode.mu.Unlock()
}

func (e *Engine) bodeInvalidate() {
	e.bode.mu.Lock()
	e.bode.liveValid = false
	e.bode.mu.Unlock()
}

// fundamentalHz estimates the fundamental frequency of a periodic record by
// mean-level crossing spacing (works for square or sine stimuli; no FFT). The
// single-bin DFT that follows only needs this as an f0 estimate — the gain/
// phase come from the X2/X1 ratio, which is robust to a small f0 error.
func fundamentalHz(sig []uint8, sampleS float64) float64 {
	n := len(sig)
	if n < 8 {
		return 0
	}
	var sum float64
	for _, v := range sig {
		sum += float64(v)
	}
	mean := sum / float64(n)
	// rising crossings of the mean with a small hysteresis to reject noise
	const hyst = 2.0
	var firstX, lastX float64
	crossings := 0
	armed := false
	firstX, lastX = -1, -1
	for i := 1; i < n; i++ {
		a, b := float64(sig[i-1]), float64(sig[i])
		if !armed && b < mean-hyst {
			armed = true
		}
		if armed && a < mean && b >= mean {
			// linear-interpolated crossing position
			x := float64(i-1) + (mean-a)/(b-a)
			if firstX < 0 {
				firstX = x
			}
			lastX = x
			crossings++
			armed = false
		}
	}
	if crossings < 2 || lastX <= firstX {
		return 0
	}
	periodSamples := (lastX - firstX) / float64(crossings-1)
	if periodSamples <= 1 {
		return 0
	}
	return 1.0 / (periodSamples * sampleS)
}

// singleBinDFT returns the real/imag parts of the DFT coefficient at frequency
// f0: Σ (v[n]−mean)·exp(−j·2π·f0·n·dt). Mean-subtracted so the DC term does not
// leak into the fundamental.
func singleBinDFT(sig []uint8, f0, sampleS float64) (re, im float64) {
	n := len(sig)
	var sum float64
	for _, v := range sig {
		sum += float64(v)
	}
	mean := sum / float64(n)
	w := 2 * math.Pi * f0 * sampleS // radians per sample
	// incremental cos/sin via rotation to avoid a trig call per sample
	c, s := math.Cos(w), math.Sin(w)
	cosk, sink := 1.0, 0.0
	for i := 0; i < n; i++ {
		v := float64(sig[i]) - mean
		re += v * cosk
		im -= v * sink
		cosk, sink = cosk*c-sink*s, sink*c+cosk*s
	}
	return re, im
}
