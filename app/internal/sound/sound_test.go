// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"math/rand"
	"testing"
)

// fsTest is the stream's rate.
var fsTest = RateHz(StreamLog2)

// gen builds n samples of fn(t) plus gaussian noise of sigma.
// TRLC-LINKS: REQ-SDS-266
func gen(n int, fs float64, fn func(t float64) float64, sigma float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	x := make([]float64, n)
	for i := range x {
		x[i] = fn(float64(i)/fs) + sigma*r.NormFloat64()
	}
	return x
}

// quantize rounds volts to the stream's Q8.8 codes at vpc volts a code and
// back, as the scope delivers them.
// TRLC-LINKS: REQ-SDS-266
func quantize(x []float64, vpc float64) []float64 {
	q := make([]uint16, len(x))
	for i, v := range x {
		c := math.Round(v/vpc*256) + 32768
		q[i] = uint16(math.Max(0, math.Min(65535, c)))
	}
	return Volts(q, vpc, 0, nil)
}

// sine is A sin(2 pi f t + ph).
// TRLC-LINKS: REQ-SDS-267
func sine(a, f, ph float64) func(float64) float64 {
	return func(t float64) float64 { return a * math.Sin(2*math.Pi*f*t+ph) }
}

// square is a band-limited square of amplitude a: odd harmonics to fs/2.
// TRLC-LINKS: REQ-SDS-267
func square(a, f, fs float64) func(float64) float64 {
	return func(t float64) float64 {
		s := 0.0
		for k := 1; float64(k)*f < fs/2.2; k += 2 {
			s += math.Sin(2*math.Pi*float64(k)*f*t) / float64(k)
		}
		return a * 4 / math.Pi * s
	}
}

// TestLevels reads a 1 V peak sine on 0.5 V DC.
// TRLC-LINKS: REQ-SDS-266
func TestLevels(t *testing.T) {
	x := gen(61035, fsTest, func(t float64) float64 { return 0.5 + math.Sin(2*math.Pi*1000*t) }, 0, 1)
	l := Measure(x)
	if math.Abs(l.RMS-1/math.Sqrt2) > 1e-3 || math.Abs(l.DC-0.5) > 1e-3 || math.Abs(l.Vpp-2) > 1e-3 || math.Abs(l.CrestDB-3.0103) > 0.01 {
		t.Fatalf("levels %+v", l)
	}
	if math.Abs(DB(DBuRefV, PresetByID("dbu").Vrms)) > 1e-9 || math.Abs(DB(1.2277, PresetByID("pro").Vrms)) > 0.01 {
		t.Fatal("dBu references")
	}
	if math.Abs(DB(5/math.Sqrt2, PresetByID("euro").Vrms)) > 1e-9 {
		t.Fatal("Eurorack 10 Vpp is 0 dB")
	}
	if Measure(nil).N != 0 || !math.IsInf(DB(0, 1), -1) {
		t.Fatal("empty")
	}
	q := []uint16{0, 0, 0, 30000, 30000}
	if !ClipCodes(q) || ClipCodes([]uint16{30000, 31000}) {
		t.Fatal("clip")
	}
}

// TestPitchSines reads crystal sines from 27.5 Hz to 4.2 kHz within half a
// cent through the stream's quantization and noise.
// TRLC-LINKS: REQ-SDS-267
func TestPitchSines(t *testing.T) {
	vpc := 0.04 // 1 V/div
	for i, f := range []float64{27.5, 41.2, 55, 110, 220.5, 440, 1000, 1760.3, 3520, 4186} {
		n := TunerWindow(f, fsTest)
		x := quantize(gen(n, fsTest, sine(1.5, f, 0.3*float64(i)), 0.01, int64(i)), vpc)
		p := DetectPitch(x, fsTest, DefaultPitchOpts)
		if !p.OK || math.Abs(Cents(p.Hz, f)) > 0.5 {
			t.Errorf("%g Hz: read %+v (%.3f cents)", f, p, Cents(p.Hz, f))
		}
	}
}

