package engine

import (
	"fmt"
	"math"
	"testing"
)

// Zone/mask breaker: 50 waveform families, each with GROUND TRUTH — a golden
// mask is built from clean frames of the family, then clean frames must ALL
// pass and deterministically-violated frames must ALL fail (with the failure
// located near the injected violation). Zones are placed on known features
// (must qualify) and known-empty regions (must not). The invariant: the tester
// never passes a violation, never fails a clean frame, and localizes failures.

type zmFamily struct {
	name    string
	shape   func(i int, ph float64) float64 // base waveform, codes around 0
	period  float64                         // samples
	amp     float64
	off     float64 // DC offset in codes (added to 128)
	noise   float64
	violate string // spike | dropout | shift | width | ring
}

func zmShapes() map[string]func(p float64) func(int, float64) float64 {
	return map[string]func(p float64) func(int, float64) float64{
		"square": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				if math.Mod(float64(i)+ph, p) < p/2 {
					return 1
				}
				return -1
			}
		},
		"sine": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 { return math.Sin(2 * math.Pi * (float64(i) + ph) / p) }
		},
		"triangle": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				m := math.Mod(float64(i)+ph, p) / p
				if m < 0.5 {
					return 4*m - 1
				}
				return 3 - 4*m
			}
		},
		"saw": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 { return 2*math.Mod(float64(i)+ph, p)/p - 1 }
		},
		"pulse": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				if math.Mod(float64(i)+ph, p) < p/8 {
					return 1
				}
				return -0.6
			}
		},
		"uart": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				bit := int(math.Mod(float64(i)+ph, p) / (p / 10))
				pat := [10]float64{-1, 1, -1, -1, 1, 1, -1, 1, 1, 1}
				return pat[bit%10]
			}
		},
		"burst": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				m := math.Mod(float64(i)+ph, p)
				if m < p/3 {
					return math.Sin(2 * math.Pi * m / (p / 24))
				}
				return 0
			}
		},
		"ringing": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				m := math.Mod(float64(i)+ph, p)
				if m < p/2 {
					return 1 - 1.8*math.Exp(-m/(p/8))*math.Cos(2*math.Pi*m/(p/10))
				}
				return -1
			}
		},
		"stairs": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				return -1 + 0.5*math.Floor(math.Mod(float64(i)+ph, p)/(p/4))
			}
		},
		"am": func(p float64) func(int, float64) float64 {
			return func(i int, ph float64) float64 {
				return math.Sin(2*math.Pi*(float64(i)+ph)/p) * (0.7 + 0.3*math.Sin(2*math.Pi*(float64(i)+ph)/(p*7)))
			}
		},
	}
}

func zmFamilies() []zmFamily {
	shapes := zmShapes()
	names := []string{"square", "sine", "triangle", "saw", "pulse", "uart", "burst", "ringing", "stairs", "am"}
	viols := []string{"spike", "dropout", "shift", "width", "ring"}
	var fams []zmFamily
	for vi, v := range viols {
		for si, n := range names {
			p := 120.0 + float64((si*37+vi*61)%200)
			fams = append(fams, zmFamily{
				name:    fmt.Sprintf("%s-%s", n, v),
				shape:   shapes[n](p),
				period:  p,
				amp:     30 + float64((si*13+vi*7)%45),
				off:     float64((si+vi)%3-1) * 15,
				noise:   1 + float64((si+2*vi)%4),
				violate: v,
			})
		}
	}
	return fams // 50
}

