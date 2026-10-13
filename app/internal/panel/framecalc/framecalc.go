// Package framecalc holds the per-frame calculations that both the LCD
// renderer and the panel's workspace tools need: the protocol decode of a
// published frame and its magnitude spectrum. Each is cached by frame
// content, so the decode strip and the BUS packet list, or the FFT plot and
// the SPECTRUM peak table, share one computation per frame instead of
// repeating it per redraw (the ARM renders about 20 times a second).
// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import (
	"math"
	"sync"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
)

// Protocol numbers, shared by the panel's decode setting and the LCD.
const (
	ProtoOff = iota
	ProtoAuto
	ProtoUART
	ProtoI2C
	ProtoSPI
	ProtoManchester
	ProtoSENT
	ProtoCAN
	ProtoMIL1553
	ProtoARINC429
	ProtoUSB
	ProtoFlexRay
	NumProtos
)

// DecodeParams selects the decoder and its settings.
type DecodeParams struct {
	Proto      int // ProtoOff..ProtoFlexRay
	Baud       int // UART baud, or the bit rate of the bit-rate protocols
	ChA, ChB   int // UART source / I2C SCL / SPI CLK, and I2C SDA / SPI DATA (0 = C1)
	CPOL, CPHA bool
	Format     string // span text: "hex", "ascii" or "both"
}

type decodeKey struct {
	id, seq bool
	ident   uint64
	valid   int
	sampleS float64
	p       DecodeParams
}

var decodeCache struct {
	sync.Mutex
	key decodeKey
	res decode.Result
	ok  bool
	at  time.Time // when Auto last ran its detection
}

// AutoRefresh paces Auto's detection, which tries every protocol: at the
// live frame rate it was the largest share of an LCD frame. Between runs a
// new frame is re-decoded as the protocol last detected.
const AutoRefresh = 500 * time.Millisecond

// Channel returns a frame channel's valid samples; C2 falls back to C1 when
// the frame has no second channel.
// TRLC-LINKS: REQ-SDS-018
func Channel(f *engine.Frame, ch, valid int) []uint8 {
	if ch == 1 && len(f.C2) >= valid {
		return f.C2[:valid]
	}
	return f.C1[:valid]
}

// Valid clamps a frame's valid-sample count into its C1 slice.
// TRLC-LINKS: REQ-SDS-018
func Valid(f *engine.Frame) int {
	return min(max(f.Valid, 1), len(f.C1))
}

