// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// fuzzSignal builds a stretch of volts from the fuzzer's bytes: a tone,
// silence, DC, a clipped square or noise, laced with the hostile values a
// broken scale brings (NaN, infinities, huge and tiny numbers).
// TRLC-LINKS: REQ-SDS-266
func fuzzSignal(kind uint8, n int, f0 float64, raw []byte) []float64 {
	x := make([]float64, n)
	for i := range x {
		t := float64(i) / fsTest
		switch kind % 6 {
		case 0:
			x[i] = math.Sin(2 * math.Pi * f0 * t)
		case 1: // silence
		case 2:
			x[i] = 3.3
		case 3:
			x[i] = math.Max(-1, math.Min(1, 4*math.Sin(2*math.Pi*f0*t)))
		case 4:
			x[i] = (1 + 0.5*math.Sin(2*math.Pi*5*t)) * math.Sin(2*math.Pi*f0*t+3*math.Sin(2*math.Pi*6*t))
		case 5:
			if len(raw) > 0 {
				x[i] = float64(int8(raw[i%len(raw)])) / 64
			}
		}
	}
	// Hostile values at places the bytes choose.
	hostile := []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e300, -1e300, 5e-324, 0}
	for i := 0; i+3 <= len(raw) && i < 48; i += 3 {
		at := int(binary.LittleEndian.Uint16(raw[i:])) % max(1, n)
		if n > 0 {
			x[at] = hostile[int(raw[i+2])%len(hostile)]
		}
	}
	return x
}

// FuzzSoundAnalysis runs every SOUND analysis on hostile stretches and
// settings: none may panic or hang.
// TRLC-LINKS: REQ-SDS-266, REQ-SDS-267, REQ-SDS-268, REQ-SDS-269, REQ-SDS-270, REQ-SDS-271, REQ-SDS-272
func FuzzSoundAnalysis(f *testing.F) {
	for k := uint8(0); k < 6; k++ {
		f.Add(k, uint16(4096), 440.0, fsTest, 3.0, []byte{})
		f.Add(k, uint16(20000), 82.4, fsTest, 1000.0, []byte{1, 2, 0, 9, 9, 3, 200, 7, 5})
	}
	f.Add(uint8(0), uint16(64), math.NaN(), math.Inf(1), math.NaN(), []byte{0, 0, 0})
	f.Add(uint8(4), uint16(65535), 1e-300, 1e300, -1.0, []byte{255, 255, 6})
	f.Fuzz(func(t *testing.T, kind uint8, n uint16, f0, fsIn, knob float64, raw []byte) {
		// The rate is the stream's (any decimation) or an envelope's: never
		// a user's number.
		fs := RateHz(9 + int(kind)%8)
		if fsIn >= 1 && fsIn <= 1e9 {
			fs = fsIn
		}
		x := fuzzSignal(kind, int(n), f0, raw)
		y := fuzzSignal(kind+1, int(n), f0*1.5, raw)
		t0 := time.Now()
		_ = Measure(x)
		_ = DB(knob, f0)
		q := make([]uint16, len(raw))
		for i, v := range raw {
			q[i] = uint16(v) << 8
		}
		_ = ClipCodes(q)
		_ = Volts(q, knob, f0, nil)
		for _, r := range []string{"", "bass", "high"} {
			o := PitchRange(r)
			_ = DetectPitch(x, fs, o)
		}
		p := DetectPitch(x, fs, PitchOpts{Fmin: f0, Fmax: knob, Threshold: knob})
		_ = NoteOf(p.Hz, knob)
		_ = NoteOf(f0, knob)
		_ = Cents(f0, knob)
		_ = TunerWindow(f0, fs)
		env, efs := Envelope(x, fs, knob, 1/math.Max(1e-3, math.Abs(knob)))
		_ = AnalyzeADSR(env, efs, knob, f0)
		bm, bfs := BlockMeans(x, fs, knob)
		_ = AnalyzeADSR(bm, bfs, -1, -1)
		d := Demodulate(x, fs, f0, knob)
		_ = AnalyzeModulation(d)
		_, _ = ModRate(x, fs)
		_ = ShapeOf(x, fs, knob)
		_ = AnalyzeLFO(x, fs)
		sp := AudioSpectrum(x, fs, int(n))
		_ = AnalyzeSidebands(sp.DBV, sp.BinHz)
		_ = Harmonics(sp, f0)
		_ = FindHum(sp)
		_ = FloorDBV(sp.BinHz, knob)
		_ = LogX(f0, knob, fs)
		if g, _ := NoteGrid(f0, knob, 440); len(g) > 2000 {
			t.Fatalf("note grid of %d lines", len(g))
		}
		_ = BesselJ(int(kind%40), knob)
		_ = RatioText(knob)
		_ = CompareAB(x, y, fs, f0)
		_, _ = Delay(x, y, fs, knob)
		w, h := 1+int(kind%64), 1+int(n%64)
		if tr := Transfer(x, y, w, h, -knob, knob, -f0, f0); len(tr) != w*h {
			t.Fatalf("transfer of %d cells, want %d", len(tr), w*h)
		}
		at, _ := PlaceMark(x, int(knob), f0, kind&1 == 0)
		_ = ExtractCycles(x, fs, at, fs/math.Max(f0, 1e-9), int(kind%5), 600)
		_ = ExtractCycles(x, fs, knob, f0, 2, 600)
		var tu Tuner
		var va VOctAuto
		var dr Drift
		for i := 0; i < 20; i++ {
			hz := tu.Add(Pitch{Hz: f0 * (1 + float64(i%3)/100), OK: i%7 != 0})
			_, _ = va.Add(float64(i)*0.1, knob, hz)
			dr.Add(float64(i)*0.3, hz)
		}
		_ = FitVOct([]VOctPoint{{V: knob, Hz: f0}, {V: knob + 1, Hz: 2 * f0}, {V: f0, Hz: knob}}, knob)
		if el := time.Since(t0); el > 10*time.Second {
			t.Fatalf("analysis took %v on %d samples", el, n)
		}
	})
}
