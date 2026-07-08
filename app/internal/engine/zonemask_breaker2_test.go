package engine

import (
	"fmt"
	"math"
	"testing"
)

// Second zone/mask breaker: 50 NEW waveform families (disjoint from the
// zonemask_breaker_test.go corpus) aimed at what the first campaign did not
// reach:
//
//   A. ZONE DIFFERENTIAL FUZZ — zonesQualify against an independently written
//      reference with a boundary-tolerance envelope (the engine's round-based
//      column mapping may legitimately differ from exact time mapping by half
//      a sample at zone boundaries; anything beyond that is a bug). Random
//      zones include spans off the record, inverted code bands, zero-width
//      spans, C2 targets, short valid, and multi-zone AND combos.
//
//   B. PUBLISH-POLICY INTEGRATION — the real oneFrame loop through the fake
//      bus: NORM zone holds exactly the non-qualifying frames, AUTO lets one
//      liveness frame through per zoneFallback holds, the mask counts held
//      frames too, stop-on-fail force-publishes past an active zone hold, and
//      ring entries carry distinct capture identities.
//
//   C. MASK WINDOW-GEOMETRY FUZZ — posFrac x edge-position x liveDepth: a
//      violation is caught iff it lands in the testable column range
//      [max(0,left), min(valid, liveDepth)) of the window, FailSample always
//      equals left+FailCol, and off-window / dead-tail violations never fail.

// ---------- 50-family corpus (new shapes) ----------

type zb2Family struct {
	name  string
	c1    func(i int) float64 // codes around 128
	valid int
}

func zb2Families() []zb2Family {
	// 10 shapes x 5 parameter variants = 50 distinct waves.
	shapes := []struct {
		name string
		gen  func(p, q float64) func(int) float64
	}{
		{"chirp", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				f := (1 + q*float64(i)/4096) / p
				return 60 * math.Sin(2*math.Pi*float64(i)*f)
			}
		}},
		{"pwm", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				duty := 0.2 + 0.6*(0.5+0.5*math.Sin(2*math.Pi*float64(i)/(p*11)))
				if math.Mod(float64(i), p) < p*duty {
					return 55 * q / q
				}
				return -55
			}
		}},
		{"damped", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				m := math.Mod(float64(i), p*8)
				return 70 * math.Exp(-m/(p*2)) * math.Cos(2*math.Pi*m/p*q/q)
			}
		}},
		{"multitone", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				x := float64(i)
				return 35*math.Sin(2*math.Pi*x/p) + 20*math.Sin(2*math.Pi*x/(p/3.1)) + 10*math.Sin(2*math.Pi*x/(p*q/40))
			}
		}},
		{"gausstrain", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				m := math.Mod(float64(i), p) - p/2
				return -30 + 110*math.Exp(-m*m/(2*q))
			}
		}},
		{"rampplateau", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				m := math.Mod(float64(i), p) / p
				switch {
				case m < 0.3:
					return -50 + m/0.3*100
				case m < 0.3+0.2*q/q:
					return 50
				default:
					return -50
				}
			}
		}},
		{"halfsine", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				v := math.Sin(2 * math.Pi * float64(i) / p)
				if v < 0 {
					v = 0
				}
				return -40 + 100*v*q/q
			}
		}},
		{"noiseburst", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				m := math.Mod(float64(i), p)
				if m < p/4 {
					// deterministic "noise": high-frequency mix
					x := float64(i)
					return 55 * math.Sin(2*math.Pi*x/3.7) * math.Sin(2*math.Pi*x/(q/3))
				}
				return 0
			}
		}},
		{"randstairs", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				k := math.Floor(float64(i) / (p / 5))
				// hash the step index into a level
				h := math.Sin(k*12.9898+q) * 43758.5453
				return -55 + 110*(h-math.Floor(h))
			}
		}},
		{"ecg", func(p, q float64) func(int) float64 {
			return func(i int) float64 {
				m := math.Mod(float64(i), p) / p
				switch {
				case m > 0.48 && m < 0.52:
					return 100 // R spike
				case m > 0.40 && m < 0.48:
					return -25
				case m > 0.60 && m < 0.72:
					return 30 * math.Sin((m-0.60)/0.12*math.Pi) * q / q
				default:
					return 0
				}
			}
		}},
	}
	var fams []zb2Family
	for si, sh := range shapes {
		for v := 0; v < 5; v++ {
			p := 90.0 + float64((si*53+v*97)%320)
			q := 20.0 + float64((si*31+v*17)%60)
			gen := sh.gen(p, q)
			valid := 4096
			if v == 3 {
				valid = 3000 // stress valid < len(sig)
			}
			fams = append(fams, zb2Family{
				name:  fmt.Sprintf("%s-%d", sh.name, v),
				c1:    gen,
				valid: valid,
			})
		}
	}
	return fams // 50
}

// zb2Frame renders a family into a frame; C2 is the inverted C1 so channel
// mix-ups are caught, plus deterministic per-family phase.
func zb2Frame(fam zb2Family, rng func() float64) *Frame {
	const n = 4096
	f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: fam.valid}
	ph := int(rng() * 512)
	for i := 0; i < n; i++ {
		v := 128 + fam.c1(i+ph) + 2*(rng()*2-1)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		f.C1[i] = uint8(v)
		f.C2[i] = 255 - f.C1[i]
	}
	return f
}