// Decode decodes f's valid samples with p, reusing the last result for the
// same frame content and settings: a deep record's decode takes most of a
// second on the ARM, and the strip, the packet list and the eye all need it.
// A zero Seq and Content (a hand-built frame) is never cached.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func Decode(f *engine.Frame, p DecodeParams) decode.Result {
	if f == nil || len(f.C1) == 0 || p.Proto == ProtoOff {
		return decode.Result{}
	}
	valid := Valid(f)
	key := decodeKey{valid: valid, sampleS: f.SampleS, p: p}
	switch {
	case f.Content != 0:
		key.id, key.ident = true, f.Content
	case f.Seq != 0:
		key.seq, key.ident = true, f.Seq
	}
	cacheable := key.id || key.seq
	ch := func(c int) []uint8 { return Channel(f, c, valid) }
	decodeCache.Lock()
	defer decodeCache.Unlock()
	if cacheable && decodeCache.ok && decodeCache.key == key {
		return decodeCache.res
	}
	// Between detections a new frame is decoded as the last one detected: the
	// cached spans belong to that frame's content, not this one's.
	redecode := false
	if prev := decodeCache.key; p.Proto == ProtoAuto && decodeCache.ok && time.Since(decodeCache.at) < AutoRefresh &&
		prev.p == key.p && prev.valid == key.valid && prev.sampleS == key.sampleS {
		if decodeCache.res.Proto == "off" {
			return decodeCache.res
		}
		redecode = true
	}
	var res decode.Result
	switch p.Proto {
	case ProtoAuto: // detect protocol, channel roles and settings from the signal
		// Strided: a full Autodetect of a 500 k-sample screen took 14 s on
		// the scope's ARM and held the frame all that time.
		var st float64
		if redecode {
			res, st = decode.RedecodeFast(decodeCache.res, ch(0), ch(1), f.SampleS, p.Format)
		} else {
			res, st = decode.AutodetectFast(ch(0), ch(1), f.SampleS, p.Format)
		}
		if k := int(st/f.SampleS + 0.5); k > 1 {
			for i := range res.Spans { // back to the frame's sample indices
				res.Spans[i].I0 *= k
				res.Spans[i].I1 *= k
			}
			res.SPB *= float64(k)
		}
	case ProtoUART:
		res = decode.DecodeUART(ch(p.ChA), f.SampleS, decode.UARTCfg{Baud: p.Baud, Format: p.Format})
	case ProtoI2C:
		res = decode.DecodeI2C(ch(p.ChA), ch(p.ChB), f.SampleS, decode.I2CCfg{Format: p.Format})
	case ProtoSPI:
		res = decode.DecodeSPI(ch(p.ChA), ch(p.ChB), f.SampleS, decode.SPICfg{CPOL: p.CPOL, CPHA: p.CPHA, MSB: true, Format: p.Format})
	case ProtoManchester:
		// MSB first, as Auto found it and the web decodes it (the lock read
		// LSB first: 12 34 showed as 48 2C; bug hunt #3).
		res = decode.DecodeManchester(ch(p.ChA), f.SampleS, decode.ManchesterCfg{Bitrate: p.Baud, IEEE: true, MSB: true, Format: p.Format})
	case ProtoSENT:
		res = decode.DecodeSENT(ch(p.ChA), f.SampleS, decode.SENTCfg{})
	case ProtoCAN:
		res = decode.DecodeCANFD(ch(p.ChA), f.SampleS, decode.CANFDCfg{NominalBaud: p.Baud, DominantLow: true})
	case ProtoMIL1553:
		res = decode.DecodeMIL1553(ch(p.ChA), f.SampleS, decode.MIL1553Cfg{Bitrate: p.Baud})
	case ProtoARINC429:
		res = decode.DecodeARINC429(ch(p.ChA), f.SampleS, decode.ARINC429Cfg{Bitrate: p.Baud})
	case ProtoUSB:
		res = decode.DecodeUSBLS(ch(p.ChA), f.SampleS, decode.USBLSCfg{Bitrate: p.Baud})
	case ProtoFlexRay:
		res = decode.DecodeFlexRay(ch(p.ChA), f.SampleS, decode.FlexRayCfg{Bitrate: p.Baud})
	}
	if f.PeakDetect && f.Ordered {
		switch {
		case p.Proto == ProtoAuto && res.Proto == "off" && res.Error == "no protocol matched":
			// a bus the envelope cannot resolve is not found either (bug hunt
			// #3: a 115200 UART at 500us/div was "no protocol matched")
			res.Error = "no protocol resolved at this timebase - S/DIV faster, or SINGLE"
		case !EnvelopeResolves(res):
			res = decode.Result{Proto: res.Proto, Error: decode.Names[res.Proto] + " not resolved at this timebase - S/DIV faster, or SINGLE"}
		}
	}
	at := time.Now()
	if redecode {
		at = decodeCache.at // the detection's pacing runs from the last detection
	}
	decodeCache.key, decodeCache.res, decodeCache.ok, decodeCache.at = key, res, cacheable, at
	return res
}

// MinEnvelopeFeature is the shortest bus feature, in points, a live envelope
// decodes: well over its (min, max) bucket of two points (a CAN frame of
// ideal edges failed at 2.8 points a bit, a 115200 UART at 500us/div's 3.6
// read 0A as 02 and 20 as 30 on the bench; bug hunt #3).
const MinEnvelopeFeature = 4.0

// EnvelopeResolves reports whether a decode of a live envelope can stand. The
// envelope holds a (min, max) pair per bucket; a line that changes within a
// bucket aliases there (a fast clock reads as a clock of two buckets, its
// data as starts and stops: I2C at 500us/div listed 151 packets of no bytes,
// bug hunt #3), so a bus whose shortest feature is not well over a bucket is
// not decoded from it.
// TRLC-LINKS: REQ-SDS-214
func EnvelopeResolves(res decode.Result) bool {
	fs := decode.FeatureSamples(res)
	return fs == 0 || fs >= MinEnvelopeFeature
}

// ---- spectrum ----

// MaxFFT caps the transform length so a frame's spectrum stays well under
// the render budget on the ARM.
const MaxFFT = 8192

// Spec is one channel's Hann-windowed magnitude spectrum: the record
// block-mean decimated to N (a power of two, at most MaxFFT) samples, mean
// removed, transformed. Mags is the half spectrum (bin 0 = DC) in code
// units; a sine of amplitude A codes reads A·N/4 at its bin.
type Spec struct {
	Mags   []float64
	N      int
	Stride int     // record samples per transform sample
	EffNyq float64 // Hz at bin N/2 (the decimated Nyquist)
	BinHz  float64
	Peak   float64 // the largest magnitude, DC excluded
	PeakK  int
}

// EnvPair is 2 for a peak-detect envelope, whose values come in (min, max)
// pairs per bucket, else 1.
// TRLC-LINKS: REQ-SDS-070
func EnvPair(f *engine.Frame) int {
	if f.PeakDetect {
		return 2
	}
	return 1
}

