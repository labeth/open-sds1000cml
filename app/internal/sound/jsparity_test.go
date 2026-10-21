// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"open-sds/app/internal/testenv"
)

// pvec is one parity vector: what to run on which samples.
type pvec struct {
	Kind   string      `json:"kind"`
	Name   string      `json:"name"`
	X      []float64   `json:"x,omitempty"`
	Y      []float64   `json:"y,omitempty"`
	Fs     float64     `json:"fs"`
	Range  string      `json:"range,omitempty"`
	F      float64     `json:"f,omitempty"`
	A4     float64     `json:"a4,omitempty"`
	CV     bool        `json:"cv,omitempty"`
	On     float64     `json:"on"`
	Off    float64     `json:"off"`
	Fc     float64     `json:"fc,omitempty"`
	N      int         `json:"n,omitempty"`
	F0     float64     `json:"f0"`
	Pts    []VOctPoint `json:"pts,omitempty"`
	Mark   int         `json:"mark,omitempty"`
	Period float64     `json:"period,omitempty"`
}

// num writes a float as the JS side's JSON does (non-finite as strings).
// TRLC-LINKS: REQ-SDS-266
func num(v float64) any {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	}
	return v
}

// nums maps num over a slice.
// TRLC-LINKS: REQ-SDS-266
func nums(x []float64) []any {
	out := make([]any, len(x))
	for i, v := range x {
		out[i] = num(v)
	}
	return out
}

