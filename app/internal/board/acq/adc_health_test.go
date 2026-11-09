// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"
	"math/rand"
	"slices"
	"testing"
	"time"
)

// snapsOf samples a signal at n random instants, as the fabric's snapshots
// do: core (p, ch) reads sig(t + its sampling phase) plus noise, held cores
// read their held code.
// TRLC-LINKS: REQ-SDS-231
func snapsOf(n int, seed int64, noise float64, sig func(t float64) float64, held map[int]uint8) [][10]uint8 {
	r := rand.New(rand.NewSource(seed))
	out := make([][10]uint8, n)
	for i := range out {
		t := r.Float64()
		for b := range 10 {
			if v, ok := held[b]; ok {
				out[i][b] = v
				continue
			}
			v := sig(t+float64(b)*1e-9) + r.NormFloat64()*noise
			out[i][b] = uint8(math.Max(0, math.Min(255, math.Round(v))))
		}
	}
	return out
}

// TRLC-LINKS: REQ-SDS-231
func adcSquare(t float64) float64 {
	if math.Mod(t*1e4, 1) < .5 {
		return 90
	}
	return 160
}

// A core holding one code under a moving input is stuck; a quiet, flat,
// clipped or slow input moves no core and finds none.
// TRLC-LINKS: REQ-SDS-231
func TestStuckCores(t *testing.T) {
	sine := func(t float64) float64 { return 128 + 60*math.Sin(2*math.Pi*1e4*t) }
	for _, c := range []struct {
		name  string
		snaps [][10]uint8
		want  uint16
	}{
		{"healthy adcSquare", snapsOf(adcSnaps, 1, .6, adcSquare, nil), 0},
		{"healthy sine", snapsOf(adcSnaps, 2, .6, sine, nil), 0},
		// converters E2 and E4: both cores each (bits 2,3 and 6,7)
		{"stuck pairs", snapsOf(adcSnaps, 3, .6, adcSquare, map[int]uint8{2: 165, 3: 177, 6: 181, 7: 120}), 0b11001100},
		// one core of a channel whose other cores move
		{"stuck core", snapsOf(adcSnaps, 4, .6, sine, map[int]uint8{9: 97}), 1 << 9},
		// four of a channel's five cores held: the one moving is evidence
		{"one core moving", snapsOf(adcSnaps, 5, .6, adcSquare, map[int]uint8{0: 85, 2: 90, 4: 91, 6: 97}), 0b1010101},
		{"quiet input", snapsOf(adcSnaps, 6, .7, func(float64) float64 { return 128 }, nil), 0},
		{"quiet input, one held", snapsOf(adcSnaps, 7, .7, func(float64) float64 { return 128 }, map[int]uint8{4: 128}), 0},
		{"noise-free flat", snapsOf(adcSnaps, 8, 0, func(float64) float64 { return 131 }, nil), 0},
		{"clipped high", snapsOf(adcSnaps, 9, .6, func(float64) float64 { return 400 }, nil), 0},
		{"clipped low, one held", snapsOf(adcSnaps, 10, .6, func(float64) float64 { return -50 }, map[int]uint8{1: 90}), 0},
		// a sine clipped at both rails still moves every live core
		{"clipped sine", snapsOf(adcSnaps, 11, .6, func(t float64) float64 { return 128 + 400*math.Sin(2*math.Pi*1e4*t) }, map[int]uint8{5: 255}), 1 << 5},
		// a slow input: every instant reads the same level
		{"slow", snapsOf(adcSnaps, 12, 0, func(t float64) float64 { return 100 + t }, nil), 0},
		{"too few snapshots", snapsOf(adcSnaps/4, 13, .6, adcSquare, map[int]uint8{2: 165}), 0},
	} {
		if got := stuckCores(c.snaps); got != c.want {
			t.Errorf("%s: stuck %010b, want %010b", c.name, got, c.want)
		}
	}
	if got := coreNames(0b11001100 | 1<<9); got != "E2, E4, E5 CH2" {
		t.Errorf("names %q", got)
	}
}