// TestPitchRich reads a square, a sawtooth and a weak-fundamental tone.
// TRLC-LINKS: REQ-SDS-267
func TestPitchRich(t *testing.T) {
	saw := func(f float64) func(float64) float64 {
		return func(t float64) float64 {
			s := 0.0
			for k := 1; float64(k)*f < fsTest/2.2; k++ {
				s += math.Sin(2*math.Pi*float64(k)*f*t) / float64(k)
			}
			return s
		}
	}
	weak := func(f float64) func(float64) float64 {
		return func(t float64) float64 {
			return 0.2*math.Sin(2*math.Pi*f*t) + math.Sin(2*math.Pi*2*f*t) + 0.8*math.Sin(2*math.Pi*3*f*t)
		}
	}
	for name, c := range map[string]struct {
		fn func(float64) float64
		f  float64
	}{"square 110": {square(1, 110, fsTest), 110}, "saw 82.4": {saw(82.41), 82.41}, "saw 1318": {saw(1318.5), 1318.5},
		"weak fundamental 196": {weak(196), 196}} {
		x := gen(TunerWindow(c.f, fsTest), fsTest, c.fn, 0.005, 3)
		p := DetectPitch(x, fsTest, DefaultPitchOpts)
		if !p.OK || math.Abs(Cents(p.Hz, c.f)) > 1 {
			t.Errorf("%s: %+v (%.2f cents)", name, p, Cents(p.Hz, c.f))
		}
	}
	// Noise is no pitch.
	if p := DetectPitch(gen(8192, fsTest, func(float64) float64 { return 0 }, 1, 9), fsTest, DefaultPitchOpts); p.OK {
		t.Errorf("noise read as %g Hz", p.Hz)
	}
	if p := DetectPitch(make([]float64, 8192), fsTest, DefaultPitchOpts); p.OK {
		t.Error("silence voiced")
	}
}

// TestNotes names frequencies.
// TRLC-LINKS: REQ-SDS-267
func TestNotes(t *testing.T) {
	cases := []struct {
		f, a4 float64
		name  string
		cents float64
	}{{440, 440, "A4", 0}, {261.6255653, 440, "C4", 0}, {445, 440, "A4", 19.56}, {432, 432, "A4", 0}, {27.5, 440, "A0", 0},
		{16.3516, 440, "C0", 0}, {8.1758, 440, "C-1", 0}, {466.1637615, 440, "A#4", 0}}
	for _, c := range cases {
		n := NoteOf(c.f, c.a4)
		if n.String() != c.name || math.Abs(n.Cents-c.cents) > 0.02 {
			t.Errorf("%g: %s %.2f", c.f, n, n.Cents)
		}
	}
	if NoteOf(0, 440).String() != "--" {
		t.Error("no pitch")
	}
}

// TestTunerSmoothing holds a median and resets on a jump.
// TRLC-LINKS: REQ-SDS-267
func TestTunerSmoothing(t *testing.T) {
	var tu Tuner
	tu.Add(Pitch{Hz: 440, OK: true})
	tu.Add(Pitch{Hz: 441, OK: true})
	if v := tu.Add(Pitch{Hz: 439.5, OK: true}); v != 440 {
		t.Fatalf("median %g", v)
	}
	if v := tu.Add(Pitch{Hz: 880, OK: true}); v != 880 {
		t.Fatalf("jump %g", v)
	}
	if v := tu.Add(Pitch{}); v != 0 {
		t.Fatal("unvoiced")
	}
}