// goReading is the Go side of a vector in the JS result's shape.
// TRLC-LINKS: REQ-SDS-266, REQ-SDS-267, REQ-SDS-268, REQ-SDS-269, REQ-SDS-270, REQ-SDS-271, REQ-SDS-272
func goReading(v pvec) any {
	switch v.Kind {
	case "measure":
		l := Measure(v.X)
		return map[string]any{"n": float64(l.N), "min": num(l.Min), "max": num(l.Max), "vpp": num(l.Vpp), "dc": num(l.DC),
			"rms": num(l.RMS), "total": num(l.Total), "peak": num(l.Peak), "crestDB": num(l.CrestDB)}
	case "pitch":
		p := DetectPitch(v.X, v.Fs, PitchRange(v.Range))
		return map[string]any{"hz": num(p.Hz), "clarity": num(p.Clarity), "ok": p.OK}
	case "window":
		return map[string]any{"n": float64(TunerWindow(v.F, v.Fs))}
	case "note":
		n := NoteOf(v.F, v.A4)
		return map[string]any{"name": n.Name, "octave": float64(n.Octave), "midi": float64(n.Midi), "cents": num(n.Cents), "text": n.String()}
	case "adsr":
		var env []float64
		var efs float64
		if v.CV {
			env, efs = BlockMeans(v.X, v.Fs, 0.001)
		} else {
			env, efs = Envelope(v.X, v.Fs, 0.02, 0.001)
		}
		r := AnalyzeADSR(env, efs, v.On, v.Off)
		return map[string]any{"envN": float64(len(env)), "envFs": num(efs), "adsr": map[string]any{"ok": r.OK, "why": r.Why,
			"start": num(r.Start), "peakAt": num(r.PeakAt), "decayEnd": num(r.DecayEnd), "releaseAt": num(r.ReleaseAt), "end": num(r.End),
			"floor": num(r.Floor), "peak": num(r.Peak), "attack1090": num(r.Attack1090), "attack0100": num(r.Attack0100),
			"decay": num(r.Decay), "decayTau": num(r.DecayTau), "sustain": num(r.Sustain), "sustainPct": num(r.SustainPct),
			"sustainDB": num(r.SustainDB), "hasSustain": r.HasSustain, "release": num(r.Release), "releaseTau": num(r.ReleaseTau)}}
	case "mod":
		d := Demodulate(v.X, v.Fs, v.Fc, 1000)
		m := AnalyzeModulation(d)
		k := min(50, len(d.Amp))
		return map[string]any{"n": float64(len(d.Amp)), "amp": nums(d.Amp[:k]), "freq": nums(d.Freq[:k]), "mod": map[string]any{
			"ok": m.OK, "why": m.Why, "fc": num(m.Fc), "hasAM": m.HasAM, "amPct": num(m.AMPct), "amDB": num(m.AMDB), "amRate": num(m.AMRate),
			"amShape": m.AMShape, "hasFM": m.HasFM, "fmHz": num(m.FMHz), "fmCents": num(m.FMCents), "fmRate": num(m.FMRate),
			"fmShape": m.FMShape, "wowPct": num(m.WowPct), "flutterPct": num(m.FlutterPct), "wfPct": num(m.WFPct),
			"amCycles": float64(m.AMCycles), "fmCycles": float64(m.FMCycles)}}
	case "sidebands":
		sp := AudioSpectrum(v.X, v.Fs, v.N)
		s := AnalyzeSidebands(sp.DBV, sp.BinHz)
		return map[string]any{"ok": s.OK, "why": s.Why, "fc": num(s.Fc), "fm": num(s.Fm), "ratio": s.Ratio, "beta": num(s.Beta),
			"fit": num(s.Fit), "lines": float64(s.Lines)}
	case "lfo":
		l := AnalyzeLFO(v.X, v.Fs)
		return map[string]any{"ok": l.OK, "why": l.Why, "rateHz": num(l.RateHz), "cycles": float64(l.Cycles), "min": num(l.Min),
			"max": num(l.Max), "vpp": num(l.Vpp), "mean": num(l.Mean), "risePct": num(l.RisePct), "shape": l.Shape}
	case "harm":
		sp := AudioSpectrum(v.X, v.Fs, v.N)
		h := Harmonics(sp, v.F0)
		var hs []any
		for i := range h.H {
			if !h.OK && i > 0 {
				break
			}
			if h.H[i].N == 0 {
				break
			}
			x := h.H[i]
			hs = append(hs, map[string]any{"n": float64(x.N), "hz": num(x.Hz), "dbv": num(x.DBV), "relDB": num(x.RelDB), "found": x.Found})
		}
		k := min(64, len(sp.DBV))
		return map[string]any{"binHz": num(sp.BinHz), "dbv": nums(sp.DBV[:k]), "h": map[string]any{"ok": h.OK, "f0": num(h.F0), "h": hs,
			"thdPct": num(h.THDPct), "thdDB": num(h.THDDB), "evenDB": num(h.EvenDB), "oddDB": num(h.OddDB), "evenOdd": num(h.EvenOdd),
			"floorDBV": num(h.FloorDBV), "character": h.Character, "hum": map[string]any{"found": h.Hum.Found, "mainHz": num(h.Hum.MainHz),
				"dbv": num(h.Hum.DBV), "sumDBV": num(h.Hum.SumDBV)}}}
	case "ab":
		r := CompareAB(v.X, v.Y, v.Fs, v.F0)
		return map[string]any{"ok": r.OK, "why": r.Why, "f0": num(r.F0), "gainDB": num(r.GainDB), "phaseDeg": num(r.PhaseDeg),
			"rmsGainDB": num(r.RMSGainDB), "delayS": num(r.DelayS), "delayOK": r.DelayOK, "thdA": num(r.THDA), "thdB": num(r.THDB),
			"addedPct": num(r.AddedPct), "addedDB": num(r.AddedDB), "match": num(r.Match)}
	case "voct":
		f := FitVOct(v.Pts, 440)
		var rows []any
		for _, r := range f.Rows {
			rows = append(rows, map[string]any{"v": r.V, "hz": r.Hz, "note": r.Note, "errCents": num(r.ErrCents), "residCents": num(r.ResidCents)})
		}
		return map[string]any{"ok": f.OK, "rows": rows, "octPerV": num(f.OctPerV), "scaleCents": num(f.ScaleCents), "trimPct": num(f.TrimPct),
			"hfDevCents": num(f.HFDevCents), "spanOctaves": num(f.SpanOctaves)}
	case "columns":
		lo, hi := MinMaxColumns(v.X, v.N)
		return map[string]any{"lo": nums(lo), "hi": nums(hi)}
	case "cycle":
		at, ok := PlaceMark(v.X, v.Mark, 0, true)
		out := map[string]any{"at": num(at), "ok": ok, "c": nil}
		if ok {
			c := ExtractCycles(v.X, v.Fs, at, v.Period, 2, 64)
			out["c"] = map[string]any{"ok": c.OK, "trace": nums(c.Trace), "cycles": float64(c.Cycles), "periodS": num(c.PeriodS),
				"min": num(c.Min), "max": num(c.Max), "vpp": num(c.Vpp), "dutyPct": num(c.DutyPct), "riseS": num(c.RiseS),
				"fallS": num(c.FallS), "slewVus": num(c.SlewVus)}
		}
		return out
	}
	return nil
}

