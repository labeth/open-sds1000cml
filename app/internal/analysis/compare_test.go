// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"math"
	"testing"
)

// squareDiv is a square wave in divisions: +-2 div, period samples long,
// shifted by shift samples, with noise (div rms).
// TRLC-LINKS: REQ-SDS-218
func squareDiv(n, period int, shift float64, noise float64, seed uint64) []float64 {
	r := rng{s: seed}
	out := make([]float64, n)
	for i := range out {
		ph := math.Mod(float64(i)+shift, float64(period))
		if ph < 0 {
			ph += float64(period)
		}
		v := -2.0
		if ph < float64(period)/2 {
			v = 2
		}
		out[i] = v + noise*r.gauss()
	}
	return out
}

// TestRefDiff: identical traces, an offset, a gap, and nothing comparable.
// TRLC-LINKS: REQ-SDS-218
func TestRefDiff(t *testing.T) {
	ref := squareDiv(1000, 100, 0, 0, 1)
	if mx, rms, pct := RefDiff(ref, ref, 0.5); mx != 0 || rms != 0 || pct != 100 {
		t.Errorf("identical: %v %v %v", mx, rms, pct)
	}
	live := append([]float64(nil), ref...)
	for i := range live {
		live[i] += 0.3
	}
	for i := 0; i < 100; i++ {
		live[i] += 0.5 // 10 % of the samples 0.8 div off
	}
	mx, rms, pct := RefDiff(live, ref, 0.5)
	if math.Abs(mx-0.8) > 1e-12 || math.Abs(pct-90) > 1e-9 || math.Abs(rms-math.Sqrt(0.1*0.64+0.9*0.09)) > 1e-12 {
		t.Errorf("offset: max %v rms %v within %v %%", mx, rms, pct)
	}
	// gaps on either side are skipped (two of the 0.8-off samples here); the
	// common length counts
	live[3], ref[5] = math.NaN(), math.NaN()
	if _, _, pct := RefDiff(live[:500], ref, 0.5); math.Abs(pct-100*400.0/498) > 1e-9 {
		t.Errorf("gaps and a shorter live: within %v %%", pct)
	}
	for _, tc := range [][2][]float64{{nil, nil}, {nil, ref}, {{math.NaN()}, {1}}} {
		if mx, rms, pct := RefDiff(tc[0], tc[1], 0.5); !math.IsNaN(mx) || !math.IsNaN(rms) || !math.IsNaN(pct) {
			t.Errorf("nothing comparable: %v %v %v", mx, rms, pct)
		}
	}
	if _, _, pct := RefDiff([]float64{1}, []float64{1}, math.NaN()); pct != 100 {
		t.Errorf("NaN tolerance (taken as 0): %v", pct)
	}
}

// TestRepeats: captures of a repeating trigger repeat; a free-running trigger
// (another phase each capture), a glitch, gaps and mismatched lengths.
// TRLC-LINKS: REQ-SDS-218
func TestRepeats(t *testing.T) {
	const tol, slack = 0.25, 6 // the prototype's rule: +-0.25 div, +-6 samples
	a := squareDiv(2048, 200, 0, 0.03, 1)
	b := squareDiv(2048, 200, 2.5, 0.03, 2) // trigger jitter of a few samples
	if !Repeats(a, b, tol, slack) {
		t.Error("a stable trigger with 2.5 samples of jitter does not repeat")
	}
	if Repeats(a, squareDiv(2048, 200, 37, 0.03, 3), tol, slack) {
		t.Error("a capture 37 samples off (free-running trigger) repeats")
	}
	glitch := append([]float64(nil), b...)
	glitch[1000] += 1.5 // inside a level
	if Repeats(a, glitch, tol, slack) || Repeats(glitch, a, tol, slack) {
		t.Error("a 1.5 div glitch repeats (the test runs both ways)")
	}
	gaps := append([]float64(nil), b...)
	for i := 300; i < 400; i++ {
		gaps[i] = math.NaN()
	}
	if !Repeats(a, gaps, tol, slack) || !Repeats(gaps, a, tol, slack) {
		t.Error("gaps break the repeat")
	}
	if Repeats(a, b[:2000], tol, slack) || Repeats(nil, nil, tol, slack) {
		t.Error("different lengths or nothing repeat")
	}
	nan := make([]float64, 64)
	for i := range nan {
		nan[i] = math.NaN()
	}
	if Repeats(nan, nan, tol, slack) || Repeats(a, b, math.NaN(), slack) || Repeats(a, b, -1, slack) {
		t.Error("nothing testable, or a bad tolerance, repeats")
	}
	// slack 0 sees the trigger jitter at the edges; a negative slack is 0
	if Repeats(a, b, tol, 0) || Repeats(a, b, tol, -3) {
		t.Error("2.5 samples of jitter pass with no slack")
	}
	// a huge slack keeps it O(n): the whole record is one window
	big := squareDiv(1<<20, 400, 0, 0.03, 5)
	if !Repeats(big, big, tol, 1<<19) {
		t.Error("a capture does not repeat itself")
	}
}

// BenchmarkRepeats: a 1M-sample capture against the last, mask tolerance.
// TRLC-LINKS: REQ-SDS-218
func BenchmarkRepeats(b *testing.B) {
	x := squareDiv(1<<20, 400, 0, 0.03, 1)
	y := squareDiv(1<<20, 400, 1.5, 0.03, 2)
	for b.Loop() {
		Repeats(x, y, 0.25, 6)
	}
}
