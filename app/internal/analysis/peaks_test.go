// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"math"
	"testing"
)

// comp is one sine component: cycles per record, amplitude (codes), phase.
// TRLC-LINKS: REQ-SDS-216
type comp struct{ cyc, amp, ph float64 }

// tones samples a sum of sines around code 128 with Gaussian noise, rounded
// to codes when round is set.
// TRLC-LINKS: REQ-SDS-216
func tones(n int, cs []comp, noise float64, seed uint64, round bool) []float64 {
	r := rng{s: seed}
	out := make([]float64, n)
	for i := range out {
		v := 128.0
		for _, c := range cs {
			v += c.amp * math.Sin(2*math.Pi*c.cyc*float64(i)/float64(n)+c.ph)
		}
		v += noise * r.gauss()
		if round {
			v = math.Round(v)
		}
		out[i] = math.Max(0, math.Min(255, v))
	}
	return out
}

// squareComps is a square wave of amplitude a (peak) at f0 cycles, band
// limited to its odd harmonics up to top: (4/pi) a/n each.
// TRLC-LINKS: REQ-SDS-216
func squareComps(f0, a float64, top int) []comp {
	var cs []comp
	for n := 1; n <= top; n += 2 {
		cs = append(cs, comp{f0 * float64(n), 4 / math.Pi * a / float64(n), 0})
	}
	return cs
}

// triangleComps: (8/pi^2) a/n^2, alternating sign, odd harmonics.
// TRLC-LINKS: REQ-SDS-216
func triangleComps(f0, a float64, top int) []comp {
	var cs []comp
	for n := 1; n <= top; n += 2 {
		ph := 0.0
		if (n/2)%2 == 1 {
			ph = math.Pi
		}
		cs = append(cs, comp{f0 * float64(n), 8 / (math.Pi * math.Pi) * a / float64(n*n), ph})
	}
	return cs
}

const peakSampleS = 1e-6

// peaksOf runs the Go pipeline: the web's spectrum, then Peaks.
// TRLC-LINKS: REQ-SDS-216
func peaksOf(samples []float64, maxPeaks int) ([]Peak, []float64, float64) {
	db := SpectrumDB(samples)
	binHz := 1 / (float64(2*len(db)) * peakSampleS)
	return Peaks(db, binHz, maxPeaks), db, binHz
}