// A full-rate record shows a held core as every fifth sample at one code;
// one brings the snapshot check forward, at most one look a second.
// TRLC-LINKS: REQ-SDS-231
func TestRecordStuckBringsCheckForward(t *testing.T) {
	rec := func(held int, quiet bool) ([]uint8, []uint8) {
		r := rand.New(rand.NewSource(int64(held) + 7))
		c1, c2 := make([]uint8, 5000), make([]uint8, 5000)
		for i := range c1 {
			v := 128 + 50*math.Sin(2*math.Pi*float64(i)/37)
			if quiet {
				v = 128
			}
			c1[i] = uint8(v + r.NormFloat64())
			c2[i] = uint8(255 - v + r.NormFloat64())
			if held >= 0 && i%5 == held {
				c1[i] = 93
			}
		}
		return c1, c2
	}
	if s := recordStuck(rec(-1, false)); s != 0 {
		t.Errorf("healthy record: %010b", s)
	}
	if s := recordStuck(rec(3, false)); s != 1<<6 {
		t.Errorf("held core 3 of CH1: %010b", s)
	}
	if s := recordStuck(rec(3, true)); s != 0 {
		t.Errorf("quiet record: %010b", s)
	}
	if s := recordStuck(make([]uint8, 100), make([]uint8, 100)); s != 0 {
		t.Errorf("short record: %010b", s)
	}
	b := New(Config{})
	now := time.Unix(1000, 0)
	b.SetLog(nil, func() time.Time { return now })
	b.adc.next = now.Add(adcCheckEvery)
	b.NoteRecordCores(rec(-1, false))
	if !b.adc.next.After(now) {
		t.Error("a healthy record brought the check forward")
	}
	b.NoteRecordCores(rec(2, false))
	if !b.adc.next.After(now) {
		t.Error("looked again within a second")
	}
	now = now.Add(adcFrameEvery)
	b.NoteRecordCores(rec(2, false))
	if b.adc.next.After(now) {
		t.Error("a held core did not bring the check forward")
	}
}

// TRLC-LINKS: REQ-SDS-231
func BenchmarkRecordStuck(b *testing.B) {
	c1, c2 := make([]uint8, 100003), make([]uint8, 100003)
	for i := range c1 {
		c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*float64(i)/37))
		c2[i] = 255 - c1[i]
	}
	for b.Loop() {
		recordStuck(c1, c2)
	}
}

// stuckCores by histogram names the cores the sorting version did.
// TRLC-LINKS: REQ-SDS-231
func TestStuckCoresSame(t *testing.T) {
	ref := func(snaps [][10]uint8) uint16 {
		n := len(snaps)
		if n < adcSnaps/2 {
			return 0
		}
		var stuck uint16
		vals := make([]int, n)
		for ch := range 2 {
			var held [5]bool
			moving := false
			for p := range 5 {
				for i, s := range snaps {
					vals[i] = int(s[2*p+ch])
				}
				first, same := vals[0], true
				for _, v := range vals {
					same = same && v == first
				}
				held[p] = same
				if same {
					continue
				}
				slices.Sort(vals)
				med, away := vals[n/2], 0
				for _, v := range vals {
					if v-med >= adcMoving || med-v >= adcMoving {
						away++
					}
				}
				if away >= n/8 {
					moving = true
				}
			}
			for p, h := range held {
				if h && moving {
					stuck |= 1 << (2*p + ch)
				}
			}
		}
		return stuck
	}
	for seed := range 300 {
		n := adcSnaps/2 + seed%400
		snaps := make([][10]uint8, n)
		for j := range snaps {
			for k := range 10 {
				switch (seed + k) % 4 {
				case 0: // held
					snaps[j][k] = uint8(seed)
				case 1: // moving widely
					snaps[j][k] = uint8((j*7919 + k*31 + seed) % 256)
				case 2: // a little noise
					snaps[j][k] = uint8(128 + (j*seed+k)%3)
				default: // mostly one code, sometimes away
					snaps[j][k] = 100
					if (j+seed)%7 == 0 {
						snaps[j][k] = 200
					}
				}
			}
		}
		if got, want := stuckCores(snaps), ref(snaps); got != want {
			t.Fatalf("seed %d: %#x, want %#x", seed, got, want)
		}
	}
}