// PairStride is the block length that reduces a record to its spectrum
// samples. An envelope's blocks hold whole (min, max) pairs, so each spectrum
// sample is bucket midpoints: transforming the pairs themselves put their
// alternation at the top of the axis as a rising false spectrum (bench
// 2026-10-07, UART at 10 us/div).
// TRLC-LINKS: REQ-SDS-070
func PairStride(f *engine.Frame, stride int) int {
	stride = max(stride, 1)
	if p := EnvPair(f); stride%p != 0 {
		stride += p - stride%p
	}
	return stride
}

// BlockMeans reduces src to n samples, each the mean of stride consecutive
// samples: a boxcar low-pass ahead of the decimation. Taking every stride-th
// sample instead folded everything above the reduced Nyquist back into the
// plot (a 1 MHz square's harmonics and the 200 MHz interleave spur at stride 2).
// TRLC-LINKS: REQ-SDS-070
func BlockMeans(src []uint8, n, stride int) []float64 {
	stride = max(stride, 1)
	out := make([]float64, n)
	for i := range out {
		sum := 0
		for _, v := range src[i*stride : (i+1)*stride] {
			sum += int(v)
		}
		out[i] = float64(sum) / float64(stride)
	}
	return out
}

// FFT is an in-place iterative radix-2 Cooley-Tukey transform (len must be
// a power of two).
// TRLC-LINKS: REQ-SDS-070
func FFT(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		ang := -2 * math.Pi / float64(l)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += l {
			cr, ci := 1.0, 0.0
			for k := 0; k < l/2; k++ {
				tr := re[i+k+l/2]*cr - im[i+k+l/2]*ci
				ti := re[i+k+l/2]*ci + im[i+k+l/2]*cr
				re[i+k+l/2] = re[i+k] - tr
				im[i+k+l/2] = im[i+k] - ti
				re[i+k] += tr
				im[i+k] += ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

// Mags Hann-windows samples (mean removed), transforms them and returns the
// half-spectrum magnitudes and the largest of them, DC excluded (Hann
// leakage at DC would otherwise become a false 0 dB anchor).
// TRLC-LINKS: REQ-SDS-070
func Mags(samples []float64) ([]float64, float64, int) {
	n := len(samples)
	re := make([]float64, n)
	im := make([]float64, n)
	var mean float64
	for _, v := range samples {
		mean += v
	}
	mean /= float64(n)
	for i, v := range samples {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		re[i] = (v - mean) * w
	}
	FFT(re, im)
	half := n / 2
	mags := make([]float64, half)
	peak, peakK := 1e-9, 1
	for k := 0; k < half; k++ {
		mags[k] = math.Hypot(re[k], im[k])
		if k >= 1 && mags[k] > peak {
			peak, peakK = mags[k], k
		}
	}
	return mags, peak, peakK
}

type specKey struct {
	id, seq bool
	ident   uint64
	valid   int
	ch      int
	sampleS float64
	pd      bool
}

var specCache struct {
	sync.Mutex
	slot [2]struct {
		key  specKey
		spec Spec
		ok   bool
	}
}

// Spectrum returns channel ch's spectrum of frame f at sampleS seconds per
// sample, cached per channel by frame content. ok is false for a frame
// without per-sample data (an envelope) or too few samples.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func Spectrum(f *engine.Frame, ch int, sampleS float64) (Spec, bool) {
	if f == nil || f.IsEnv || len(f.C1) == 0 {
		return Spec{}, false
	}
	ch &= 1
	valid := Valid(f)
	if ch == 1 && len(f.C2) < valid {
		return Spec{}, false
	}
	n := 1
	for n*2 <= valid/EnvPair(f) && n < MaxFFT {
		n <<= 1
	}
	if n < 16 {
		return Spec{}, false
	}
	key := specKey{valid: valid, ch: ch, sampleS: sampleS, pd: f.PeakDetect}
	switch {
	case f.Content != 0:
		key.id, key.ident = true, f.Content
	case f.Seq != 0:
		key.seq, key.ident = true, f.Seq
	}
	cacheable := key.id || key.seq
	specCache.Lock()
	defer specCache.Unlock()
	s := &specCache.slot[ch]
	if cacheable && s.ok && s.key == key {
		return s.spec, true
	}
	stride := PairStride(f, valid/n)
	mags, peak, peakK := Mags(BlockMeans(Channel(f, ch, valid), n, stride))
	sp := Spec{Mags: mags, N: n, Stride: stride, Peak: peak, PeakK: peakK}
	if sampleS > 0 {
		sp.EffNyq = 0.5 / sampleS / float64(stride)
		sp.BinHz = sp.EffNyq / float64(n/2)
	}
	s.key, s.spec, s.ok = key, sp, cacheable
	return sp, true
}
