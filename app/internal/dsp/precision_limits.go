package dsp

import "math"

// PrecisionLimits returns the ideal white-noise gain and -3dB frequency
// (normalized to OUTPUT sample rate) of CIC3 /R followed by our FIR. This
// excludes ADC nonlinearity, correlated noise, timing error and Q8 rounding.
func PrecisionLimits(reduction int) (gain, bandwidth float64) {
	r := float64(reduction)
	r2 := r * r
	r3 := r2 * r
	r5 := r3 * r2
	// Autocorrelation of three normalized R-point boxcars, at lags 0,R,2R.
	h0 := 11/(20*r) + 1/(4*r3) + 1/(5*r5)
	h1 := 13/(60*r) - 1/(12*r3) - 2/(15*r5)
	h2 := 1/(120*r) - 1/(24*r3) + 1/(30*r5)
	var cc [3]float64
	for lag := 0; lag < 3; lag++ {
		for i := lag; i < len(precisionTaps); i++ {
			cc[lag] += float64(precisionTaps[i]) * float64(precisionTaps[i-lag]) / (1 << 44)
		}
	}
	gain = 1 / math.Sqrt(h0*cc[0]+2*h1*cc[1]+2*h2*cc[2])
	amplitude := func(f float64) float64 {
		cic := math.Pow(math.Sin(math.Pi*f)/(r*math.Sin(math.Pi*f/r)), 3)
		fir := 0.0
		for i, c := range precisionTaps {
			fir += float64(c) / (1 << 22) * math.Cos(2*math.Pi*f*float64(i-PrecisionGuard))
		}
		return math.Abs(cic * fir)
	}
	lo, hi := 0.075, .5
	target := math.Pow(10, -3.0/20)
	for i := 0; i < 45; i++ {
		f := (lo + hi) / 2
		if amplitude(f) > target {
			lo = f
		} else {
			hi = f
		}
	}
	return gain, (lo + hi) / 2
}