// same compares a Go reading with the JS one: every Go key must be there,
// numbers to 1e-6 relative (or 1e-9 absolute), strings and booleans exactly.
// TRLC-LINKS: REQ-SDS-266
func same(path string, g, j any) error {
	switch gv := g.(type) {
	case map[string]any:
		jm, ok := j.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: JS has %T", path, j)
		}
		for k, v := range gv {
			if err := same(path+"."+k, v, jm[k]); err != nil {
				return err
			}
		}
	case []any:
		ja, ok := j.([]any)
		if !ok && len(gv) == 0 && j == nil {
			return nil
		}
		if !ok || len(ja) != len(gv) {
			return fmt.Errorf("%s: length %d, JS %v", path, len(gv), j)
		}
		for i := range gv {
			if err := same(fmt.Sprintf("%s[%d]", path, i), gv[i], ja[i]); err != nil {
				return err
			}
		}
	case float64:
		jv, ok := j.(float64)
		if !ok || math.Abs(gv-jv) > 1e-9+1e-6*math.Abs(gv) {
			return fmt.Errorf("%s: Go %v JS %v", path, gv, j)
		}
	case nil:
		if j != nil {
			return fmt.Errorf("%s: Go nil JS %v", path, j)
		}
	default:
		if g != j {
			return fmt.Errorf("%s: Go %v JS %v", path, g, j)
		}
	}
	return nil
}

