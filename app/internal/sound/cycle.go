// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import "math"

// The cycle view: a few periods of the waveform drawn from a trigger the
// FPGA pointed at (an edge mark in the stream). Software only places it:
// the level crossing next to the mark, interpolated between samples; a mark
// with no crossing beside it is dropped, never replaced by one software
// found elsewhere.

// markReach is how far (samples) from a mark the crossing it points at may
// lie: the decimator's group delay against the trigger's comparison.
const markReach = 6

// PlaceMark finds the crossing of level in the direction asked nearest
// sample mark of x, within markReach samples: its interpolated position.
// TRLC-LINKS: REQ-SDS-272
func PlaceMark(x []float64, mark int, level float64, rising bool) (float64, bool) {
	best, bd := -1.0, math.Inf(1)
	for i := max(1, mark-markReach); i <= min(len(x)-1, mark+markReach); i++ {
		a, b := x[i-1], x[i]
		var hit bool
		if rising {
			hit = a < level && b >= level
		} else {
			hit = a > level && b <= level
		}
		if !hit || a == b {
			continue
		}
		t := float64(i-1) + (level-a)/(b-a)
		if d := math.Abs(t - float64(mark)); d < bd {
			best, bd = t, d
		}
	}
	return best, best >= 0
}

// Cycle is the waveform from a placed trigger, resampled for drawing, and
// its readouts.
type Cycle struct {
	OK      bool
	Trace   []float64 // points volts over Cycles periods
	Cycles  int
	PeriodS float64
	Min     float64
	Max     float64
	Vpp     float64
	DutyPct float64 // the share of a period above the mid level
	RiseS   float64 // 10-90 %, first edge up (NaN none)
	FallS   float64
	SlewVus float64 // the steepest step, V/us
}

// catmull interpolates x at fractional position t (Catmull-Rom).
// TRLC-LINKS: REQ-SDS-272
func catmull(x []float64, t float64) float64 {
	i := int(math.Floor(t))
	f := t - float64(i)
	at := func(k int) float64 { return x[max(0, min(len(x)-1, k))] }
	p0, p1, p2, p3 := at(i-1), at(i), at(i+1), at(i+2)
	return p1 + 0.5*f*(p2-p0+f*(2*p0-5*p1+4*p2-p3+f*(3*(p1-p2)+p3-p0)))
}

// ExtractCycles takes cycles periods (period in samples) of x from the
// placed trigger at, resampled to points.
// TRLC-LINKS: REQ-SDS-272
func ExtractCycles(x []float64, fs, at, period float64, cycles, points int) Cycle {
	c := Cycle{Cycles: cycles}
	if !(period > 1) || cycles < 1 || points < 8 || at < 0 || at+period*float64(cycles) > float64(len(x)-2) {
		return c
	}
	span := period * float64(cycles)
	c.Trace = make([]float64, points)
	for i := range c.Trace {
		c.Trace[i] = catmull(x, at+span*float64(i)/float64(points-1))
	}
	seg := x[int(at) : int(math.Ceil(at+span))+1]
	l := Measure(seg)
	c.Min, c.Max, c.Vpp = l.Min, l.Max, l.Vpp
	c.PeriodS = period / fs
	mid := (l.Min + l.Max) / 2
	above := 0
	one := x[int(at):int(math.Ceil(at+period))]
	for _, v := range one {
		if v > mid {
			above++
		}
	}
	if len(one) > 0 {
		c.DutyPct = 100 * float64(above) / float64(len(one))
	}
	lo, hi := l.Min+0.1*l.Vpp, l.Min+0.9*l.Vpp
	c.RiseS, c.FallS = edgeTime(seg, lo, hi, true)/fs, edgeTime(seg, lo, hi, false)/fs
	steep := 0.0
	for i := 1; i < len(seg); i++ {
		steep = math.Max(steep, math.Abs(seg[i]-seg[i-1]))
	}
	c.SlewVus = steep * fs / 1e6
	c.OK = true
	return c
}

// edgeTime is the first 10-90 % (rising) or 90-10 % (falling) transit in
// samples, NaN when none.
// TRLC-LINKS: REQ-SDS-272
func edgeTime(x []float64, lo, hi float64, rising bool) float64 {
	start := -1.0
	for i := 1; i < len(x); i++ {
		a, b := x[i-1], x[i]
		if rising {
			if a < lo && b >= lo {
				start = float64(i-1) + (lo-a)/(b-a)
			}
			if start >= 0 && a < hi && b >= hi {
				return float64(i-1) + (hi-a)/(b-a) - start
			}
		} else {
			if a > hi && b <= hi {
				start = float64(i-1) + (a-hi)/(a-b)
			}
			if start >= 0 && a > lo && b <= lo {
				return float64(i-1) + (a-lo)/(a-b) - start
			}
		}
	}
	return math.NaN()
}