// TestPeaksAndTHDOfWaveforms: a square wave's odd harmonics and THD, a
// triangle's, and a sine with a -40 dB second harmonic.
// TRLC-LINKS: REQ-SDS-216
func TestPeaksAndTHDOfWaveforms(t *testing.T) {
	const n = 4096
	sq := math.Sqrt(1.0/9 + 1.0/25 + 1.0/49 + 1.0/81)        // H3..H9 of 1/n
	tri := math.Sqrt(1.0/81 + 1.0/625 + 1.0/2401 + 1.0/6561) // H3..H9 of 1/n^2
	for _, tc := range []struct {
		name    string
		samples []float64
		f0      float64 // cycles per record
		thd     float64 // fraction
		tol     float64 // on Pct
		harms   []int
	}{
		{"square 31.7 bins", tones(n, squareComps(31.7, 80, 15), 0.5, 1, true), 31.7, sq, 1.5, []int{1, 3, 5, 7, 9}},
		{"square on a bin", tones(n, squareComps(32, 80, 15), 0.5, 2, true), 32, sq, 1.5, []int{1, 3, 5, 7, 9}},
		{"triangle 23.4 bins", tones(n, triangleComps(23.4, 90, 15), 0.3, 3, false), 23.4, tri, 0.5, []int{1, 3, 5, 7, 9}},
		{"sine + H2 at -40 dB", tones(n, []comp{{57.25, 90, 0}, {114.5, 0.9, 1}}, 0.05, 4, false),
			57.25, 0.01, 0.05, []int{1, 2}},
	} {
		ps, db, binHz := peaksOf(tc.samples, 8)
		th := Harmonics(ps, 0)
		f0 := tc.f0 * binHz
		if th.Fund < 0 || math.Abs(th.F0-f0) > 0.05*binHz {
			t.Errorf("%s: fundamental %.2f Hz (index %d), want %.2f", tc.name, th.F0, th.Fund, f0)
			continue
		}
		if math.Abs(th.Pct-100*tc.thd) > tc.tol {
			t.Errorf("%s: THD %.2f %%, want %.2f %%", tc.name, th.Pct, 100*tc.thd)
		}
		if want := 20 * math.Log10(th.Pct/100); math.Abs(th.DB-want) > 1e-9 {
			t.Errorf("%s: THD %.3f dB, want %.3f", tc.name, th.DB, want)
		}
		var got []int
		for i, p := range ps {
			if p.Harm > 0 {
				got = append(got, p.Harm)
				if want := float64(p.Harm) * f0; math.Abs(p.Freq-want) > 0.1*binHz {
					t.Errorf("%s: H%d at %.2f Hz, want %.2f", tc.name, p.Harm, p.Freq, want)
				}
			}
			if i > 0 && !(p.Freq > ps[i-1].Freq) {
				t.Errorf("%s: peaks not in frequency order", tc.name)
			}
			if p.RelDB > 1e-12 || p.DB < db[p.Bin]-1e-12 {
				t.Errorf("%s: peak %d level %.3f (bin %.3f) rel %.3f", tc.name, i, p.DB, db[p.Bin], p.RelDB)
			}
		}
		if len(got) != len(tc.harms) {
			t.Errorf("%s: harmonics %v, want %v", tc.name, got, tc.harms)
		} else {
			for i := range got {
				if got[i] != tc.harms[i] {
					t.Errorf("%s: harmonics %v, want %v", tc.name, got, tc.harms)
					break
				}
			}
		}
		if th.N != len(tc.harms)-1 {
			t.Errorf("%s: %d harmonics counted, want %d", tc.name, th.N, len(tc.harms)-1)
		}
		// a measured fundamental gives the same answer
		if th2 := Harmonics(ps, f0*1.0005); math.Abs(th2.Pct-th.Pct) > 1e-9 || th2.Fund != th.Fund {
			t.Errorf("%s: with a measured f0 THD %.3f (fund %d), largest-peak %.3f (fund %d)",
				tc.name, th2.Pct, th2.Fund, th.Pct, th.Fund)
		}
		t.Logf("%-20s THD %.2f %% (%.2f dB, %d harmonics), want %.2f %%", tc.name, th.Pct, th.DB, th.N, 100*tc.thd)
	}
}

// TestPeakLevelBetweenBins: a tone between two bins reads up to 1.42 dB low
// in its Hann bin; the corrected level stays within 0.06 dB at any offset.
// TRLC-LINKS: REQ-SDS-216
func TestPeakLevelBetweenBins(t *testing.T) {
	const n = 4096
	for _, off := range []float64{0, 0.1, 0.2, 0.25, 0.3, 0.4, 0.45, 0.5, 0.6, 0.75, 0.9} {
		ps, db, _ := peaksOf(tones(n, []comp{{100 + off, 60, 0}, {300, 30, 0.4}}, 0, 0, false), 4)
		if len(ps) != 2 {
			t.Fatalf("offset %.2f: %d peaks, want 2", off, len(ps))
		}
		// against the on-bin 30-code tone, the 60-code tone is +6.02 dB
		binErr := db[ps[0].Bin] - ps[1].DB - 20*math.Log10(2)
		lvlErr := ps[0].DB - ps[1].DB - 20*math.Log10(2)
		if math.Abs(lvlErr) > 0.06 {
			t.Errorf("offset %.2f bins: level %.3f dB off (the bin %.3f dB)", off, lvlErr, binErr)
		}
		if off == 0.5 && binErr > -1.4 {
			t.Errorf("half a bin off the bin reads %.3f dB, want about -1.42", binErr)
		}
	}
}

