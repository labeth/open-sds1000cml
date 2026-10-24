// Package sound is the SOUND analysis (ADR-SOUND): level meters, the tuner,
// the envelope (ADSR), modulation, audio spectrum and harmonics, input
// against output (A/B), V/oct tracking and the pitch-locked cycle, all on
// the gap-free audio-rate stream (or any evenly sampled stretch of volts).
// The functions are pure; the LCD's SOUND view runs them in Go, and the web
// page runs their twins in sound.js, held to them by TestSoundJSParity.
// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"sort"
)

// StreamLog2 is the audio stream's decimation: 2^13 converter samples a
// stream word, 61.035 kSa/s a channel, the precision decimator's CIC on the
// way. It keeps 20 kHz within the decimator's band (about 1.2 dB droop at
// 10 kHz, 4.8 dB at 20 kHz) at a sixteenth of the stream's top rate, so the
// host's share of the drain stays small.
const StreamLog2 = 13

// RateHz is the stream's sample rate a channel at decimation 2^log2.
// TRLC-LINKS: REQ-SDS-265
func RateHz(log2 int) float64 { return 500e6 / float64(uint64(1)<<uint(log2)) }

// Reference levels.
const (
	DBuRefV = 0.7745966692414834 // 0 dBu: 1 mW into 600 ohm
	DBVRefV = 1.0
)

// Preset is a meter's 0 dB reference: the RMS volts that read 0 dB.
type Preset struct {
	ID   string
	Name string
	Vrms float64
}

// Presets are the meters' references. Eurorack's is a 10 Vpp sine (5 V
// peak); guitar's a typical pickup's 100 mV.
var Presets = []Preset{
	{"dbu", "dBu", DBuRefV},
	{"dbv", "dBV", DBVRefV},
	{"euro", "Eurorack 10 Vpp", 5 / math.Sqrt2},
	{"pro", "+4 dBu line", DBuRefV * 1.5848931924611136},
	{"consumer", "-10 dBV", 0.31622776601683794},
	{"guitar", "guitar 100 mV", 0.1},
}

// PresetByID finds a reference (dBu when unknown).
// TRLC-LINKS: REQ-SDS-266
func PresetByID(id string) Preset {
	for _, p := range Presets {
		if p.ID == id {
			return p
		}
	}
	return Presets[0]
}

// DB is v (RMS volts) in dB against ref volts (-Inf for no signal).
// TRLC-LINKS: REQ-SDS-266
func DB(v, ref float64) float64 {
	if !(v > 0) || !(ref > 0) {
		return math.Inf(-1)
	}
	return 20 * math.Log10(v/ref)
}

// Levels is one channel's meter reading over a stretch.
type Levels struct {
	N       int
	Min     float64 // volts
	Max     float64
	Vpp     float64
	DC      float64 // the mean
	RMS     float64 // AC: the mean removed
	Total   float64 // with the DC
	Peak    float64 // largest excursion from the mean
	CrestDB float64 // Peak over RMS, dB (0 without signal)
	Clip    bool    // the converter's range was reached (the caller's codes)
}

// Measure reads the meters of x (volts).
// TRLC-LINKS: REQ-SDS-266
func Measure(x []float64) Levels {
	l := Levels{N: len(x)}
	if len(x) == 0 {
		return l
	}
	l.Min, l.Max = x[0], x[0]
	s := 0.0
	for _, v := range x {
		s += v
		if v < l.Min {
			l.Min = v
		}
		if v > l.Max {
			l.Max = v
		}
	}
	l.DC = s / float64(len(x))
	ss := 0.0
	for _, v := range x {
		d := v - l.DC
		ss += d * d
	}
	l.RMS = math.Sqrt(ss / float64(len(x)))
	l.Total = math.Sqrt(l.RMS*l.RMS + l.DC*l.DC)
	l.Vpp = l.Max - l.Min
	l.Peak = math.Max(l.Max-l.DC, l.DC-l.Min)
	if l.RMS > 0 {
		l.CrestDB = 20 * math.Log10(l.Peak/l.RMS)
	}
	return l
}

// ClipCodes reports Q8.8 codes at the converter's rails: more than a
// two-hundredth of them within two codes of either end (as the scope's own
// clip readout, measure.Clipped).
// TRLC-LINKS: REQ-SDS-266
func ClipCodes(q []uint16) bool {
	if len(q) == 0 {
		return false
	}
	lo, hi := 0, 0
	for _, v := range q {
		if v <= 2<<8 {
			lo++
		}
		if v >= 253<<8 {
			hi++
		}
	}
	return lo*200 > len(q) || hi*200 > len(q)
}

// Volts converts Q8.8 codes to volts at the tip: v = (code-128)*vpc - off,
// vpc the volts a code (probe included) and off the offset (volts at the tip).
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-266
func Volts(q []uint16, vpc, off float64, dst []float64) []float64 {
	dst = dst[:0]
	k := vpc / 256
	for _, v := range q {
		dst = append(dst, (float64(v)-32768)*k-off)
	}
	return dst
}

// percentile is the p-quantile (0..1) of x (sorted copy; 0 for none).
// TRLC-LINKS: REQ-SDS-268, REQ-SDS-269
func percentile(x []float64, p float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	i := p * float64(len(s)-1)
	lo := int(math.Floor(i))
	hi := min(lo+1, len(s)-1)
	f := i - float64(lo)
	return s[lo]*(1-f) + s[hi]*f
}

// median is the 0.5 percentile.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-268
func median(x []float64) float64 { return percentile(x, 0.5) }

// MinMaxColumns reduces x to cols columns of their lowest and highest
// values: column i covers samples i·n/cols up to (i+1)·n/cols, at least the
// one under it (a take's window drawn at any zoom).
// TRLC-LINKS: REQ-SDS-275
func MinMaxColumns(x []float64, cols int) (lo, hi []float64) {
	n := len(x)
	if n == 0 || cols <= 0 {
		return nil, nil
	}
	lo, hi = make([]float64, cols), make([]float64, cols)
	for i := 0; i < cols; i++ {
		a := min(i*n/cols, n-1)
		b := min(max(a+1, (i+1)*n/cols), n)
		l, h := x[a], x[a]
		for _, v := range x[a+1 : b] { // plain compares: the volts are finite
			if v < l {
				l = v
			}
			if v > h {
				h = v
			}
		}
		lo[i], hi[i] = l, h
	}
	return lo, hi
}
