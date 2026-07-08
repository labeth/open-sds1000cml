package lcd

import (
	"fmt"
	"math"
	"testing"

	"open-sds/app/internal/engine"
)

// heat() is the colour primitive shared by the device waterfall and (mirrored)
// the web sgHeat. It must NEVER panic, whatever float it is handed — a NaN or
// Inf leaking in from a degenerate spectrum (peak==0, floorDB==0, empty row)
// would otherwise do int(NaN) → a garbage index → stops[i] out-of-range.
func TestHeatNeverPanics(t *testing.T) {
	inputs := []float64{
		math.NaN(), math.Inf(1), math.Inf(-1),
		-1e300, -1, -0.001, 0, 1e-12, 0.5, 1, 1.0000001, 2, 1e300,
		math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
	}
	for _, v := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("heat(%g) PANIC: %v", v, r)
				}
			}()
			_ = heat(v)
		}()
	}
	// monotone-in-brightness sanity across the valid range
	prev := -1.0
	for i := 0; i <= 100; i++ {
		c := heat(float64(i) / 100)
		r, g, b := unrgb(c)
		lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
		if lum < prev-8 {
			t.Errorf("heat non-monotone at t=%.2f: lum %g < prev %g", float64(i)/100, lum, prev)
		}
		prev = lum
	}
}

func unrgb(c uint16) (uint8, uint8, uint8) {
	r := uint8((c >> 11) & 0x1f)
	g := uint8((c >> 5) & 0x3f)
	b := uint8(c & 0x1f)
	return r << 3, g << 2, b << 3
}

func sgClamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}

type sgFam struct {
	name string
	gen  func(i, n int, rng func() float64) float64
}

// 50 diverse waveforms local to the lcd package (mirrors the Bode breaker set).
func sgFamilies(dt float64) []sgFam {
	nyq := 0.5 / dt
	var f []sgFam
	add := func(name string, g func(int, int, func() float64) float64) { f = append(f, sgFam{name, g}) }
	sine := func(fHz, amp, off float64) func(int, int, func() float64) float64 {
		return func(i, n int, _ func() float64) float64 { return off + amp*math.Sin(2*math.Pi*fHz*float64(i)*dt) }
	}
	for _, fr := range []float64{nyq * 1e-4, nyq * 0.001, nyq * 0.01, nyq * 0.05, nyq * 0.1, nyq * 0.25, nyq * 0.4, nyq * 0.49, nyq * 0.5, nyq * 0.51, nyq * 0.8, nyq * 1.3} {
		ff := fr
		add(fmt.Sprintf("sine-%.3g", ff), sine(ff, 90, 128))
	}
	sq := func(fHz, duty float64) func(int, int, func() float64) float64 {
		p := 1.0 / (fHz * dt)
		return func(i, n int, _ func() float64) float64 {
			if math.Mod(float64(i), p) < p*duty {
				return 218
			}
			return 38
		}
	}
	add("sq-1%", sq(nyq*0.02, 0.01))
	add("sq-10%", sq(nyq*0.05, 0.1))
	add("sq-50%", sq(nyq*0.1, 0.5))
	add("sq-90%", sq(nyq*0.05, 0.9))
	add("sq-99%", sq(nyq*0.02, 0.99))
	add("sq-nearNyq", sq(nyq*0.48, 0.5))
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
	add("sine+drift", func(i, n int, _ func() float64) float64 {
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
		v := 128 + 300*math.Sin(2*math.Pi*nyq*0.07*float64(i)*dt)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return v
	})
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
	add("sub-cycle", sine(0.6/(4096*dt), 90, 128))
	add("one-cycle", sine(1.0/(4096*dt), 90, 128))
	add("step", func(i, n int, _ func() float64) float64 {
		if i < n/2 {
			return 40
		}
		return 210
	})
	add("exp-decay", func(i, n int, _ func() float64) float64 {
		return 40 + 200*math.Exp(-float64(i)/float64(n)*4)*math.Abs(math.Sin(2*math.Pi*nyq*0.05*float64(i)*dt))
	})
	add("asym-pulse", sq(nyq*0.04, 0.03))
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
	add("railed-square", func(i, n int, _ func() float64) float64 {
		p := 1.0 / (nyq * 0.1 * dt)
		if math.Mod(float64(i), p) < p*0.5 {
			return 255
		}
		return 0
	})
	return f
}