// adsrSignal is a 440 Hz tone (or, cv, the envelope itself) shaped by a
// linear attack, exponential decay to the sustain and exponential release.
// TRLC-LINKS: REQ-SDS-268
func adsrSignal(a, d, s, hold, r float64, cv bool) func(float64) float64 {
	env := func(t float64) float64 {
		const t0 = 0.1
		switch {
		case t < t0:
			return 0
		case t < t0+a:
			return (t - t0) / a
		case t < t0+a+hold:
			return s + (1-s)*math.Exp(-(t-t0-a)/d)
		}
		top := s + (1-s)*math.Exp(-hold/d)
		return top * math.Exp(-(t-t0-a-hold)/r)
	}
	if cv {
		return func(t float64) float64 { return 5 * env(t) }
	}
	return func(t float64) float64 { return env(t) * math.Sin(2*math.Pi*440*t) }
}

// TestADSR reads attack, decay, sustain and release of a tone and of a CV,
// with the gate known and found.
// TRLC-LINKS: REQ-SDS-268
func TestADSR(t *testing.T) {
	const a, d, s, hold, r = 0.05, 0.04, 0.5, 0.6, 0.08
	n := int(1.4 * fsTest)
	gateOff := 0.1 + a + hold
	for _, c := range []struct {
		name string
		cv   bool
		gate bool
	}{{"tone gated", false, true}, {"tone found", false, false}, {"cv gated", true, true}, {"cv found", true, false}} {
		x := gen(n, fsTest, adsrSignal(a, d, s, hold, r, c.cv), 0.002, 5)
		var env []float64
		var efs float64
		if c.cv {
			env, efs = BlockMeans(x, fsTest, 0.001)
		} else {
			env, efs = Envelope(x, fsTest, 0.02, 0.001)
		}
		on, off := -1.0, -1.0
		if c.gate {
			on, off = 0.1, gateOff
		}
		res := AnalyzeADSR(env, efs, on, off)
		if !res.OK {
			t.Fatalf("%s: %s", c.name, res.Why)
		}
		// Linear attack: 10-90 % is 0.8 of it.
		if math.Abs(res.Attack1090-0.8*a) > 0.005 || math.Abs(res.Attack0100-a) > 0.006 {
			t.Errorf("%s: attack %.4f / %.4f", c.name, res.Attack1090, res.Attack0100)
		}
		if !res.HasSustain || math.Abs(res.SustainPct-50) > 3 {
			t.Errorf("%s: sustain %+v", c.name, res)
		}
		// 90 % of the way down: d ln 10.
		if math.Abs(res.Decay-d*math.Log(10)) > 0.012 || math.Abs(res.DecayTau-d) > 0.01 {
			t.Errorf("%s: decay %.4f tau %.4f", c.name, res.Decay, res.DecayTau)
		}
		if math.Abs(res.Release-r*math.Log(10)) > 0.02 || math.Abs(res.ReleaseTau-r) > 0.015 {
			t.Errorf("%s: release %.4f tau %.4f at %.3f", c.name, res.Release, res.ReleaseTau, res.ReleaseAt)
		}
	}
	// A percussive envelope: no sustain, the decay to a tenth.
	x := gen(n, fsTest, func(tt float64) float64 {
		if tt < 0.1 {
			return 0
		}
		return math.Exp(-(tt-0.1)/0.15) * math.Sin(2*math.Pi*300*tt)
	}, 0.001, 6)
	env, efs := Envelope(x, fsTest, 0.02, 0.001)
	res := AnalyzeADSR(env, efs, -1, -1)
	if !res.OK || res.HasSustain || math.Abs(res.Decay-0.15*math.Log(10)) > 0.02 {
		t.Errorf("percussive: %+v", res)
	}
	if r := AnalyzeADSR(make([]float64, 100), 1000, -1, -1); r.OK {
		t.Error("flat read")
	}
}

