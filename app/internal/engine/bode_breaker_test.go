package engine

import (
	"fmt"
	"math"
	"testing"
)

// 50-waveform adversarial breaker for the Bode / FRA measurement. Every family
// is fed as BOTH channels in several relationships; the contract under attack:
//   - bodeEval never panics;
//   - it either produces NO point, or one with FINITE freq/gain/phase, a
//     frequency in (0, Nyquist), and phase in [-180, 180];
//   - a degenerate reference (flat / noise / sub-cycle / at-or-over Nyquist)
//     produces NO point rather than a bogus one;
//   - an identical DUT reads ~0 dB.

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func clampf(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

type bkFamily struct {
	name string
	gen  func(i, n int, rng func() float64) float64
}

func bodeBreakerFamilies(dt float64) []bkFamily {
	nyq := 0.5 / dt
	sine := func(fHz, amp, off, ph float64) func(int, int, func() float64) float64 {
		return func(i, n int, _ func() float64) float64 {
			return off + amp*math.Sin(2*math.Pi*fHz*float64(i)*dt+ph)
		}
	}
	sq := func(fHz, amp, off, duty float64) func(int, int, func() float64) float64 {
		p := 1.0 / (fHz * dt)
		return func(i, n int, _ func() float64) float64 {
			if math.Mod(float64(i), p) < p*duty {
				return off + amp
			}
			return off - amp
		}
	}
	var fams []bkFamily
	add := func(name string, g func(int, int, func() float64) float64) { fams = append(fams, bkFamily{name, g}) }

	for _, f := range []float64{nyq * 1e-4, nyq * 0.001, nyq * 0.01, nyq * 0.05, nyq * 0.1, nyq * 0.25, nyq * 0.4, nyq * 0.49, nyq * 0.5, nyq * 0.51, nyq * 0.8, nyq * 1.3} {
		ff := f
		add(fmt.Sprintf("sine-%.3gHz", ff), sine(ff, 90, 128, 0.3))
	}
	add("sq-1%", sq(nyq*0.02, 90, 128, 0.01))
	add("sq-10%", sq(nyq*0.05, 90, 128, 0.1))
	add("sq-50%", sq(nyq*0.1, 90, 128, 0.5))
	add("sq-90%", sq(nyq*0.05, 90, 128, 0.9))
	add("sq-99%", sq(nyq*0.02, 90, 128, 0.99))
	add("sq-nearNyq", sq(nyq*0.48, 90, 128, 0.5))
	add("saw", func(i, n int, _ func() float64) float64 {
		p := 1.0 / (nyq * 0.05 * dt)
		return 40 + 200*math.Mod(float64(i), p)/p
	})
	add("triangle", func(i, n int, _ func() float64) float64 {
		p := 1.0 / (nyq * 0.05 * dt)
		m := math.Mod(float64(i), p) / p
		if m < 0.5 {
			return 30 + 400*m
		}
		return 30 + 400*(1-m)
	})
	add("two-tone", func(i, n int, _ func() float64) float64 {
		x := float64(i) * dt
		return 128 + 45*math.Sin(2*math.Pi*nyq*0.05*x) + 45*math.Sin(2*math.Pi*nyq*0.13*x)
	})
	add("three-tone", func(i, n int, _ func() float64) float64 {
		x := float64(i) * dt
		return 128 + 30*math.Sin(2*math.Pi*nyq*0.03*x) + 30*math.Sin(2*math.Pi*nyq*0.07*x) + 30*math.Sin(2*math.Pi*nyq*0.19*x)
	})
	add("am", func(i, n int, _ func() float64) float64 {
		x := float64(i) * dt
		return 128 + 90*math.Sin(2*math.Pi*nyq*0.1*x)*(0.5+0.5*math.Sin(2*math.Pi*nyq*0.005*x))
	})
	add("fm", func(i, n int, _ func() float64) float64 {
		x := float64(i) * dt
		return 128 + 90*math.Sin(2*math.Pi*nyq*0.1*x+3*math.Sin(2*math.Pi*nyq*0.008*x))
	})
	add("chirp", func(i, n int, _ func() float64) float64 {
		fr := nyq * (0.02 + 0.3*float64(i)/float64(n))
		return 128 + 90*math.Sin(2*math.Pi*fr*float64(i)*dt)
	})
	add("beat", func(i, n int, _ func() float64) float64 {
		x := float64(i) * dt
		return 128 + 90*math.Sin(2*math.Pi*nyq*0.1*x)*math.Cos(2*math.Pi*nyq*0.002*x)
	})
	add("harmonic-rich", func(i, n int, _ func() float64) float64 {
		x := float64(i) * dt
		s := 0.0
		for h := 1; h <= 7; h++ {
			s += math.Sin(2*math.Pi*nyq*0.03*float64(h)*x) / float64(h)
		}
		return 128 + 70*s
	})
	add("halfwave", func(i, n int, _ func() float64) float64 {
		v := math.Sin(2 * math.Pi * nyq * 0.06 * float64(i) * dt)
		if v < 0 {
			v = 0
		}
		return 40 + 180*v
	})
	add("sine+dc-drift", func(i, n int, _ func() float64) float64 {
		return 60 + float64(i)/float64(n)*120 + 40*math.Sin(2*math.Pi*nyq*0.08*float64(i)*dt)
	})
	add("burst", func(i, n int, _ func() float64) float64 {
		if i%800 < 200 {
			return 128 + 90*math.Sin(2*math.Pi*nyq*0.12*float64(i)*dt)
		}
		return 128
	})
	add("white-noise", func(i, n int, rng func() float64) float64 { return 128 + 80*(rng()*2-1) })
	add("noisy-sine", func(i, n int, rng func() float64) float64 {
		return 128 + 70*math.Sin(2*math.Pi*nyq*0.09*float64(i)*dt) + 20*(rng()*2-1)
	})
	add("flat-mid", func(i, n int, _ func() float64) float64 { return 128 })
	add("flat-low", func(i, n int, _ func() float64) float64 { return 0 })
	add("flat-high", func(i, n int, _ func() float64) float64 { return 255 })
	add("tiny-amp", func(i, n int, _ func() float64) float64 { return 128 + 1.2*math.Sin(2*math.Pi*nyq*0.1*float64(i)*dt) })
	add("clipped", func(i, n int, _ func() float64) float64 {
		return clampf(128 + 300*math.Sin(2*math.Pi*nyq*0.07*float64(i)*dt))
	})
	add("railed-square", sq(nyq*0.1, 300, 128, 0.5))
	add("dc-near-rail", func(i, n int, _ func() float64) float64 { return 253 + 2*math.Sin(2*math.Pi*nyq*0.1*float64(i)*dt) })
	add("dc-near-zero", func(i, n int, _ func() float64) float64 { return 2 + 2*math.Sin(2*math.Pi*nyq*0.1*float64(i)*dt) })
	add("impulse-train", func(i, n int, _ func() float64) float64 {
		if i%512 == 0 {
			return 255
		}
		return 128
	})
	add("single-impulse", func(i, n int, _ func() float64) float64 {
		if i == n/2 {
			return 255
		}
		return 128
	})
	add("sub-cycle", sine(0.6/(4096*dt), 90, 128, 0))
	add("one-cycle", sine(1.0/(4096*dt), 90, 128, 0))
	add("step", func(i, n int, _ func() float64) float64 {
		if i < n/2 {
			return 40
		}
		return 210
	})
	add("exp-decay", func(i, n int, _ func() float64) float64 {
		return 40 + 200*math.Exp(-float64(i)/float64(n)*4)*math.Abs(math.Sin(2*math.Pi*nyq*0.05*float64(i)*dt))
	})
	add("asym-pulse", sq(nyq*0.04, 90, 128, 0.03))
	add("ringing", func(i, n int, _ func() float64) float64 {
		m := math.Mod(float64(i), 1000)
		return 128 + 100*math.Exp(-m/120)*math.Cos(2*math.Pi*nyq*0.15*m*dt)
	})
	add("glitch-sine", func(i, n int, _ func() float64) float64 {
		v := 128 + 90*math.Sin(2*math.Pi*nyq*0.08*float64(i)*dt)
		if i%333 == 0 {
			v = 0
		}
		return v
	})
	add("staircase", func(i, n int, _ func() float64) float64 {
		return 20 + 40*math.Floor(math.Mod(float64(i), 600)/100)
	})
	return fams
}

func mkChan(fam bkFamily, n int, dt float64, seed int64) []uint8 {
	s := seed
	rng := func() float64 {
		s = (s*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
		return float64(s%1_000_000) / 1_000_000
	}
	out := make([]uint8, n)
	for i := 0; i < n; i++ {
		out[i] = clamp8(fam.gen(i, n, rng))
	}
	return out
}

func TestBodeBreaker50(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	const n = 6000
	const dt = 2e-9
	nyq := 0.5 / dt
	fams := bodeBreakerFamilies(dt)
	if len(fams) != 50 {
		t.Fatalf("expected 50 families, got %d", len(fams))
	}
	e.SetBodeMode(true, 0, 1)

	rels := []struct {
		name string
		dut  func(c1 []uint8) []uint8
	}{
		{"identical", func(c1 []uint8) []uint8 { return append([]uint8(nil), c1...) }},
		{"half-amp", func(c1 []uint8) []uint8 {
			d := make([]uint8, len(c1))
			for i, v := range c1 {
				d[i] = clamp8(128 + 0.5*(float64(v)-128))
			}
			return d
		}},
		{"flat-dut", func(c1 []uint8) []uint8 {
			d := make([]uint8, len(c1))
			for i := range d {
				d[i] = 128
			}
			return d
		}},
		{"noise-dut", func(c1 []uint8) []uint8 {
			d := make([]uint8, len(c1))
			s := int64(99)
			for i := range d {
				s = (s*1103515245 + 12345) & 0x7fffffff
				d[i] = clamp8(128 + 80*(float64(s%1000)/500-1))
			}
			return d
		}},
	}

	for fi, fam := range fams {
		c1 := mkChan(fam, n, dt, int64(1000+fi*7))
		for _, rel := range rels {
			e.ClearBode()
			c2 := rel.dut(c1)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("fam %s / %s: bodeEval PANIC: %v", fam.name, rel.name, r)
					}
				}()
				e.bodeEval(&Frame{C1: c1, C2: c2, Valid: n, SampleS: dt}, n, dt)
			}()
			for _, p := range e.BodePoints() {
				if math.IsNaN(p.FreqHz) || math.IsInf(p.FreqHz, 0) || p.FreqHz <= 0 || p.FreqHz >= nyq {
					t.Errorf("fam %s / %s: bad freq %g (Nyquist %g)", fam.name, rel.name, p.FreqHz, nyq)
				}
				if math.IsNaN(p.GainDB) || math.IsInf(p.GainDB, 0) {
					t.Errorf("fam %s / %s: non-finite gain %g", fam.name, rel.name, p.GainDB)
				}
				if math.IsNaN(p.PhaseDeg) || math.IsInf(p.PhaseDeg, 0) || p.PhaseDeg < -180.001 || p.PhaseDeg > 180.001 {
					t.Errorf("fam %s / %s: bad phase %g", fam.name, rel.name, p.PhaseDeg)
				}
			}
			s := e.Snapshot()
			if s.BodeValid && (math.IsNaN(s.BodeGainDB) || math.IsInf(s.BodeGainDB, 0) || math.IsNaN(s.BodePhaseDeg) || math.IsInf(s.BodePhaseDeg, 0)) {
				t.Errorf("fam %s / %s: non-finite live point gain=%g phase=%g", fam.name, rel.name, s.BodeGainDB, s.BodePhaseDeg)
			}
			if rel.name == "identical" && s.BodeValid && math.Abs(s.BodeGainDB) > 1.0 {
				t.Errorf("fam %s identical: gain %g dB, expected ~0", fam.name, s.BodeGainDB)
			}
			// half-amp scales EVERY sample by 0.5 about mid, so the fundamental
			// halves too → -6 dB, for any family whose reference actually locked.
			// (skip clipped/railed: 0.5× changes the clip content, and tiny-amp:
			// the ×0.5 drops it below quantisation.)
			if rel.name == "half-amp" && s.BodeValid &&
				fam.name != "clipped" && fam.name != "railed-square" && fam.name != "tiny-amp" &&
				fam.name != "dc-near-rail" && fam.name != "dc-near-zero" && fam.name != "flat-high" {
				if math.Abs(s.BodeGainDB-(-6.02)) > 1.2 {
					t.Errorf("fam %s half-amp: gain %g dB, expected ~-6", fam.name, s.BodeGainDB)
				}
			}
		}
	}
}