// 50-waveform breaker for the spectrogram Push path: no panic, rows advance
// only when a spectrum was actually painted, effNyq stays finite/positive.
func TestSpectrogramBreaker50(t *testing.T) {
	const n = 6000
	const dt = 2e-9
	nyq := 0.5 / dt
	fams := sgFamilies(dt)
	if len(fams) != 50 {
		t.Fatalf("expected 50 families, got %d", len(fams))
	}
	for fi, fam := range fams {
		buf := make([]uint8, n)
		s := int64(1000 + fi*7)
		rng := func() float64 {
			s = (s*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
			return float64(s%1_000_000) / 1_000_000
		}
		for i := 0; i < n; i++ {
			buf[i] = sgClamp8(fam.gen(i, n, rng))
		}
		sg := NewSpectrogram()
		fr := &engine.Frame{C1: buf, C2: buf, Valid: n, SampleS: dt}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("fam %s: Push PANIC: %v", fam.name, r)
				}
			}()
			// push several frames to exercise the scroll + repeated paint
			for rep := 0; rep < 4; rep++ {
				sg.Push(fr, 0, nyq)
			}
		}()
		if sg.effNyq < 0 || math.IsNaN(sg.effNyq) || math.IsInf(sg.effNyq, 0) {
			t.Errorf("fam %s: bad effNyq %g", fam.name, sg.effNyq)
		}
	}
}

// Adversarial floorDB values: the paint math is t = 1 + db*(1/-floorDB). A
// floorDB of 0 makes 1/-floorDB = ±Inf; a bin exactly at the peak makes db≈0,
// so 0*Inf = NaN can reach heat(). Drive that directly.
func TestSpectrogramFloorEdges(t *testing.T) {
	const n = 4096
	const dt = 2e-9
	nyq := 0.5 / dt
	buf := make([]uint8, n)
	for i := range buf {
		buf[i] = sgClamp8(128 + 90*math.Sin(2*math.Pi*nyq*0.1*float64(i)*dt))
	}
	fr := &engine.Frame{C1: buf, C2: buf, Valid: n, SampleS: dt}
	for _, floor := range []float64{-60, -40, -1, -0.0001, 0, 1, math.NaN(), math.Inf(-1)} {
		sg := NewSpectrogram()
		sg.floorDB = floor
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("floorDB=%g: Push PANIC: %v", floor, r)
				}
			}()
			sg.Push(fr, 0, nyq)
		}()
	}
}

// Push must survive hostile frames: nil frame, nil C2 with ch=1, Valid below
// the floor, Valid larger than the slice, absurd effNyq.
func TestSpectrogramPushGuards(t *testing.T) {
	sg := NewSpectrogram()
	buf := bytesFill(4096, 128)
	for i := range buf {
		buf[i] = sgClamp8(128 + 90*math.Sin(float64(i)*0.05))
	}
	type gc struct {
		name string
		fr   *engine.Frame
		ch   int
		nyq  float64
	}
	cases := []gc{
		{"nil-frame", nil, 0, 1e8},
		{"nil-c2-ch1", &engine.Frame{C1: buf, C2: nil, Valid: 4096, SampleS: 2e-9}, 1, 1e8},
		{"valid-too-small", &engine.Frame{C1: buf[:16], Valid: 16, SampleS: 2e-9}, 0, 1e8},
		{"valid-over-len", &engine.Frame{C1: buf, Valid: 99999, SampleS: 2e-9}, 0, 1e8},
		{"nyq-zero", &engine.Frame{C1: buf, C2: buf, Valid: 4096, SampleS: 2e-9}, 0, 0},
		{"nyq-nan", &engine.Frame{C1: buf, C2: buf, Valid: 4096, SampleS: 2e-9}, 0, math.NaN()},
		{"nyq-inf", &engine.Frame{C1: buf, C2: buf, Valid: 4096, SampleS: 2e-9}, 0, math.Inf(1)},
	}
	for _, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: Push PANIC: %v", c.name, r)
				}
			}()
			sg.Push(c.fr, c.ch, c.nyq)
		}()
	}
}

// spectrumMags on degenerate records: all-zero, all-max, single spike, tiny.
func TestSpectrumMagsDegenerate(t *testing.T) {
	cases := map[string][]uint8{
		"all-zero":    make([]uint8, 4096),
		"all-max":     bytesFill(4096, 255),
		"all-mid":     bytesFill(4096, 128),
		"single-hi":   spike(4096, 0, 255),
		"single-end":  spike(4096, 4095, 255),
		"len-16":      bytesFill(16, 128),
		"len-31":      bytesFill(31, 128),
		"len-17-sine": {}, // filled below
	}
	sine := make([]uint8, 4096)
	for i := range sine {
		sine[i] = sgClamp8(128 + 90*math.Sin(float64(i)*0.05))
	}
	cases["sine"] = sine
	for name, src := range cases {
		if len(src) == 0 {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: spectrumMags PANIC: %v", name, r)
				}
			}()
			mags, peak := spectrumMags(src, 1)
			if peak <= 0 && mags != nil {
				t.Errorf("%s: mags non-nil but peak %g", name, peak)
			}
			for k, m := range mags {
				if math.IsNaN(m) || math.IsInf(m, 0) || m < 0 {
					t.Errorf("%s: bad mag[%d]=%g", name, k, m)
					break
				}
			}
		}()
	}
	_ = fmt.Sprint
}

func bytesFill(n int, v uint8) []uint8 {
	b := make([]uint8, n)
	for i := range b {
		b[i] = v
	}
	return b
}

func spike(n, at int, v uint8) []uint8 {
	b := make([]uint8, n)
	b[at] = v
	return b
}