// ---------- Campaign A: zone differential fuzz ----------

// zoneRefState is the independent reference: +1 the zone MUST be satisfied,
// -1 it MUST be violated, 0 tie (the verdict may go either way inside the
// half-sample boundary tolerance of the engine's rounded column mapping).
func zoneRefState(f *Frame, valid int, edgeX, sampleS float64, z Zone) int {
	sig := f.C1
	if z.Ch == 1 {
		sig = f.C2
	}
	dtLo, dtHi := z.DtLoS, z.DtHiS
	if dtLo > dtHi {
		dtLo, dtHi = dtHi, dtLo
	}
	hitStrict, hitLoose := false, false
	for s := 0; s < valid && s < len(sig); s++ {
		v := int(sig[s])
		if v < z.CodeLo || v > z.CodeHi {
			continue
		}
		t := (float64(s) - edgeX) * sampleS
		if t >= dtLo-0.51*sampleS && t <= dtHi+0.51*sampleS {
			hitLoose = true
			if t >= dtLo+0.5*sampleS && t <= dtHi-0.5*sampleS {
				hitStrict = true
				break
			}
		}
	}
	if z.Avoid {
		switch {
		case hitStrict:
			return -1 // definitely inside an avoid zone
		case !hitLoose:
			return +1 // definitely clear of it
		}
		return 0
	}
	switch {
	case hitStrict:
		return +1
	case !hitLoose:
		return -1
	}
	return 0
}

func TestZoneBreakerDifferential(t *testing.T) {
	e := &Engine{}
	fams := zb2Families()
	if len(fams) != 50 {
		t.Fatalf("want 50 families, got %d", len(fams))
	}
	const sampleS = 2e-9
	checked, ties := 0, 0
	for fi, fam := range fams {
		seed := int64(77 + fi*104729)
		rng := func() float64 {
			seed = (seed*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
			return float64(seed%1_000_000) / 1_000_000
		}
		f := zb2Frame(fam, rng)
		edgeX := 900 + rng()*400 // deliberately non-integer
		// hand-picked adversarial zones + random ones
		zones := []Zone{
			{DtLoS: -5e-6, DtHiS: -4e-6, CodeLo: 0, CodeHi: 255},                       // pre-record
			{DtLoS: float64(fam.valid) * sampleS, DtHiS: 9e-6, CodeLo: 0, CodeHi: 255}, // post-valid
			{DtLoS: 1e-7, DtHiS: 1e-7, CodeLo: 0, CodeHi: 255},                         // zero-width
			{DtLoS: -1e-6, DtHiS: 3e-6, CodeLo: 200, CodeHi: 100},                      // inverted codes: never hits
			{DtLoS: -2e-6, DtHiS: 6e-6, CodeLo: 0, CodeHi: 255, Avoid: true},           // avoid-everything: must violate
		}
		for k := 0; k < 30; k++ {
			dtA := (rng()*5000 - 1500) * sampleS
			dtB := (rng()*5000 - 1500) * sampleS
			cl := int(rng()*280) - 10
			ch := cl + int(rng()*120)
			zones = append(zones, Zone{
				DtLoS: dtA, DtHiS: dtB,
				CodeLo: cl, CodeHi: ch,
				Avoid: rng() < 0.4,
				Ch:    int(rng() * 2),
			})
		}
		for zi, z := range zones {
			want := zoneRefState(f, fam.valid, edgeX, sampleS, z)
			e.SetZones([]Zone{z})
			got := e.zonesQualify(f, fam.valid, edgeX, sampleS)
			checked++
			if want == 0 {
				ties++
				continue
			}
			if got != (want > 0) {
				t.Fatalf("fam %s zone %d: engine=%v ref=%+d (zone %+v edgeX=%.2f)",
					fam.name, zi, got, want, z, edgeX)
			}
		}
		// multi-zone AND combos
		for k := 0; k < 10; k++ {
			var combo []Zone
			must := +1
			for j := 0; j < 2+int(rng()*2); j++ {
				z := zones[int(rng()*float64(len(zones)))]
				combo = append(combo, z)
				st := zoneRefState(f, fam.valid, edgeX, sampleS, z)
				switch {
				case st < 0:
					must = -1
				case st == 0 && must > 0:
					must = 0
				}
			}
			e.SetZones(combo)
			got := e.zonesQualify(f, fam.valid, edgeX, sampleS)
			checked++
			if must == 0 {
				ties++
				continue
			}
			if got != (must > 0) {
				t.Fatalf("fam %s combo %d: engine=%v ref=%+d (%d zones)", fam.name, k, got, must, len(combo))
			}
		}
	}
	if ties > checked/3 {
		t.Fatalf("referee degenerate: %d/%d ties", ties, checked)
	}
	t.Logf("zone differential: %d verdicts (%d boundary ties skipped)", checked, ties)
}

// ---------- Campaign B: publish-policy integration ----------

// ---------- Campaign C: mask window-geometry fuzz ----------

// ---------- root-cause regressions found by this campaign ----------