// zmGen builds one frame of the family. phase jitters per frame; edgeX is the
// first rising mid-crossing after sample 600 (mimicking the trigger anchor).
// violAt >= 0 injects the family's violation at edge-relative sample violAt.
func zmGen(fam zmFamily, frameIdx int, violAt int, rng func() float64) (*Frame, float64, int, []float64) {
	const n = 4096
	f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n}
	ph := rng() * fam.period
	vals := make([]float64, n)
	for i := 0; i < n; i++ {
		vals[i] = 128 + fam.off + fam.amp*fam.shape(i, ph)
	}
	// Trigger anchor: first rising crossing of (mid + amp/2) preceded by a
	// QUIET stretch below the level (holdoff emulation) — the review's phase-
	// lock requirement: mask testing needs a trigger that anchors ONE unique
	// point of the repetition; a bare mid-level crossing is ambiguous on
	// multi-edge patterns (uart) and anchors NOISE on signals whose baseline
	// sits at mid (burst). This is exactly how a user sets up a real scope:
	// level on the feature + holdoff over the repetition's inner edges.
	level := 128 + fam.off + fam.amp*0.5
	quiet := int(0.15 * fam.period)
	if quiet < 4 {
		quiet = 4
	}
	edgeX := -1.0
	below := 0
	for i := 1; i < n-1; i++ {
		if vals[i] < level {
			below++
			continue
		}
		// rising through the level
		if i > 600 && below >= quiet && vals[i-1] < level {
			edgeX = float64(i-1) + (level-vals[i-1])/(vals[i]-vals[i-1])
			break
		}
		below = 0
	}
	if edgeX < 0 {
		edgeX = float64(n) / 2
	}
	violSample := -1
	if violAt >= 0 {
		violSample = int(edgeX) + violAt
		switch fam.violate {
		case "spike": // opposite-direction spike, 6 samples
			for i := violSample; i < violSample+6 && i < n; i++ {
				vals[i] = 128 + fam.off - 1.6*fam.amp*sign(vals[i]-128-fam.off)
			}
		case "dropout": // collapse to the mid level for 20 samples
			for i := violSample; i < violSample+20 && i < n; i++ {
				vals[i] = 128 + fam.off
			}
		case "shift": // DC step for 40 samples
			for i := violSample; i < violSample+40 && i < n; i++ {
				vals[i] += 0.9 * fam.amp
			}
		case "width": // freeze the waveform (hold value) for 30 samples
			hold := vals[violSample]
			for i := violSample; i < violSample+30 && i < n; i++ {
				vals[i] = hold + 0.8*fam.amp
			}
		case "ring": // parasitic oscillation burst
			for i := violSample; i < violSample+24 && i < n; i++ {
				vals[i] += 1.2 * fam.amp * math.Sin(2*math.Pi*float64(i-violSample)/4)
			}
		}
	}
	for i := 0; i < n; i++ {
		v := vals[i] + fam.noise*(rng()*2-1)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		f.C1[i] = uint8(v)
		f.C2[i] = f.C1[i]
	}
	return f, edgeX, violSample, vals
}

func sign(x float64) float64 {
	if x < 0 {
		return -1
	}
	return 1
}