// TestTremoloVibrato reads AM and FM depth and rate.
// TRLC-LINKS: REQ-SDS-269
func TestTremoloVibrato(t *testing.T) {
	n := int(4 * fsTest)
	const fc = 440.0
	// Tremolo: 5 Hz, 30 % depth.
	x := gen(n, fsTest, func(t float64) float64 {
		return (1 + 0.3*math.Sin(2*math.Pi*5*t)) * math.Sin(2*math.Pi*fc*t)
	}, 0.005, 7)
	m := AnalyzeModulation(Demodulate(x, fsTest, fc, 1000))
	if !m.HasAM || math.Abs(m.AMPct-30) > 1 || math.Abs(m.AMRate-5) > 0.05 || m.AMShape != "sine" || m.HasFM {
		t.Errorf("tremolo: %+v", m)
	}
	// A square tremolo.
	x = gen(n, fsTest, func(t float64) float64 {
		g := 1.0
		if math.Sin(2*math.Pi*3*t) < 0 {
			g = 0.4
		}
		return g * math.Sin(2*math.Pi*fc*t)
	}, 0.005, 8)
	m = AnalyzeModulation(Demodulate(x, fsTest, fc, 1000))
	if math.Abs(m.AMPct-100*0.6/1.4) > 2 || math.Abs(m.AMRate-3) > 0.05 || m.AMShape != "square" {
		t.Errorf("square tremolo: %+v", m)
	}
	// Vibrato: +-20 cents at 6 Hz on a square (the harmonics must not leak).
	dev := fc * (math.Pow(2, 20.0/1200) - 1)
	x = gen(n, fsTest, func(t float64) float64 {
		ph := 2*math.Pi*fc*t - dev/6*math.Cos(2*math.Pi*6*t)
		s := 0.0
		for k := 1; k < 30; k += 2 {
			s += math.Sin(float64(k)*ph) / float64(k)
		}
		return s
	}, 0.005, 9)
	m = AnalyzeModulation(Demodulate(x, fsTest, fc, 1000))
	if !m.HasFM || math.Abs(m.FMCents-20) > 1 || math.Abs(m.FMRate-6) > 0.05 || m.FMShape != "sine" || m.HasAM {
		t.Errorf("vibrato: %+v", m)
	}
	// Wow: 0.1 % peak at 1 Hz on 3150 Hz reads 0.0707 % RMS in the wow band.
	x = gen(n*2, fsTest, func(t float64) float64 {
		return math.Sin(2*math.Pi*3150*t - 0.001*3150/1*math.Cos(2*math.Pi*1*t))
	}, 0.001, 10)
	m = AnalyzeModulation(Demodulate(x, fsTest, 3150, 1000))
	if math.Abs(m.WowPct-0.0707) > 0.008 || m.FlutterPct > 0.01 {
		t.Errorf("wow: %+v", m)
	}
}

// TestSidebands reads the index and ratio of audio-rate FM.
// TRLC-LINKS: REQ-SDS-269
func TestSidebands(t *testing.T) {
	for _, c := range []struct{ fc, fm, beta float64 }{{3000, 600, 2}, {2000, 300, 1.5}, {4000, 1000, 0.6}, {5000, 500, 3.2}} {
		x := gen(32768, fsTest, func(t float64) float64 {
			return math.Sin(2*math.Pi*c.fc*t + c.beta*math.Sin(2*math.Pi*c.fm*t))
		}, 0.0005, 11)
		sp := AudioSpectrum(x, fsTest, 32768)
		sb := AnalyzeSidebands(sp.DBV, sp.BinHz)
		if !sb.OK || math.Abs(sb.Beta-c.beta) > 0.1 || math.Abs(sb.Fm-c.fm) > 2 || math.Abs(sb.Fc-c.fc) > 2 {
			t.Errorf("%+v: %+v", c, sb)
		}
	}
	// A flat noise spectrum has thousands of lines within 40 dB: the search
	// weighs only the strongest (it once took seconds a call on the ARM).
	rng := rand.New(rand.NewSource(5))
	noise := make([]float64, 16384)
	for i := range noise {
		noise[i] = -60 + 6*rng.Float64()
	}
	if n := len(strongestLines(findLines(noise, 40), sbMaxLines)); n != sbMaxLines {
		t.Errorf("noise kept %d lines, want %d", n, sbMaxLines)
	}
	_ = AnalyzeSidebands(noise, 1)
	if RatioText(5) != "5:1" || RatioText(1.5) != "3:2" || RatioText(20.0/3) != "20:3" || RatioText(math.Sqrt2) != "1.41" {
		t.Errorf("ratios %s %s %s %s", RatioText(5), RatioText(1.5), RatioText(20.0/3), RatioText(math.Sqrt2))
	}
	j := BesselJ(3, 2.404825557695773)
	if math.Abs(j[0]) > 1e-9 || math.Abs(j[1]-0.519147497289) > 1e-9 {
		t.Errorf("bessel %v", j)
	}
}

