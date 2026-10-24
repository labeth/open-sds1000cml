// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import "math"

// Stats is the running statistics of one measurement in the MEASURE workspace
// (now, mean, min, max, sdev, N; REQ-SDS-217), by Welford's update, which
// stays exact over long runs where a sum of squares would cancel. Values that
// are not finite (a measurement that failed on a capture) are not counted.
// The zero value is empty.
// TRLC-LINKS: REQ-SDS-217
type Stats struct {
	n              int
	mean, m2       float64
	min, max, last float64
}

// Add counts one value.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Add(v float64) {
	if !finite(v) {
		return
	}
	s.n++
	if s.n == 1 {
		s.mean, s.m2, s.min, s.max, s.last = v, 0, v, v, v
		return
	}
	d := v - s.mean
	s.mean += d / float64(s.n)
	s.m2 += d * (v - s.mean)
	s.min = min(s.min, v) // the builtins: math.Min's results (signed zeros too), inline
	s.max = max(s.max, v)
	s.last = v
}

// N is the number of values counted.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) N() int { return s.n }

// Last is the most recent value counted, NaN when there is none.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Last() float64 { return s.or(s.last) }

// Mean is the mean, NaN when there is no value.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Mean() float64 { return s.or(s.mean) }

// Min is the smallest value, NaN when there is none.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Min() float64 { return s.or(s.min) }

// Max is the largest value, NaN when there is none.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Max() float64 { return s.or(s.max) }

// Sdev is the standard deviation of the values counted (the population's, as
// the prototype's table showed it), NaN below two values.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Sdev() float64 {
	if s.n < 2 {
		return math.NaN()
	}
	return math.Sqrt(math.Max(0, s.m2/float64(s.n)))
}

// Reset empties the statistics (the Position push in MEASURE, or a change of
// channel, timebase, trigger or acquisition).
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Reset() { *s = Stats{} }

// or is v, or NaN when nothing was counted.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) or(v float64) float64 {
	if s.n == 0 {
		return math.NaN()
	}
	return v
}