// TestSoundJSParity runs sound.js and the Go analysis on the same synthetic
// audio: tones, chords, tremolo, vibrato, FM, envelopes, CV steps, LFOs, a
// circuit's input and output. Skips without node (fails under
// CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-266, REQ-SDS-267, REQ-SDS-268, REQ-SDS-269, REQ-SDS-270, REQ-SDS-271, REQ-SDS-272
func TestSoundJSParity(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	fs := fsTest
	var vs []pvec
	add := func(v pvec) { vs = append(vs, v) }
	chord := func(t float64) float64 {
		return math.Sin(2*math.Pi*220*t) + 0.8*math.Sin(2*math.Pi*277.18*t) + 0.7*math.Sin(2*math.Pi*329.63*t)
	}
	add(pvec{Kind: "measure", Name: "sine on DC", X: gen(6000, fs, func(t float64) float64 { return 0.3 + sine(2, 997, 0)(t) }, 0.01, 1)})
	add(pvec{Kind: "measure", Name: "empty"})
	for i, f := range []float64{41.2, 110, 440, 1760.3, 4186} {
		add(pvec{Kind: "pitch", Name: fmt.Sprintf("sine %g", f), Fs: fs, X: quantize(gen(TunerWindow(f, fs), fs, sine(1.5, f, float64(i)), 0.01, int64(i)), 0.04)})
	}
	add(pvec{Kind: "pitch", Name: "square 98", Fs: fs, X: gen(16384, fs, square(1, 98, fs), 0.003, 7)})
	add(pvec{Kind: "pitch", Name: "chord", Fs: fs, X: gen(8192, fs, chord, 0.003, 8)})
	add(pvec{Kind: "pitch", Name: "bass range", Fs: fs, Range: "bass", X: gen(32768, fs, sine(1, 30.87, 0), 0.003, 9)})
	add(pvec{Kind: "pitch", Name: "noise", Fs: fs, X: gen(8192, fs, func(float64) float64 { return 0 }, 1, 10)})
	for _, f := range []float64{440, 261.6, 27.5, 8.2, 12543.85, 0} {
		add(pvec{Kind: "note", Name: fmt.Sprint(f), F: f, A4: 440})
	}
	add(pvec{Kind: "note", Name: "432", F: 440, A4: 432})
	for _, f := range []float64{0, 1e-300, 1e-4, 0.5, 20, 440, 5000, 1e9} {
		add(pvec{Kind: "window", Name: fmt.Sprint("window ", f), F: f, Fs: fs})
	}
	n := int(1.4 * fs)
	add(pvec{Kind: "adsr", Name: "tone gated", Fs: fs, X: gen(n, fs, adsrSignal(0.05, 0.04, 0.5, 0.6, 0.08, false), 0.002, 11), On: 0.1, Off: 0.75})
	add(pvec{Kind: "adsr", Name: "tone found", Fs: fs, X: gen(n, fs, adsrSignal(0.02, 0.1, 0.3, 0.5, 0.2, false), 0.002, 12), On: -1, Off: -1})
	add(pvec{Kind: "adsr", Name: "cv found", Fs: fs, CV: true, X: gen(n, fs, adsrSignal(0.1, 0.05, 0.7, 0.6, 0.1, true), 0.002, 13), On: -1, Off: -1})
	add(pvec{Kind: "adsr", Name: "held to the end", Fs: fs, X: gen(n, fs, adsrSignal(0.02, 0.1, 0.6, 5, 0.2, false), 0.002, 25), On: -1, Off: -1})
	add(pvec{Kind: "adsr", Name: "percussive", Fs: fs, X: gen(n, fs, func(t float64) float64 {
		if t < 0.1 {
			return 0
		}
		return math.Exp(-(t-0.1)/0.15) * math.Sin(2*math.Pi*300*t)
	}, 0.001, 14), On: -1, Off: -1})
	m := int(2 * fs)
	add(pvec{Kind: "mod", Name: "tremolo", Fs: fs, Fc: 440, X: gen(m, fs, func(t float64) float64 {
		return (1 + 0.3*math.Sin(2*math.Pi*5*t)) * math.Sin(2*math.Pi*440*t)
	}, 0.005, 15)})
	add(pvec{Kind: "mod", Name: "vibrato", Fs: fs, Fc: 330, X: gen(m, fs, func(t float64) float64 {
		return math.Sin(2*math.Pi*330*t - 330*(math.Pow(2, 30.0/1200)-1)/5.5*math.Cos(2*math.Pi*5.5*t))
	}, 0.005, 16)})
	add(pvec{Kind: "sidebands", Name: "fm 3000/600 b2", Fs: fs, N: 32768, X: gen(32768, fs, func(t float64) float64 {
		return math.Sin(2*math.Pi*3000*t + 2*math.Sin(2*math.Pi*600*t))
	}, 0.0005, 17)})
	add(pvec{Kind: "sidebands", Name: "plain sine", Fs: fs, N: 16384, X: gen(16384, fs, sine(1, 1000, 0), 0.001, 18)})
	add(pvec{Kind: "lfo", Name: "triangle", Fs: fs, X: gen(int(3*fs), fs, func(t float64) float64 { return 2 * (2*math.Abs(2*(t*2-math.Floor(t*2+0.5))) - 1) }, 0.002, 19)})
	add(pvec{Kind: "lfo", Name: "flat", Fs: fs, X: make([]float64, 1000)})
	add(pvec{Kind: "harm", Name: "rich", Fs: fs, N: 16384, X: gen(16384, fs, func(t float64) float64 {
		return math.Sin(2*math.Pi*500*t) + 0.05*math.Sin(2*math.Pi*1000*t) + 0.1*math.Sin(2*math.Pi*1500*t) + 0.001*math.Sin(2*math.Pi*60*t)
	}, 1e-4, 20)})
	add(pvec{Kind: "harm", Name: "chord", Fs: fs, N: 8192, X: gen(8192, fs, chord, 1e-3, 21)})
	in := gen(16384, fs, chord, 0.001, 22)
	out := make([]float64, len(in))
	for i := 5; i < len(in); i++ {
		out[i] = math.Tanh(0.8 * in[i-5])
	}
	add(pvec{Kind: "ab", Name: "clipper", Fs: fs, X: in, Y: out, F0: 220})
	add(pvec{Kind: "ab", Name: "found pitch", Fs: fs, X: gen(8192, fs, sine(1, 700, 0), 0.001, 23), Y: gen(8192, fs, sine(0.7, 700, 0.5), 0.001, 24)})
	var pts []VOctPoint
	for v := 0.0; v <= 4; v++ {
		pts = append(pts, VOctPoint{V: v + 0.01*v*v, Hz: 55 * math.Pow(2, 1.005*v)})
	}
	add(pvec{Kind: "voct", Name: "five points", Pts: pts})
	add(pvec{Kind: "voct", Name: "one point", Pts: pts[:1]})
	add(pvec{Kind: "cycle", Name: "square", Fs: fs, X: gen(4096, fs, square(1, 300, fs), 0, 0), Mark: int(2*fs/300) + 2, Period: fs / 300})
	for _, c := range []int{1, 7, 640, 5000} {
		add(pvec{Kind: "columns", Name: fmt.Sprint("columns ", c), X: gen(3001, fs, sine(1, 440, 0), 0.01, 26), N: c})
	}
	add(pvec{Kind: "columns", Name: "columns of none", N: 4})
	add(pvec{Kind: "cycle", Name: "no crossing", Fs: fs, X: make([]float64, 100), Mark: 50, Period: 20})

	dir := t.TempDir()
	blob, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	vf := filepath.Join(dir, "vectors.json")
	if err := os.WriteFile(vf, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("testdata", "soundparity.cjs"), vf, filepath.Join("..", "web"))
	cmd.Stderr = os.Stderr
	outJS, err := cmd.Output()
	if err != nil {
		t.Fatalf("sound.js run: %v", err)
	}
	var js []any
	if err := json.Unmarshal(outJS, &js); err != nil {
		t.Fatal(err)
	}
	if len(js) != len(vs) {
		t.Fatalf("%d JS results for %d vectors", len(js), len(vs))
	}
	for i, v := range vs {
		g := goReading(v)
		// Round-trip the Go side through JSON so both are plain values.
		b, _ := json.Marshal(g)
		var gv any
		_ = json.Unmarshal(b, &gv)
		if err := same(v.Kind+" "+v.Name, gv, js[i]); err != nil {
			t.Error(err)
		}
	}
}