// TestPeaksDegenerate: no panic on empty, flat, NaN or infinite spectra;
// maxPeaks trims to the strongest; Harmonics on nothing.
// TRLC-LINKS: REQ-SDS-216
func TestPeaksDegenerate(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	for _, db := range [][]float64{nil, {}, {0, 0, 0, 0}, make([]float64, 64),
		{nan, nan, nan, nan, nan, nan}, {0, -1, -2, inf, -2, -1, 0}, {-inf, -inf, -inf, -inf, -inf, -inf}} {
		if ps := Peaks(db, 10, 8); len(ps) != 0 {
			t.Errorf("Peaks(%v) = %v", db, ps)
		}
	}
	if ps := Peaks([]float64{1, 2, 3, 2, 1}, 10, 8); len(ps) != 1 || ps[0].Bin != 2 {
		t.Errorf("Peaks of a 5-bin bump: %+v", ps)
	}
	if ps := Peaks([]float64{-90, -80, -10, 0, -10, -80, -90}, 10, 8); len(ps) != 1 || ps[0].Bin != 3 || ps[0].Freq != 30 {
		t.Errorf("single peak: %+v", ps)
	}
	// -Inf neighbours (a log of zero) still give a finite frequency and level
	ps := Peaks([]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1), -3, math.Inf(-1), math.Inf(-1), math.Inf(-1)}, 10, 8)
	if len(ps) != 1 || ps[0].Freq != 30 || ps[0].DB != -3 {
		t.Errorf("-Inf neighbours: %+v", ps)
	}
	db := SpectrumDB(tones(2048, []comp{{40, 50, 0}, {90, 40, 0}, {150, 30, 0}, {220, 20, 0}}, 0.2, 7, false))
	top2 := Peaks(db, 1, 2)
	if len(top2) != 2 || top2[0].Bin != 40 || top2[1].Bin != 90 {
		t.Errorf("maxPeaks 2 keeps %+v, want bins 40 and 90", top2)
	}
	if Peaks(db, 1, 0) != nil || Peaks(db, 1, -1) != nil {
		t.Error("maxPeaks <= 0 must find nothing")
	}
	if th := Harmonics(nil, 0); th.Fund != -1 || !math.IsNaN(th.Pct) || th.N != 0 {
		t.Errorf("Harmonics(nil) = %+v", th)
	}
	if th := Harmonics([]Peak{{Freq: nan, DB: nan}}, 0); th.Fund != -1 || !math.IsNaN(th.Pct) {
		t.Errorf("Harmonics(NaN peak) = %+v", th)
	}
	one := []Peak{{Freq: 1000, DB: -3, Bin: 10}}
	if th := Harmonics(one, 0); th.Fund != 0 || th.N != 0 || !math.IsNaN(th.Pct) || one[0].Harm != 1 {
		t.Errorf("a lone sine: %+v, harm %d", th, one[0].Harm)
	}
	if th := Harmonics(one, 333); th.Fund != -1 || one[0].Harm != 3 {
		t.Errorf("f0 a third of the peak: %+v, harm %d", th, one[0].Harm)
	}
	if SpectrumDB(make([]float64, 15)) != nil {
		t.Error("SpectrumDB needs 16 samples")
	}
	if db := SpectrumDB(make([]float64, 40)); len(db) != 16 {
		t.Errorf("SpectrumDB of 40 samples: %d bins, want 16 (the 32-sample prefix)", len(db))
	}
}

// BenchmarkPeaks: an 8192-point spectrum's peaks and THD.
// TRLC-LINKS: REQ-SDS-216
func BenchmarkPeaks(b *testing.B) {
	db := SpectrumDB(tones(8192, squareComps(41.3, 80, 31), 0.5, 1, true))
	b.ReportAllocs()
	for b.Loop() {
		Harmonics(Peaks(db, 100, 8), 0)
	}
}