// TestLFO reads a triangle, a ramp and a square LFO.
// TRLC-LINKS: REQ-SDS-269
func TestLFO(t *testing.T) {
	n := int(5 * fsTest)
	tri := gen(n, fsTest, func(t float64) float64 { return 2 * (2*math.Abs(2*(t*2-math.Floor(t*2+0.5))) - 1) }, 0.002, 12)
	l := AnalyzeLFO(tri, fsTest)
	if !l.OK || math.Abs(l.RateHz-2) > 0.01 || math.Abs(l.Vpp-4) > 0.05 || l.Shape != "triangle" || math.Abs(l.RisePct-50) > 3 {
		t.Errorf("triangle: %+v", l)
	}
	ramp := gen(n, fsTest, func(t float64) float64 { return 3 * (t*1.3 - math.Floor(t*1.3)) }, 0.002, 13)
	l = AnalyzeLFO(ramp, fsTest)
	if !l.OK || math.Abs(l.RateHz-1.3) > 0.01 || l.Shape != "saw" || l.RisePct < 95 {
		t.Errorf("ramp: %+v", l)
	}
	sq := gen(n, fsTest, func(t float64) float64 {
		if math.Sin(2*math.Pi*0.7*t) >= 0 {
			return 1
		}
		return -1
	}, 0.002, 14)
	l = AnalyzeLFO(sq, fsTest)
	if !l.OK || math.Abs(l.RateHz-0.7) > 0.01 || l.Shape != "square" {
		t.Errorf("square: %+v", l)
	}
}

// TestHarmonics reads a tone with known harmonics, the hum and THD.
// TRLC-LINKS: REQ-SDS-270
func TestHarmonics(t *testing.T) {
	a := math.Sqrt2 // 1 V RMS
	x := gen(65536, fsTest, func(t float64) float64 {
		return a*math.Sin(2*math.Pi*1000*t) + a*0.01*math.Sin(2*math.Pi*2000*t) + a*0.0316228*math.Sin(2*math.Pi*3000*t+1) +
			a*0.001*math.Sin(2*math.Pi*50*t) + a*0.0005*math.Sin(2*math.Pi*150*t)
	}, 1e-5, 15)
	sp := AudioSpectrum(x, fsTest, 32768)
	h := Harmonics(sp, 0)
	if !h.OK || math.Abs(h.H[0].DBV) > 0.05 || math.Abs(h.F0-1000) > 0.05 {
		t.Fatalf("H1 %+v", h.H[0])
	}
	if math.Abs(h.H[1].RelDB+40) > 0.2 || math.Abs(h.H[2].RelDB+30) > 0.2 || h.H[3].Found {
		t.Errorf("H2 %+v H3 %+v H4 %+v", h.H[1], h.H[2], h.H[3])
	}
	want := 100 * math.Sqrt(1e-4+1e-3)
	if math.Abs(h.THDPct-want) > 0.05 || math.Abs(h.EvenOdd+10) > 0.3 || h.Character != "odd (symmetric)" {
		t.Errorf("THD %.3f even/odd %.2f %s", h.THDPct, h.EvenOdd, h.Character)
	}
	if !h.Hum.Found || h.Hum.MainHz != 50 || math.Abs(h.Hum.DBV+60) > 0.3 {
		t.Errorf("hum %+v", h.Hum)
	}
	if f := FloorDBV(sp.BinHz, 0.04); !(f < -95 && f > -120) {
		t.Errorf("floor %g", f)
	}
	fs, ns := NoteGrid(20, 20000, 440)
	if len(fs) != 10 || ns[0] != "C1" || math.Abs(fs[0]-32.7032) > 0.001 {
		t.Errorf("grid %v %v", fs, ns)
	}
}

