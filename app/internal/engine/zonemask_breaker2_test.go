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

func TestZoneMaskPublishPolicy(t *testing.T) {
	fb := newFakeBus()
	// Square wave, period 256, plus a controllable feature pulse: code 250
	// over samples 1400..1450 when the flag is up. The flag is read under
	// fb.mu (DrainRead holds it), so flip it only between oneFrame calls.
	feature := false
	fb.wave = func(i int) (uint8, uint8) {
		var c1 uint8 = 56
		if (i/128)%2 == 0 {
			c1 = 200
		}
		if feature && i >= 1400 && i < 1450 {
			c1 = 250
		}
		return c1, 255 - c1
	}
	e, _ := newTestEngine(t, fb)
	e.bringUp()

	// learn the lock geometry from a plain publish
	e.oneFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.EdgeX < 0 {
		t.Fatalf("no locked baseline frame (fresh=%v)", fresh)
	}
	edgeX, sampleS := f.EdgeX, f.SampleS
	dtLo := (1405 - edgeX) * sampleS
	dtHi := (1445 - edgeX) * sampleS
	e.SetZones([]Zone{{DtLoS: dtLo, DtHiS: dtHi, CodeLo: 230, CodeHi: 255}})
	e.SetZoneMode(ZoneTrigger)

	// NORM: exactly the feature frames publish.
	for k := 0; k < 20; k++ {
		feature = k%3 == 0
		e.oneFrame(true)
		_, fresh := e.Consume()
		if fresh != feature {
			t.Fatalf("NORM frame %d: published=%v feature=%v", k, fresh, feature)
		}
	}

	// AUTO liveness: feature permanently off -> exactly one unqualified
	// publish per zoneFallback holds.
	feature = false
	e.zoneHeld = 0
	pubs := 0
	for k := 0; k < 2*zoneFallback; k++ {
		e.oneFrame(false)
		if _, fresh := e.Consume(); fresh {
			pubs++
		}
	}
	if pubs != 2 {
		t.Fatalf("AUTO liveness: %d publishes over %d holds, want 2", pubs, 2*zoneFallback)
	}

	// Mask counts every locked frame even while the zone holds, and ring
	// entries carry DISTINCT capture identities.
	win := e.band.WinCols()
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range lo {
		lo[j], hi[j] = 0, 255
	}
	// a tight band that the square wave violates immediately
	for j := 0; j < win; j++ {
		hi[j] = 100
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
	e.SetMaskMode(MaskTest)
	before := e.maskFail.Load()
	for k := 0; k < 6; k++ {
		e.oneFrame(false) // zone still holds (feature off)
	}
	if got := e.maskFail.Load() - before; got != 6 {
		t.Fatalf("mask must test held frames: %d fails over 6 held frames", got)
	}
	ring := e.MaskFails()
	seen := map[uint64]bool{}
	for _, r := range ring {
		if seen[r.Seq] {
			t.Fatalf("ring entries share a capture identity (seq %d twice)", r.Seq)
		}
		seen[r.Seq] = true
	}

	// Stop-on-fail publishes the failing frame even though the zone holds.
	e.SetMaskMode(MaskStopFail)
	e.oneFrame(false)
	if _, fresh := e.Consume(); !fresh {
		t.Fatal("stop-on-fail must force-publish past the zone hold")
	}
	if !e.maskStopped.Load() || e.running.Load() {
		t.Fatal("stop-on-fail must latch and stop acquisition")
	}
}

// ---------- Campaign C: mask window-geometry fuzz ----------

func TestMaskGeometryFuzz(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	win := e.band.WinCols()
	const n = 6016
	const sampleS = 2e-9

	// golden: flat 100 +/- tolerance band
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range lo {
		lo[j], hi[j] = 90, 110
	}

	seed := int64(424242)
	rng := func() float64 {
		seed = (seed*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
		return float64(seed%1_000_000) / 1_000_000
	}

	cases := 0
	for _, posFrac := range []float64{0.1, 0.5, 0.9} {
		for _, edgeX := range []float64{40.7, 900.2, 3000.5, float64(n) - 60.3} {
			for _, liveDepth := range []int{0, int(edgeX) + 150, n} {
				for k := 0; k < 12; k++ {
					// flat frame with one spike at a random raw position
					f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n}
					for i := 0; i < n; i++ {
						f.C1[i] = 100
						f.C2[i] = 100
					}
					spike := int(rng() * float64(n))
					f.C1[spike] = 220

					left := int(math.Round(edgeX - float64(win)*posFrac))
					testableEnd := n
					if liveDepth > 0 && liveDepth < n {
						testableEnd = liveDepth
					}
					col := spike - left
					wantFail := col >= 0 && col < win && spike >= 0 && spike < testableEnd

					e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
					e.SetMaskMode(MaskTest)
					e.ClearMaskFails()
					fail, _ := e.maskEval(f, n, liveDepth, edgeX, sampleS, posFrac)
					cases++
					if fail != wantFail {
						t.Fatalf("posFrac=%.1f edgeX=%.1f live=%d spike=%d (col %d): fail=%v want %v",
							posFrac, edgeX, liveDepth, spike, col, fail, wantFail)
					}
					if fail {
						r := e.MaskFails()
						mf := r[len(r)-1]
						if mf.FailSample != left+mf.FailCol {
							t.Fatalf("geometry: FailSample %d != left %d + FailCol %d", mf.FailSample, left, mf.FailCol)
						}
						if mf.FailSample != spike {
							t.Fatalf("localization: FailSample %d, spike %d", mf.FailSample, spike)
						}
					}
				}
			}
		}
	}
	t.Logf("mask geometry fuzz: %d cases", cases)
}

// ---------- root-cause regressions found by this campaign ----------

// Env/roll frames have no edge anchor: the zone trigger and mask cannot run.
// They must still publish (holding would blank slow timebases forever), but
// the bypass is COUNTED — a silently bypassed qualifier wears a clean run's
// signature.
func TestZoneMaskEnvRollCounted(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	b, ok := PlanTdiv(5e-3) // KindEnvelope
	if !ok || b.Kind() != KindEnvelope {
		t.Fatalf("5ms/div did not plan an envelope band (kind %v)", b.Kind())
	}
	e.band = b
	e.transition(false, false)
	e.SetZoneMode(ZoneTrigger)
	e.SetZones([]Zone{{DtLoS: 0, DtHiS: 1e-6, CodeLo: 0, CodeHi: 255}})
	win := e.band.WinCols()
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range hi {
		hi[j] = 255
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
	e.SetMaskMode(MaskTest)

	e.envFrame(false)
	if _, fresh := e.Consume(); !fresh {
		t.Fatal("env frame must still publish under an armed zone trigger")
	}
	if e.zoneSkip.Load() != 1 {
		t.Fatalf("zoneSkip = %d, want 1", e.zoneSkip.Load())
	}
	if e.maskSkip.Load() != 1 {
		t.Fatalf("maskSkip = %d, want 1", e.maskSkip.Load())
	}
	if e.maskPass.Load() != 0 || e.maskFail.Load() != 0 {
		t.Fatal("an env frame must not count as a mask verdict")
	}

	// roll path counts the same way
	b, ok = PlanTdiv(100e-3)
	if !ok || b.Kind() != KindRoll {
		t.Fatalf("100ms/div did not plan a roll band")
	}
	e.band = b
	e.transition(false, false)
	e.rollUpdate(false)
	if e.zoneSkip.Load() < 2 || e.maskSkip.Load() < 2 {
		t.Fatalf("roll publish not counted: zoneSkip=%d maskSkip=%d", e.zoneSkip.Load(), e.maskSkip.Load())
	}
}

// A frame whose whole window falls off the record / into the dead tail has
// ZERO testable columns — that is a skip, not a pass (found by the geometry
// fuzz review: a zero-column "pass" wears a clean run's signature).
func TestMaskZeroTestableColumnsSkips(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	win := e.band.WinCols()
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range hi {
		hi[j] = 255
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
	e.SetMaskMode(MaskTest)
	const n = 6016
	f := zmFrame(n, 100, 0, 0)
	// liveDepth far left of the window: every column is dead
	edgeX := float64(n) - 10
	skip0 := e.maskSkip.Load()
	fail, _ := e.maskEval(f, n, 100, edgeX, 2e-9, 0.5)
	if fail {
		t.Fatal("zero-testable-column frame must not fail")
	}
	if e.maskPass.Load() != 0 {
		t.Fatal("zero-testable-column frame must not count as a pass")
	}
	if e.maskSkip.Load() != skip0+1 {
		t.Fatalf("zero-testable-column frame must count a skip (got %d)", e.maskSkip.Load()-skip0)
	}
}

// NaN trigger position would silently un-map every mask column (left = NaN
// -> all columns "off-record" -> eternal skips at best).
func TestTrigPosFracNaNGuard(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	e.SetTrigPosFrac(math.NaN())
	if got := e.Snapshot().TrigPosFrac; got != 0.5 {
		t.Fatalf("NaN posFrac stored as %v, want the 0.5 default", got)
	}
}

// SINGLE + zone trigger compose: the latch must wait for a QUALIFYING frame,
// not the first triggered one — that is the flagship "arm single, catch the
// anomaly" workflow.
func TestSingleShotWaitsForZoneQualify(t *testing.T) {
	fb := newFakeBus()
	// The feature must be PERIOD-RELATIVE: a SINGLE arms the full deep drain
	// (effDrainCols), which moves edgeX — an absolute-index feature would slide
	// out of the zone window. Put it at +32..56 samples after EVERY rising
	// edge of the square instead.
	feature := false
	fb.wave = func(i int) (uint8, uint8) {
		var c1 uint8 = 56
		if (i/128)%2 == 0 {
			c1 = 200
		}
		if feature && i%256 >= 32 && i%256 < 56 {
			c1 = 250
		}
		return c1, 255 - c1
	}
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.oneFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.EdgeX < 0 {
		t.Fatal("no baseline lock")
	}
	e.SetZones([]Zone{{DtLoS: 36 * f.SampleS, DtHiS: 52 * f.SampleS, CodeLo: 230, CodeHi: 255}})
	e.SetZoneMode(ZoneTrigger)
	e.SetSingle()
	for k := 0; k < 8; k++ { // unqualified frames must not satisfy the single
		e.oneFrame(true)
		if !e.singleArmed.Load() {
			t.Fatalf("single latched on an unqualified frame (k=%d)", k)
		}
		if _, fresh := e.Consume(); fresh {
			t.Fatalf("unqualified frame published under single+zone (k=%d)", k)
		}
	}
	feature = true
	e.oneFrame(true)
	if _, fresh := e.Consume(); !fresh {
		t.Fatal("qualifying frame did not publish")
	}
	if e.singleArmed.Load() || e.running.Load() {
		t.Fatal("single must latch and stop on the qualifying frame")
	}
}
