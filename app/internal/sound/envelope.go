// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import "math"

// Envelope and ADSR. An audio capture's amplitude is followed by a peak
// detector that releases with a time constant and is read in short blocks;
// an envelope generator's CV is its own envelope (block means). The ADSR
// reading places start, peak, sustain, release and end on that envelope.

// Envelope follows the amplitude of x (volts, sampled at fs) around its mean:
// a peak detector releasing with time constant tau (s), read as the block
// maximum every block seconds. It returns the envelope and its rate.
// TRLC-LINKS: REQ-SDS-268
func Envelope(x []float64, fs, tau, block float64) ([]float64, float64) {
	if len(x) == 0 || !(fs > 0) {
		return nil, 0
	}
	b := max(1, int(math.Round(block*fs)))
	m := mean(x)
	k := 0.0
	if tau > 0 {
		k = math.Exp(-1 / (tau * fs))
	}
	out := make([]float64, 0, len(x)/b+1)
	hold, top := 0.0, 0.0
	for i, v := range x {
		a := math.Abs(v - m)
		hold *= k
		if a > hold {
			hold = a
		}
		if hold > top {
			top = hold
		}
		if (i+1)%b == 0 {
			out = append(out, top)
			top = 0
		}
	}
	if len(x)%b != 0 {
		out = append(out, top)
	}
	return out, fs / float64(b)
}

// BlockMeans averages x in blocks of block seconds: a CV read as its own
// envelope.
// TRLC-LINKS: REQ-SDS-268
func BlockMeans(x []float64, fs, block float64) ([]float64, float64) {
	if len(x) == 0 || !(fs > 0) {
		return nil, 0
	}
	b := max(1, int(math.Round(block*fs)))
	out := make([]float64, 0, len(x)/b+1)
	for i := 0; i < len(x); i += b {
		j := min(len(x), i+b)
		out = append(out, mean(x[i:j]))
	}
	return out, fs / float64(b)
}

// ADSR is an envelope's reading. Times are seconds from the capture's start;
// durations are seconds. A field with no reading is NaN.
type ADSR struct {
	OK  bool
	Why string // no reading: why

	Start, PeakAt, DecayEnd, ReleaseAt, End float64

	Floor, Peak float64 // levels (the envelope's units)

	Attack1090, Attack0100 float64
	Decay, DecayTau        float64
	Sustain                float64 // level
	SustainPct, SustainDB  float64 // of the peak (above the floor)
	HasSustain             bool
	Release, ReleaseTau    float64
}

// crossUp is the first time (interpolated, in samples) from i0 at which e
// reaches level going up, -1 when it never does before i1.
// TRLC-LINKS: REQ-SDS-268
func crossUp(e []float64, i0, i1 int, level float64) float64 {
	for i := max(1, i0); i < i1 && i < len(e); i++ {
		if e[i] >= level && e[i-1] < level {
			return float64(i-1) + (level-e[i-1])/(e[i]-e[i-1])
		}
		if i == i0 && e[i] >= level {
			return float64(i)
		}
	}
	return -1
}

// crossDown is the first time from i0 at which e falls to level, -1 never.
// TRLC-LINKS: REQ-SDS-268
func crossDown(e []float64, i0, i1 int, level float64) float64 {
	if i0 < len(e) && i0 >= 0 && e[i0] <= level {
		return float64(i0)
	}
	for i := max(1, i0+1); i < i1 && i < len(e); i++ {
		if e[i] <= level && e[i-1] > level {
			return float64(i-1) + (e[i-1]-level)/(e[i-1]-e[i])
		}
	}
	return -1
}

// expTau fits e = S + A exp(-t/tau) over [i0, i1) where e - S stays above
// lo, by a line through ln(e - S); NaN with too few points.
// TRLC-LINKS: REQ-SDS-268
func expTau(e []float64, i0, i1 int, s, lo, rate float64) float64 {
	var n, sx, sy, sxx, sxy float64
	for i := max(0, i0); i < i1 && i < len(e); i++ {
		d := e[i] - s
		if d <= lo {
			continue
		}
		t, y := float64(i)/rate, math.Log(d)
		n++
		sx += t
		sy += y
		sxx += t * t
		sxy += t * y
	}
	if n < 3 {
		return math.NaN()
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return math.NaN()
	}
	slope := (n*sxy - sx*sy) / den
	if slope >= 0 {
		return math.NaN()
	}
	return -1 / slope
}