// TestAB compares a circuit's input and output: gain, phase, delay, added
// distortion.
// TRLC-LINKS: REQ-SDS-271
func TestAB(t *testing.T) {
	n := 32768
	in := gen(n, fsTest, sine(1, 1000, 0), 0.001, 16)
	out := gen(n, fsTest, func(t float64) float64 { return 0.5 * math.Sin(2*math.Pi*1000*(t-0.00025)) }, 0.001, 17)
	r := CompareAB(in, out, fsTest, 0)
	if !r.OK || math.Abs(r.GainDB+6.0206) > 0.05 || math.Abs(r.PhaseDeg+90) > 0.5 || math.Abs(r.RMSGainDB+6.02) > 0.1 {
		t.Errorf("AB %+v", r)
	}
	// Delay on a broadband signal.
	rnd := rand.New(rand.NewSource(18))
	a := make([]float64, n)
	for i := range a {
		a[i] = rnd.NormFloat64()
	}
	b := make([]float64, n)
	for i := 37; i < n; i++ {
		b[i] = 0.8 * a[i-37]
	}
	if d, ok := Delay(a, b, fsTest, 0.02); !ok || math.Abs(d*fsTest-37) > 0.05 {
		t.Errorf("delay %g samples", d*fsTest)
	}
	// A clipping stage adds odd harmonics.
	clip := make([]float64, n)
	for i, v := range in {
		clip[i] = math.Tanh(2 * v)
	}
	r = CompareAB(in, clip, fsTest, 1000)
	if !(r.THDB > 5) || !(r.AddedPct > 5) || math.Abs(r.PhaseDeg) > 1 {
		t.Errorf("clip: %+v", r)
	}
	h := Transfer([]float64{-1, 0, 1}, []float64{-1, 0, 1}, 4, 4, -1.01, 1.01, -1.01, 1.01)
	if h[3*4+0] != 1 || h[0*4+3] != 1 {
		t.Errorf("transfer %v", h)
	}
}

// TestVOct fits a VCO off by 1 % in scale and flat at the top.
// TRLC-LINKS: REQ-SDS-267
func TestVOct(t *testing.T) {
	var pts []VOctPoint
	for v := 0.0; v <= 5; v++ {
		hz := 65.406 * math.Pow(2, 1.01*v)
		if v == 5 {
			hz *= math.Pow(2, -15.0/1200) // the top tracks 15 cents flat
		}
		pts = append(pts, VOctPoint{V: v, Hz: hz})
	}
	f := FitVOct(pts, 440)
	if !f.OK || len(f.Rows) != 6 || f.Rows[0].Note != "C2" {
		t.Fatalf("fit %+v", f)
	}
	if math.Abs(f.Rows[1].ErrCents-12) > 0.01 || math.Abs(f.HFDevCents+15) > 0.5 {
		t.Errorf("err %.3f hf %.3f", f.Rows[1].ErrCents, f.HFDevCents)
	}
	if !(f.ScaleCents > 8 && f.ScaleCents < 12) || !(f.TrimPct < -0.6 && f.TrimPct > -1) {
		t.Errorf("scale %.3f trim %.3f", f.ScaleCents, f.TrimPct)
	}
	// Auto: a CV stepped every second with a stable pitch takes a point each.
	var au VOctAuto
	got := 0
	for i := 0; i < 400; i++ {
		tt := float64(i) * 0.01
		v := math.Floor(tt)
		if _, ok := au.Add(tt, v+0.0005*math.Sin(float64(i)), 100*math.Pow(2, v)); ok {
			got++
		}
	}
	if got != 4 {
		t.Errorf("auto took %d points", got)
	}
	var dr Drift
	for i := 0; i < 1000; i++ {
		tt := float64(i) * 0.01
		dr.Add(tt, 440*math.Pow(2, tt/1200))
	}
	if len(dr.Points) != 9 || math.Abs(dr.Points[8].Cents-8) > 0.05 {
		t.Errorf("drift %+v", dr.Points)
	}
}