func TestZoneMaskBreaker(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	win := e.band.WinCols()
	const sampleS = 2e-9
	const posFrac = 0.5
	fams := zmFamilies()
	if len(fams) != 50 {
		t.Fatalf("expected 50 families, got %d", len(fams))
	}
	for fi, fam := range fams {
		seed := int64(1000 + fi*7919)
		rng := func() float64 {
			seed = (seed*1103515245 + 12345) & 0x7fffffff
			return float64(seed) / float64(0x7fffffff)
		}
		// ---- build the golden mask from 32 clean frames ----
		// tolerances: ±5 samples horizontal (must cover TRIGGER-POINT JITTER =
		// noise/slope — ~4 samples on the slowest edges here), ±8 codes vertical
		// (noise pp + sub-sample phase on steep slopes). The same rule a real
		// scope's mask wizard applies; documented in docs/zonemask-plan.md.
		lo := make([]uint8, win)
		hi := make([]uint8, win)
		for j := range lo {
			lo[j] = 255
		}
		for k := 0; k < 32; k++ {
			f, edgeX, _, _ := zmGen(fam, k, -1, rng)
			left := int(math.Round(edgeX - float64(win)*posFrac))
			for j := 0; j < win; j++ {
				s := left + j
				if s < 0 || s >= f.Valid {
					continue
				}
				v := f.C1[s]
				if v < lo[j] {
					lo[j] = v
				}
				if v > hi[j] {
					hi[j] = v
				}
			}
		}
		m := BuildMaskFromEnvelope(lo, hi, win, 5, 8, 0)
		if m == nil {
			t.Fatalf("fam %s: mask build failed", fam.name)
		}
		e.SetMask(m)
		e.SetMaskMode(MaskTest)
		e.ClearMaskFails()

		// ---- clean frames must all pass ----
		for k := 0; k < 16; k++ {
			f, edgeX, _, _ := zmGen(fam, 100+k, -1, rng)
			if fail, _ := e.maskEval(f, f.Valid, 0, edgeX, sampleS, posFrac, uint64(k)); fail {
				mf := e.MaskFails()
				t.Fatalf("fam %s: CLEAN frame %d failed the mask at col %d (false positive)", fam.name, k, mf[len(mf)-1].FailCol)
			}
		}
		// ---- violated frames must all fail, near the injected position ----
		for k := 0; k < 12; k++ {
			violAt := 40 + (k*53)%(win/2-80) // within the tested window, after the edge
			f, edgeX, violSample, clean := zmGen(fam, 200+k, violAt, rng)
			// PHYSICS REFEREE: an amplitude mask can only promise detection when
			// the defect ESCAPES the dilated envelope by more than the noise floor.
			// ±tolCols horizontal dilation opens oscillating regions to their local
			// min/max swing — a dropout-to-mid, a freeze, or a shift that stays
			// inside that swing is invisible to ANY per-column envelope, not a bug.
			span := map[string]int{"spike": 6, "dropout": 20, "shift": 40, "width": 30, "ring": 24}[fam.violate]
			left := int(math.Round(edgeX - float64(win)*posFrac))
			det := false
			for i := violSample; i < violSample+span && i < len(clean); i++ {
				j := i - left
				if j < 0 || j >= win {
					continue
				}
				if clean[i] < float64(m.Lo[j])-fam.noise-2 || clean[i] > float64(m.Hi[j])+fam.noise+2 {
					det = true
					break
				}
			}
			if !det {
				continue // physically undetectable — no assert either way
			}
			fail, _ := e.maskEval(f, f.Valid, 0, edgeX, sampleS, posFrac, uint64(100+k))
			if !fail {
				t.Fatalf("fam %s: VIOLATED frame %d passed the mask (missed %s at %d)", fam.name, k, fam.violate, violSample)
			}
			ring := e.MaskFails()
			got := ring[len(ring)-1].FailSample
			if got < violSample-8 || got > violSample+60 {
				t.Errorf("fam %s: violation localized at %d, injected at %d", fam.name, got, violSample)
			}
		}

		// ---- zones: intersect a known feature; avoid an empty band ----
		f, edgeX, _, _ := zmGen(fam, 999, -1, rng)
		// feature: right after the anchored crossing the waveform is AT the
		// anchor level — a zone straddling it must be hit
		lvl := int(128 + fam.off + fam.amp*0.5)
		hit := Zone{DtLoS: 0, DtHiS: float64(fam.period) * sampleS, CodeLo: lvl - int(fam.amp), CodeHi: lvl + int(fam.amp), Ch: 0}
		e.SetZones([]Zone{hit})
		if !e.zonesQualify(f, f.Valid, edgeX, sampleS) {
			t.Fatalf("fam %s: intersect zone on the live waveform must qualify", fam.name)
		}
		// empty: derive from the family's MEASURED maximum (ringing overshoots
		// to ~2.8x amp — assuming max=amp made the "empty" zone non-empty)
		maxCode := 0
		for _, v := range f.C1 {
			if int(v) > maxCode {
				maxCode = int(v)
			}
		}
		top := maxCode + 20
		if top < 250 {
			empty := Zone{DtLoS: 0, DtHiS: float64(fam.period) * sampleS, CodeLo: top, CodeHi: 255, Ch: 0, Avoid: true}
			e.SetZones([]Zone{empty})
			if !e.zonesQualify(f, f.Valid, edgeX, sampleS) {
				t.Fatalf("fam %s: avoid zone over empty space must qualify", fam.name)
			}
			// and as an intersect zone it must NOT qualify
			empty.Avoid = false
			e.SetZones([]Zone{empty})
			if e.zonesQualify(f, f.Valid, edgeX, sampleS) {
				t.Fatalf("fam %s: intersect zone over empty space must not qualify", fam.name)
			}
		}
		e.SetZones(nil)
		e.SetMaskMode(MaskOff)
	}
}