// AnalyzeADSR reads the envelope env (rate fs). gateOn and gateOff are the
// gate's edges in seconds from the start when known (negative: found from
// the envelope). Attack runs 10-90 % and start-to-peak, decay from the peak
// to 90 % of the way down to the sustain, release from its start to a tenth
// of its starting level (the floor removed throughout).
// TRLC-LINKS: REQ-SDS-268
func AnalyzeADSR(env []float64, fs, gateOn, gateOff float64) ADSR {
	nan := math.NaN()
	r := ADSR{Start: nan, PeakAt: nan, DecayEnd: nan, ReleaseAt: nan, End: nan, Attack1090: nan, Attack0100: nan,
		Decay: nan, DecayTau: nan, Sustain: nan, SustainPct: nan, SustainDB: nan, Release: nan, ReleaseTau: nan}
	n := len(env)
	if n < 8 || !(fs > 0) {
		r.Why = "too short"
		return r
	}
	low := percentile(env, 0.05)
	pk, pi := env[0], 0
	for i, v := range env {
		if v > pk {
			pk, pi = v, i
		}
	}
	r.Floor, r.Peak = low, pk
	if !(pk-low > 1e-12) || pk-low < 0.05*math.Abs(pk) {
		r.Why = "no envelope: the level hardly moves"
		return r
	}
	// e: the envelope with the floor at 0 and the peak at 1.
	e := make([]float64, n)
	for i, v := range env {
		e[i] = (v - low) / (pk - low)
	}
	at := func(s float64) float64 { return s / fs }
	start := -1.0
	if gateOn >= 0 {
		start = gateOn * fs
		if start >= float64(pi) {
			start = -1 // the gate came after the peak: find the start
		}
	}
	if start < 0 {
		// The last quiet sample before the rise to 10 %.
		c10 := crossUp(e, 0, pi+1, 0.1)
		if c10 < 0 {
			c10 = float64(pi)
		}
		start = 0
		for i := int(c10); i >= 0; i-- {
			if e[i] <= 0.02 {
				start = float64(i)
				break
			}
		}
	}
	// The peak's first sample at its level: a held top reads where it begins.
	for i := int(start); i < pi; i++ {
		if e[i] >= 0.995 {
			pi = i
			break
		}
	}
	r.Start, r.PeakAt = at(start), at(float64(pi))
	r.Attack0100 = at(float64(pi) - start)
	a10 := crossUp(e, int(start), pi+1, 0.1)
	a90 := crossUp(e, int(start), pi+1, 0.9)
	if a10 >= 0 && a90 >= a10 {
		r.Attack1090 = at(a90 - a10)
	}
	// The sustain: the held stretch before the gate's end, or the longest
	// flat stretch after the peak.
	rel := -1
	sus := 0.0
	if gateOff >= 0 && gateOff*fs > float64(pi) {
		rel = min(n-1, int(math.Round(gateOff*fs)))
		lo := pi + (rel-pi)*3/4
		sus = median(e[lo : rel+1])
		r.HasSustain = sus > 0.03
	} else {
		bs, be := flatStretch(e, pi, fs)
		if be > bs && float64(be-bs) >= 0.1*fs {
			sus = median(e[bs:be])
			r.HasSustain = sus > 0.03
			if be < n { // a sustain held to the capture's end has no release in it
				rel = be
			}
		}
	}
	if !r.HasSustain {
		sus = 0
	}
	r.Sustain = low + sus*(pk-low)
	r.SustainPct = 100 * sus
	r.SustainDB = 20 * math.Log10(math.Max(sus, 1e-6))
	if r.HasSustain {
		dEnd := crossDown(e, pi, n, sus+0.1*(1-sus))
		if rel >= 0 {
			dEnd = math.Min(dEnd, float64(rel))
			if dEnd < 0 {
				dEnd = float64(rel)
			}
		}
		if dEnd >= 0 {
			r.DecayEnd = at(dEnd)
			r.Decay = at(dEnd - float64(pi))
			r.DecayTau = expTau(e, pi, int(dEnd)+1, sus, 0.05*(1-sus), fs)
		}
		if rel < 0 {
			r.OK = true
			return r
		}
		r.ReleaseAt = at(float64(rel))
		lr := e[rel]
		if gateOff < 0 {
			lr = sus
		}
		end := crossDown(e, rel, n, 0.1*lr)
		if end >= 0 {
			r.End = at(end)
			r.Release = at(end - float64(rel))
			r.ReleaseTau = expTau(e, rel, int(end)+1, 0, 0.05*lr, fs)
		}
		r.OK = true
		return r
	}
	// No sustain: the decay runs from the peak to a tenth (an AD or
	// percussive envelope); a gate's end, if any, starts the release.
	end := crossDown(e, pi, n, 0.1)
	if end >= 0 {
		r.DecayEnd, r.End = at(end), at(end)
		r.Decay = at(end - float64(pi))
		r.DecayTau = expTau(e, pi, int(end)+1, 0, 0.05, fs)
	}
	if rel >= 0 {
		r.ReleaseAt = at(float64(rel))
	}
	r.OK = true
	return r
}

// flatStretch is the longest stretch after the peak where e stays within
// a band about its running median (5 % of full scale, or a fifth of the
// level): [start, end) indexes, empty when none.
// TRLC-LINKS: REQ-SDS-268
func flatStretch(e []float64, from int, fs float64) (int, int) {
	bestS, bestE := 0, 0
	w := max(4, int(0.02*fs)) // judged in 20 ms steps
	for s := from; s+w <= len(e); s += w {
		lvl := median(e[s : s+w])
		if lvl < 0.03 {
			continue
		}
		band := math.Max(0.05, 0.2*lvl)
		end := s
		for end < len(e) && math.Abs(e[end]-lvl) <= band {
			end++
		}
		if end-s > bestE-bestS {
			bestS, bestE = s, end
		}
	}
	return bestS, bestE
}