// TestCycle places a mark on its crossing and reads the cycle.
// TRLC-LINKS: REQ-SDS-272
func TestCycle(t *testing.T) {
	const f = 500.0
	x := gen(8192, fsTest, square(1, f, fsTest), 0, 0)
	per := fsTest / f
	// The square rises through zero at t = k/f.
	want := 3 * per
	at, ok := PlaceMark(x, int(want)+3, 0, true)
	if !ok || math.Abs(at-want) > 0.1 {
		t.Fatalf("placed %g want %g", at, want)
	}
	if _, ok := PlaceMark(x, int(want+per/2), 0, true); ok {
		t.Fatal("a mark with no rising crossing beside it was placed")
	}
	sn := gen(8192, fsTest, sine(1, f, 0), 0, 0)
	if at, ok := PlaceMark(sn, int(want)-4, 0, true); !ok || math.Abs(at-want) > 0.01 {
		t.Fatalf("sine placed %g want %g", at, want)
	}
	c := ExtractCycles(x, fsTest, at, per, 2, 400)
	if !c.OK || len(c.Trace) != 400 || math.Abs(c.DutyPct-50) > 2 || math.Abs(c.PeriodS-1/f) > 1e-9 || c.Vpp < 2 {
		t.Errorf("cycle %+v", c)
	}
	if math.Abs(c.Trace[0]) > 0.05 {
		t.Errorf("starts at %g, not the crossing", c.Trace[0])
	}
}

// TestBreakers feeds every reading degenerate and hostile input: no panic.
// TRLC-LINKS: REQ-SDS-266, REQ-SDS-267, REQ-SDS-268, REQ-SDS-269, REQ-SDS-270, REQ-SDS-271, REQ-SDS-272
func TestBreakers(t *testing.T) {
	r := rand.New(rand.NewSource(19))
	inputs := [][]float64{nil, {1}, make([]float64, 7), make([]float64, 5000)}
	for i := 0; i < 20; i++ {
		x := make([]float64, 1+r.Intn(70000))
		for j := range x {
			switch i % 4 {
			case 0:
				x[j] = r.NormFloat64()
			case 1:
				x[j] = float64(j % 2)
			case 2:
				x[j] = math.Sin(float64(j) * r.Float64())
			default:
				x[j] = 1e9 * float64(j%3)
			}
		}
		inputs = append(inputs, x)
	}
	for _, x := range inputs {
		Measure(x)
		DetectPitch(x, fsTest, DefaultPitchOpts)
		env, efs := Envelope(x, fsTest, 0.02, 0.001)
		AnalyzeADSR(env, efs, -1, -1)
		AnalyzeADSR(env, efs, 0.01, 0.02)
		AnalyzeModulation(Demodulate(x, fsTest, 440, 1000))
		AnalyzeLFO(x, fsTest)
		sp := AudioSpectrum(x, fsTest, 16384)
		Harmonics(sp, 0)
		AnalyzeSidebands(sp.DBV, sp.BinHz)
		CompareAB(x, x, fsTest, 0)
		PlaceMark(x, len(x)/2, 0, true)
		ExtractCycles(x, fsTest, 1, 50, 2, 100)
	}
}
